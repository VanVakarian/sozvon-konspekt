package yadisk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

const apiBaseURL = "https://cloud-api.yandex.net/v1/disk"

var ErrAlreadyExists = errors.New("resource already exists")

type TokenSource interface {
	AccessToken(context.Context) (string, error)
	ForceRefresh(context.Context) (string, error)
}

type ClientConfig struct {
	TokenSource TokenSource
	Timeout     time.Duration
	Logger      *slog.Logger
}

type Client struct {
	tokenSource TokenSource
	httpClient  *http.Client
	logger      *slog.Logger
}

type Resource struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Type     string    `json:"type"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

type folderResponse struct {
	Embedded resourceList `json:"_embedded"`
}

type resourceList struct {
	Items  []Resource `json:"items"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
	Total  int        `json:"total"`
}

type linkResponse struct {
	Href      string `json:"href"`
	Method    string `json:"method"`
	Templated bool   `json:"templated"`
}

type apiErrorResponse struct {
	Code        string `json:"error"`
	Description string `json:"description"`
	Message     string `json:"message"`
}

type APIError struct {
	Status      int
	Code        string
	Description string
	RetryAfter  time.Duration
	Body        string
}

func NewClient(cfg ClientConfig) (*Client, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	if cfg.TokenSource == nil {
		return nil, errors.New("token source is required")
	}

	return &Client{
		tokenSource: cfg.TokenSource,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		logger: logger,
	}, nil
}

func (c *Client) ListFolder(ctx context.Context, folderPath string) ([]Resource, error) {
	fields := strings.Join([]string{
		"_embedded.items.name",
		"_embedded.items.path",
		"_embedded.items.type",
		"_embedded.items.size",
		"_embedded.items.modified",
		"_embedded.limit",
		"_embedded.offset",
		"_embedded.total",
	}, ",")

	resources := make([]Resource, 0)
	limit := 100
	offset := 0

	for {
		values := url.Values{}
		values.Set("path", folderPath)
		values.Set("fields", fields)
		values.Set("limit", strconv.Itoa(limit))
		values.Set("offset", strconv.Itoa(offset))

		requestURL := apiBaseURL + "/resources?" + values.Encode()

		var response folderResponse
		if err := c.getJSON(ctx, requestURL, &response); err != nil {
			return nil, err
		}

		resources = append(resources, response.Embedded.Items...)

		if len(response.Embedded.Items) == 0 {
			break
		}

		nextOffset := response.Embedded.Offset + len(response.Embedded.Items)
		if nextOffset >= response.Embedded.Total {
			break
		}

		offset = nextOffset
	}

	return resources, nil
}

func (c *Client) DownloadToFile(ctx context.Context, remotePath string, localPath string) error {
	link, err := c.getDownloadLink(ctx, remotePath)
	if err != nil {
		return err
	}

	return c.doWithRetry(ctx, "download file", func(ctx context.Context) error {
		currentURL := link.Href

		for redirectCount := 0; redirectCount < 5; redirectCount++ {
			resp, err := c.doAuthorized(ctx, func(ctx context.Context, token string) (*http.Request, error) {
				req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, currentURL, nil)
				if reqErr != nil {
					return nil, reqErr
				}

				req.Header.Set("Authorization", "OAuth "+token)
				return req, nil
			})
			if err != nil {
				return err
			}

			switch {
			case resp.StatusCode == http.StatusOK:
				file, err := os.Create(localPath)
				if err != nil {
					resp.Body.Close()
					return err
				}

				_, copyErr := io.Copy(file, resp.Body)
				closeBodyErr := resp.Body.Close()
				closeFileErr := file.Close()

				if copyErr != nil {
					return copyErr
				}

				if closeBodyErr != nil {
					return closeBodyErr
				}

				if closeFileErr != nil {
					return closeFileErr
				}

				return nil
			case isRedirectStatus(resp.StatusCode):
				location, locationErr := resp.Location()
				resp.Body.Close()
				if locationErr != nil {
					return locationErr
				}

				currentURL = location.String()
			default:
				apiErr := parseAPIError(resp)
				resp.Body.Close()
				return apiErr
			}
		}

		return errors.New("too many download redirects")
	})
}

func (c *Client) UploadBytes(ctx context.Context, remotePath string, body []byte, overwrite bool) error {
	link, err := c.getUploadLink(ctx, remotePath, overwrite)
	if err != nil {
		return err
	}

	contentType := "application/octet-stream"
	if strings.EqualFold(path.Ext(remotePath), ".txt") {
		contentType = "text/plain; charset=utf-8"
	}

	return c.doWithRetry(ctx, "upload file", func(ctx context.Context) error {
		reader := bytes.NewReader(body)

		req, err := http.NewRequestWithContext(ctx, http.MethodPut, link.Href, reader)
		if err != nil {
			return err
		}

		req.Header.Set("Content-Type", contentType)
		req.ContentLength = int64(len(body))

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusAccepted {
			return nil
		}

		return parseAPIError(resp)
	})
}

func (c *Client) getDownloadLink(ctx context.Context, remotePath string) (linkResponse, error) {
	values := url.Values{}
	values.Set("path", remotePath)
	values.Set("fields", "href,method,templated")

	requestURL := apiBaseURL + "/resources/download?" + values.Encode()

	var link linkResponse
	if err := c.getJSON(ctx, requestURL, &link); err != nil {
		return linkResponse{}, err
	}

	return link, nil
}

func (c *Client) getUploadLink(ctx context.Context, remotePath string, overwrite bool) (linkResponse, error) {
	values := url.Values{}
	values.Set("path", remotePath)
	values.Set("fields", "href,method,templated")
	if overwrite {
		values.Set("overwrite", "true")
	}

	requestURL := apiBaseURL + "/resources/upload?" + values.Encode()

	var link linkResponse
	err := c.doWithRetry(ctx, "request upload link", func(ctx context.Context) error {
		resp, doErr := c.doAuthorized(ctx, func(ctx context.Context, token string) (*http.Request, error) {
			req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
			if reqErr != nil {
				return nil, reqErr
			}

			req.Header.Set("Authorization", "OAuth "+token)
			req.Header.Set("Accept", "application/json")
			return req, nil
		})
		if doErr != nil {
			return doErr
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return json.NewDecoder(resp.Body).Decode(&link)
		}

		if resp.StatusCode == http.StatusConflict {
			return ErrAlreadyExists
		}

		return parseAPIError(resp)
	})
	if err != nil {
		return linkResponse{}, err
	}

	return link, nil
}

func (c *Client) getJSON(ctx context.Context, requestURL string, destination any) error {
	return c.doWithRetry(ctx, "request json", func(ctx context.Context) error {
		resp, err := c.doAuthorized(ctx, func(ctx context.Context, token string) (*http.Request, error) {
			req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
			if reqErr != nil {
				return nil, reqErr
			}

			req.Header.Set("Authorization", "OAuth "+token)
			req.Header.Set("Accept", "application/json")
			return req, nil
		})
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return parseAPIError(resp)
		}

		return json.NewDecoder(resp.Body).Decode(destination)
	})
}

func (c *Client) doAuthorized(ctx context.Context, buildRequest func(context.Context, string) (*http.Request, error)) (*http.Response, error) {
	token, err := c.tokenSource.AccessToken(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := c.doAuthorizedWithToken(ctx, token, buildRequest)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	c.logger.Warn("unauthorized, refreshing token")
	resp.Body.Close()

	token, err = c.tokenSource.ForceRefresh(ctx)
	if err != nil {
		return nil, err
	}

	return c.doAuthorizedWithToken(ctx, token, buildRequest)
}

func (c *Client) doAuthorizedWithToken(ctx context.Context, token string, buildRequest func(context.Context, string) (*http.Request, error)) (*http.Response, error) {
	req, err := buildRequest(ctx, token)
	if err != nil {
		return nil, err
	}

	return c.httpClient.Do(req)
}

func (c *Client) doWithRetry(ctx context.Context, operation string, fn func(context.Context) error) error {
	backoff := 2 * time.Second

	for attempt := 1; attempt <= 3; attempt++ {
		err := fn(ctx)
		if err == nil {
			return nil
		}

		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrAlreadyExists) {
			return err
		}

		if !isRetryableError(err) || attempt == 3 {
			return err
		}

		retryDelay := backoff
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
			retryDelay = apiErr.RetryAfter
		}

		c.logger.Warn("request failed, retrying", "operation", operation, "attempt", attempt, "retry in", retryDelay, "error", err)

		if waitErr := waitWithContext(ctx, retryDelay); waitErr != nil {
			return waitErr
		}

		backoff *= 2
	}

	return nil
}

func (e *APIError) Error() string {
	parts := []string{fmt.Sprintf("status %d", e.Status)}
	if e.Code != "" {
		parts = append(parts, e.Code)
	}
	if e.Description != "" {
		parts = append(parts, e.Description)
	}
	if e.Body != "" {
		parts = append(parts, e.Body)
	}
	return strings.Join(parts, ": ")
}

func (e *APIError) Retryable() bool {
	switch e.Status {
	case http.StatusLocked, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func isRetryableError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retryable()
	}

	var netErr net.Error
	return errors.As(err, &netErr)
}

func parseAPIError(resp *http.Response) error {
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	var apiPayload apiErrorResponse
	if err := json.Unmarshal(payload, &apiPayload); err == nil {
		return &APIError{
			Status:      resp.StatusCode,
			Code:        apiPayload.Code,
			Description: firstNonEmpty(apiPayload.Description, apiPayload.Message),
			RetryAfter:  parseRetryAfter(resp.Header.Get("Retry-After")),
			Body:        strings.TrimSpace(string(payload)),
		}
	}

	return &APIError{
		Status:      resp.StatusCode,
		RetryAfter:  parseRetryAfter(resp.Header.Get("Retry-After")),
		Body:        strings.TrimSpace(string(payload)),
		Description: http.StatusText(resp.StatusCode),
	}
}

func parseRetryAfter(value string) time.Duration {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(trimmed); err == nil {
		return time.Duration(seconds) * time.Second
	}

	if timestamp, err := http.ParseTime(trimmed); err == nil {
		return time.Until(timestamp)
	}

	return 0
}

func waitWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isRedirectStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}

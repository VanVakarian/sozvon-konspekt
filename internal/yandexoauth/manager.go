package yandexoauth

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	authorizeURL = "https://oauth.yandex.ru/authorize"
	tokenURL     = "https://oauth.yandex.ru/token"
)

type ManagerConfig struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	RedirectURI  string
	EnvFilePath  string
	Timeout      time.Duration
	Logger       *slog.Logger
}

type Manager struct {
	httpClient *http.Client
	logger     *slog.Logger

	clientID     string
	clientSecret string
	redirectURI  string
	envFilePath  string
	refreshToken string

	mu                sync.Mutex
	accessToken       string
	accessTokenExpiry time.Time
}

type tokenResponse struct {
	TokenType        string `json:"token_type"`
	AccessToken      string `json:"access_token"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func NewManager(cfg ManagerConfig) *Manager {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Manager{
		httpClient:   &http.Client{Timeout: cfg.Timeout},
		logger:       logger,
		clientID:     strings.TrimSpace(cfg.ClientID),
		clientSecret: strings.TrimSpace(cfg.ClientSecret),
		redirectURI:  strings.TrimSpace(cfg.RedirectURI),
		envFilePath:  strings.TrimSpace(cfg.EnvFilePath),
		refreshToken: strings.TrimSpace(cfg.RefreshToken),
	}
}

func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.accessToken != "" && time.Now().Before(m.accessTokenExpiry) {
		return m.accessToken, nil
	}

	return m.obtainAccessTokenLocked(ctx, false)
}

func (m *Manager) ForceRefresh(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.accessToken = ""
	m.accessTokenExpiry = time.Time{}

	return m.obtainAccessTokenLocked(ctx, true)
}

func (m *Manager) AuthorizationURL() string {
	return m.authorizationURL("")
}

func (m *Manager) authorizationURL(state string) string {
	values := url.Values{}
	values.Set("response_type", "code")
	values.Set("client_id", m.clientID)
	if m.redirectURI != "" {
		values.Set("redirect_uri", m.redirectURI)
	}
	if state != "" {
		values.Set("state", state)
	}

	return authorizeURL + "?" + values.Encode()
}

func (m *Manager) obtainAccessTokenLocked(ctx context.Context, forceRefresh bool) (string, error) {
	if m.accessToken != "" && !forceRefresh && time.Now().Before(m.accessTokenExpiry) {
		return m.accessToken, nil
	}

	var refreshErr error
	if m.refreshToken != "" {
		m.logger.Info("refreshing access token")
		response, err := m.requestToken(ctx, url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {m.refreshToken},
			"client_id":     {m.clientID},
			"client_secret": {m.clientSecret},
		})
		if err == nil {
			if applyErr := m.applyTokenResponseLocked(response); applyErr != nil {
				return "", applyErr
			}

			m.logger.Info("access token refreshed")
			return m.accessToken, nil
		}

		refreshErr = err
		m.logger.Warn("token refresh failed", "error", err)
	}

	m.logger.Info("authorization required", "redirect URI", m.redirectURI)
	m.logger.Info("scan continues after authorization")

	if code, err := m.waitForAuthorizationCodeLocked(ctx); err == nil {
		m.logger.Info("authorization code received")
		return m.exchangeAuthorizationCodeLocked(ctx, code, refreshErr)
	} else if refreshErr != nil {
		return "", errors.Join(refreshErr, err)
	} else {
		return "", err
	}
}

func (m *Manager) exchangeAuthorizationCodeLocked(ctx context.Context, authorizationCode string, refreshErr error) (string, error) {
	m.logger.Info("exchanging authorization code")

	response, err := m.requestToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {authorizationCode},
		"client_id":     {m.clientID},
		"client_secret": {m.clientSecret},
	})
	if err != nil {
		if refreshErr != nil {
			return "", errors.Join(refreshErr, err)
		}

		return "", err
	}

	if applyErr := m.applyTokenResponseLocked(response); applyErr != nil {
		return "", applyErr
	}

	m.logger.Info("authorization completed")

	return m.accessToken, nil
}

func (m *Manager) waitForAuthorizationCodeLocked(ctx context.Context) (string, error) {
	if !isScreenCodeRedirectURI(m.redirectURI) {
		return "", fmt.Errorf("unsupported oauth redirect uri %q: only https://oauth.yandex.ru/verification_code is supported", m.redirectURI)
	}

	return m.waitForAuthorizationCodeFromTerminal(ctx)
}

func (m *Manager) waitForAuthorizationCodeFromTerminal(ctx context.Context) (string, error) {
	m.logger.Info("authorization started", "URL", m.AuthorizationURL(), "redirect URI", m.redirectURI)
	m.logger.Info("after approving access in the browser, paste the confirmation code into this terminal and press Enter")

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			line, err := reader.ReadString('\n')
			code := strings.TrimSpace(line)
			if code != "" {
				codeCh <- code
				return
			}

			if err == nil {
				continue
			}

			if errors.Is(err, io.EOF) {
				errCh <- errors.New("stdin closed before confirmation code was entered")
				return
			}

			errCh <- err
			return
		}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case err := <-errCh:
		return "", err
	case code := <-codeCh:
		m.logger.Info("confirmation code received")
		return code, nil
	}
}

func (m *Manager) applyTokenResponseLocked(response tokenResponse) error {
	if strings.TrimSpace(response.AccessToken) == "" {
		return errors.New("oauth response does not contain access_token")
	}

	m.accessToken = strings.TrimSpace(response.AccessToken)
	ttl := time.Duration(response.ExpiresIn) * time.Second
	if ttl > time.Minute {
		m.accessTokenExpiry = time.Now().Add(ttl - time.Minute)
	} else if ttl > 0 {
		m.accessTokenExpiry = time.Now().Add(ttl)
	} else {
		m.accessTokenExpiry = time.Time{}
	}

	updates := make(map[string]string)
	if strings.TrimSpace(response.RefreshToken) != "" {
		m.refreshToken = strings.TrimSpace(response.RefreshToken)
		updates["YADISK_REFRESH_TOKEN"] = m.refreshToken
	}

	if len(updates) > 0 && m.envFilePath != "" {
		if err := updateEnvFile(m.envFilePath, updates); err != nil {
			return fmt.Errorf("persist oauth data: %w", err)
		}

		m.logger.Info("refresh token saved", "env file", m.envFilePath)
	}

	return nil
}

func (m *Manager) requestToken(ctx context.Context, values url.Values) (tokenResponse, error) {
	body := values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(body))
	if err != nil {
		return tokenResponse{}, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return tokenResponse{}, err
	}

	var token tokenResponse
	if unmarshalErr := json.Unmarshal(payload, &token); unmarshalErr != nil {
		return tokenResponse{}, fmt.Errorf("oauth response decode error: %w", unmarshalErr)
	}

	if resp.StatusCode != http.StatusOK {
		description := firstNonEmpty(token.ErrorDescription, token.Error, strings.TrimSpace(string(payload)), http.StatusText(resp.StatusCode))
		return tokenResponse{}, fmt.Errorf("oauth token request failed: status %d: %s", resp.StatusCode, description)
	}

	return token, nil

}

func updateEnvFile(filePath string, updates map[string]string) error {
	content, err := os.ReadFile(filePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	lines := make([]string, 0)
	if err == nil {
		normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
		lines = strings.Split(normalized, "\n")
	}

	seen := make(map[string]bool)
	for index, line := range lines {
		candidate := strings.TrimSpace(line)
		if candidate == "" || strings.HasPrefix(candidate, "#") {
			continue
		}

		if strings.HasPrefix(candidate, "export ") {
			candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "export "))
		}

		key, _, found := strings.Cut(candidate, "=")
		if !found {
			continue
		}

		key = strings.TrimSpace(key)
		value, ok := updates[key]
		if !ok {
			continue
		}

		lines[index] = key + "=" + formatEnvValue(value)
		seen[key] = true
	}

	for key, value := range updates {
		if seen[key] {
			continue
		}

		lines = append(lines, key+"="+formatEnvValue(value))
	}

	result := strings.Join(lines, "\n")
	result = strings.TrimRight(result, "\n") + "\n"

	return os.WriteFile(filePath, []byte(result), 0o600)
}

func formatEnvValue(value string) string {
	if value == "" {
		return ""
	}

	if strings.ContainsAny(value, " \t\n\r#\"'") {
		return strconv.Quote(value)
	}

	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}

func isScreenCodeRedirectURI(rawURL string) bool {
	parsedURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}

	host := strings.ToLower(parsedURL.Hostname())
	return (host == "oauth.yandex.ru" || host == "oauth.yandex.com") && parsedURL.EscapedPath() == "/verification_code"
}

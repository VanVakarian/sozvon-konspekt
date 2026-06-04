package yandexoauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	ListenAddr   string
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
	listenAddr   string
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

type callbackEndpoint struct {
	listenAddr string
	path       string
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
		listenAddr:   strings.TrimSpace(cfg.ListenAddr),
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

			return m.accessToken, nil
		}

		refreshErr = err
		m.logger.Warn("refresh token exchange failed", "error", err)
	}

	if code, err := m.waitForAuthorizationCodeLocked(ctx); err == nil {
		return m.exchangeAuthorizationCodeLocked(ctx, code, refreshErr)
	} else if refreshErr != nil {
		return "", errors.Join(refreshErr, err)
	} else {
		return "", err
	}
}

func (m *Manager) exchangeAuthorizationCodeLocked(ctx context.Context, authorizationCode string, refreshErr error) (string, error) {
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

	return m.accessToken, nil
}

func (m *Manager) waitForAuthorizationCodeLocked(ctx context.Context) (string, error) {
	endpoint, err := parseCallbackEndpoint(m.redirectURI, m.listenAddr)
	if err != nil {
		m.logger.Error("oauth authorization required", "url", m.AuthorizationURL())
		return "", fmt.Errorf("invalid oauth callback configuration: %w", err)
	}

	state, err := newOAuthState()
	if err != nil {
		return "", fmt.Errorf("generate oauth state: %w", err)
	}

	authorizationURL := m.authorizationURL(state)
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	serveErrCh := make(chan error, 1)
	mux := http.NewServeMux()
	server := &http.Server{
		Addr:              endpoint.listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	mux.HandleFunc(endpoint.path, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = writer.Write([]byte("Method not allowed\n"))
			return
		}

		if request.URL.Query().Get("state") != state {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte("Invalid state\n"))
			return
		}

		if oauthErr := strings.TrimSpace(request.URL.Query().Get("error")); oauthErr != "" {
			description := firstNonEmpty(request.URL.Query().Get("error_description"), oauthErr)
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte("Authorization failed\n"))
			select {
			case errCh <- fmt.Errorf("oauth authorization failed: %s", description):
			default:
			}
			go shutdownServer(server)
			return
		}

		code := strings.TrimSpace(request.URL.Query().Get("code"))
		if code == "" {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte("Missing code\n"))
			return
		}

		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("Authorization received. You can close this page.\n"))

		select {
		case codeCh <- code:
		default:
		}

		go shutdownServer(server)
	})

	listener, err := net.Listen("tcp", endpoint.listenAddr)
	if err != nil {
		m.logger.Error("oauth authorization required", "url", authorizationURL)
		return "", fmt.Errorf("start oauth callback listener on %s: %w", endpoint.listenAddr, err)
	}

	m.logger.Info("oauth bootstrap waiting for browser authorization", "url", authorizationURL, "callback", m.redirectURI)

	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serveErrCh <- serveErr
		}
	}()

	select {
	case <-ctx.Done():
		shutdownServer(server)
		return "", ctx.Err()
	case err := <-errCh:
		return "", err
	case err := <-serveErrCh:
		return "", fmt.Errorf("oauth callback server failed: %w", err)
	case code := <-codeCh:
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

func parseCallbackEndpoint(rawURL string, listenAddr string) (callbackEndpoint, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return callbackEndpoint{}, errors.New("redirect uri is empty")
	}

	parsedURL, err := url.Parse(trimmed)
	if err != nil {
		return callbackEndpoint{}, fmt.Errorf("parse redirect uri: %w", err)
	}

	if !strings.EqualFold(parsedURL.Scheme, "http") && !strings.EqualFold(parsedURL.Scheme, "https") {
		return callbackEndpoint{}, fmt.Errorf("redirect uri %q must use http or https", trimmed)
	}

	bindAddr := strings.TrimSpace(listenAddr)
	if bindAddr == "" {
		if !strings.EqualFold(parsedURL.Scheme, "http") {
			return callbackEndpoint{}, fmt.Errorf("redirect uri %q requires YADISK_OAUTH_LISTEN_ADDR for callback capture", trimmed)
		}

		port := parsedURL.Port()
		if port == "" {
			port = "80"
		}

		bindAddr = ":" + port
	}

	path := parsedURL.EscapedPath()
	if path == "" {
		path = "/"
	}

	return callbackEndpoint{
		listenAddr: bindAddr,
		path:       path,
	}, nil
}

func newOAuthState() (string, error) {
	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}

	return hex.EncodeToString(randomBytes), nil
}

func shutdownServer(server *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

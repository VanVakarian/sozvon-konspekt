package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ClientID              string
	ClientSecret          string
	RefreshToken          string
	RedirectURI           string
	EnvFilePath           string
	Folder                string
	PollInterval          time.Duration
	PlaceholderStaleAfter time.Duration
	HTTPTimeout           time.Duration
	LogLevel              slog.Level
}

func Load() (Config, error) {
	if err := loadDotEnvFile(".env"); err != nil {
		return Config{}, fmt.Errorf("load .env: %w", err)
	}

	cfg := Config{
		PollInterval:          60 * time.Second,
		PlaceholderStaleAfter: 300 * time.Second,
		HTTPTimeout:           120 * time.Second,
		LogLevel:              slog.LevelInfo,
	}

	var validationErrs []error

	cfg.ClientID = strings.TrimSpace(os.Getenv("YADISK_CLIENT_ID"))
	cfg.ClientSecret = strings.TrimSpace(os.Getenv("YADISK_CLIENT_SECRET"))
	cfg.RefreshToken = strings.TrimSpace(os.Getenv("YADISK_REFRESH_TOKEN"))
	cfg.RedirectURI = strings.TrimSpace(os.Getenv("YADISK_REDIRECT_URI"))
	cfg.EnvFilePath = ".env"

	cfg.Folder = normalizeDiskPath(os.Getenv("YADISK_FOLDER"))
	if cfg.Folder == "" {
		validationErrs = append(validationErrs, errors.New("YADISK_FOLDER is required"))
	}

	if cfg.ClientID == "" {
		validationErrs = append(validationErrs, errors.New("YADISK_CLIENT_ID is required"))
	}

	if cfg.ClientSecret == "" {
		validationErrs = append(validationErrs, errors.New("YADISK_CLIENT_SECRET is required"))
	}

	if cfg.RedirectURI == "" {
		validationErrs = append(validationErrs, errors.New("YADISK_REDIRECT_URI is required"))
	} else if err := validateRedirectURI(cfg.RedirectURI); err != nil {
		validationErrs = append(validationErrs, fmt.Errorf("YADISK_REDIRECT_URI: %w", err))
	}

	if value := strings.TrimSpace(os.Getenv("POLL_INTERVAL")); value != "" {
		duration, err := parseSeconds(value)
		if err != nil {
			validationErrs = append(validationErrs, fmt.Errorf("POLL_INTERVAL: %w", err))
		} else {
			cfg.PollInterval = duration
		}
	}

	if value := strings.TrimSpace(os.Getenv("PLACEHOLDER_STALE_AFTER")); value != "" {
		duration, err := parseSeconds(value)
		if err != nil {
			validationErrs = append(validationErrs, fmt.Errorf("PLACEHOLDER_STALE_AFTER: %w", err))
		} else {
			cfg.PlaceholderStaleAfter = duration
		}
	}

	if value := strings.TrimSpace(os.Getenv("HTTP_TIMEOUT")); value != "" {
		duration, err := parseSeconds(value)
		if err != nil {
			validationErrs = append(validationErrs, fmt.Errorf("HTTP_TIMEOUT: %w", err))
		} else {
			cfg.HTTPTimeout = duration
		}
	}

	if value := strings.TrimSpace(os.Getenv("LOG_LEVEL")); value != "" {
		level, err := parseLogLevel(value)
		if err != nil {
			validationErrs = append(validationErrs, err)
		} else {
			cfg.LogLevel = level
		}
	}

	if cfg.PollInterval <= 0 {
		validationErrs = append(validationErrs, errors.New("POLL_INTERVAL must be greater than zero"))
	}

	if cfg.PlaceholderStaleAfter <= 0 {
		validationErrs = append(validationErrs, errors.New("PLACEHOLDER_STALE_AFTER must be greater than zero"))
	}

	if cfg.HTTPTimeout <= 0 {
		validationErrs = append(validationErrs, errors.New("HTTP_TIMEOUT must be greater than zero"))
	}

	if len(validationErrs) > 0 {
		return Config{}, errors.Join(validationErrs...)
	}

	return cfg, nil
}

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("LOG_LEVEL must be one of: debug, info, warn, error")
	}
}

func parseSeconds(value string) (time.Duration, error) {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, errors.New("must be an integer number of seconds")
	}

	if seconds <= 0 {
		return 0, errors.New("must be greater than zero")
	}

	return time.Duration(seconds) * time.Second, nil
}

func validateRedirectURI(rawURL string) error {
	parsedURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsedURL.Scheme == "" || parsedURL.Host == "" {
		return errors.New("absolute URL is required")
	}

	host := strings.ToLower(parsedURL.Hostname())
	if (host != "oauth.yandex.ru" && host != "oauth.yandex.com") || parsedURL.EscapedPath() != "/verification_code" {
		return errors.New("must be https://oauth.yandex.ru/verification_code")
	}

	return nil
}

func normalizeDiskPath(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	if strings.HasPrefix(trimmed, "disk:") {
		cleaned := path.Clean(strings.TrimPrefix(trimmed, "disk:"))
		if cleaned == "." {
			cleaned = "/"
		}
		if !strings.HasPrefix(cleaned, "/") {
			cleaned = "/" + cleaned
		}
		return "disk:" + cleaned
	}

	cleaned := path.Clean("/" + strings.TrimPrefix(trimmed, "/"))
	return "disk:" + cleaned
}

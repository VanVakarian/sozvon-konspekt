package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"sozvon-konspekt/internal/app"
	"sozvon-konspekt/internal/config"
	"sozvon-konspekt/internal/transcriber"
	"sozvon-konspekt/internal/yadisk"
	"sozvon-konspekt/internal/yandexoauth"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
		logger.Error("config error", "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tokenSource := yandexoauth.NewManager(yandexoauth.ManagerConfig{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RefreshToken: cfg.RefreshToken,
		RedirectURI:  cfg.RedirectURI,
		ListenAddr:   cfg.OAuthListenAddr,
		EnvFilePath:  cfg.EnvFilePath,
		Timeout:      cfg.HTTPTimeout,
		Logger:       logger,
	})

	diskClient, err := yadisk.NewClient(yadisk.ClientConfig{
		TokenSource: tokenSource,
		Timeout:     cfg.HTTPTimeout,
		Logger:      logger,
	})
	if err != nil {
		logger.Error("disk client init error", "error", err)
		os.Exit(1)
	}

	application := app.New(cfg, logger, diskClient, transcriber.NewStub())
	if err := application.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("service exited", "error", err)
		os.Exit(1)
	}
}

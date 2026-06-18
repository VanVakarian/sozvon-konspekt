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
	"sozvon-konspekt/internal/logger"
	"sozvon-konspekt/internal/transcriber"
	"sozvon-konspekt/internal/yadisk"
	"sozvon-konspekt/internal/yandexoauth"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fbLog := slog.New(logger.NewHandler(os.Stderr, nil))
		fbLog.Error("config error", "error", err)
		os.Exit(1)
	}

	logr, err := logger.Setup(cfg.LogDir, &logger.HandlerOptions{Level: cfg.LogLevel})
	if err != nil {
		fbLog := slog.New(logger.NewHandler(os.Stderr, nil))
		fbLog.Error("logger setup failed", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tokenSource := yandexoauth.NewManager(yandexoauth.ManagerConfig{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RefreshToken: cfg.RefreshToken,
		RedirectURI:  cfg.RedirectURI,
		EnvFilePath:  cfg.EnvFilePath,
		Timeout:      cfg.HTTPTimeout,
		Logger:       logr,
	})

	diskClient, err := yadisk.NewClient(yadisk.ClientConfig{
		TokenSource: tokenSource,
		Timeout:     cfg.HTTPTimeout,
		Logger:      logr,
	})
	if err != nil {
		logr.Error("disk client init failed", "error", err)
		os.Exit(1)
	}

	processor, err := transcriber.NewOpenRouter(transcriber.OpenRouterConfig{
		APIKey:         cfg.OpenRouterAPIKey,
		Model:          cfg.OpenRouterModel,
		PromptFilePath: cfg.TranscriptionPrompt,
		Timeout:        cfg.HTTPTimeout,
	})
	if err != nil {
		logr.Error("transcriber init failed", "error", err)
		os.Exit(1)
	}

	application := app.New(cfg, logr, diskClient, processor)
	if err := application.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logr.Error("service exited", "error", err)
		os.Exit(1)
	}
}

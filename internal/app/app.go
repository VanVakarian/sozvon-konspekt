package app

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"time"

	"sozvon-konspekt/internal/config"
	"sozvon-konspekt/internal/transcriber"
	"sozvon-konspekt/internal/worker"
	"sozvon-konspekt/internal/yadisk"
)

type App struct {
	cfg    config.Config
	logger *slog.Logger
	syncer *worker.Syncer
}

func New(cfg config.Config, logger *slog.Logger, disk *yadisk.Client, processor transcriber.Processor) *App {
	return &App{
		cfg:    cfg,
		logger: logger,
		syncer: worker.New(logger, disk, processor, cfg.Folder, cfg.PlaceholderStaleAfter),
	}
}

func (a *App) Run(ctx context.Context) error {
	a.logger.Info(
		"service started",
		"folder",
		a.cfg.Folder,
		"poll_interval",
		a.cfg.PollInterval,
		"placeholder_stale_after",
		a.cfg.PlaceholderStaleAfter,
	)

	a.runCycle(ctx)

	ticker := time.NewTicker(a.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.logger.Info("service stopped")
			return nil
		case <-ticker.C:
			a.runCycle(ctx)
		}
	}
}

func (a *App) runCycle(ctx context.Context) {
	startedAt := time.Now()
	a.logger.Info("poll started", "folder", a.cfg.Folder)

	defer func() {
		if recovered := recover(); recovered != nil {
			a.logger.Error("poll panic", "panic", recovered, "stack", string(debug.Stack()))
		}
	}()

	if err := a.syncer.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		a.logger.Error("poll failed", "error", err)
		return
	}

	a.logger.Info("poll finished", "folder", a.cfg.Folder, "duration", time.Since(startedAt))
}

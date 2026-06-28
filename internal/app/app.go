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
		syncer: worker.New(logger, disk, processor, cfg.Folder, cfg.ConvertAudio),
	}
}

func (a *App) Run(ctx context.Context) error {
	a.logger.Info(
		"service started",
		"folder",
		a.cfg.Folder,
		"poll interval",
		a.cfg.PollInterval,
	)

	for {
		delay := nextPollDelay(time.Now(), a.cfg.PollInterval)
		timer := time.NewTimer(delay)

		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			a.logger.Info("service stopped")
			return nil
		case <-timer.C:
			a.runCycle(ctx)
		}
	}
}

func nextPollDelay(now time.Time, interval time.Duration) time.Duration {
	next := now.Truncate(interval)
	if next.Before(now) {
		next = next.Add(interval)
	}

	if next.Equal(now) {
		return 0
	}

	return time.Until(next)
}

func (a *App) runCycle(ctx context.Context) {
	startedAt := time.Now()

	defer func() {
		if recovered := recover(); recovered != nil {
			a.logger.Error("poll panic", "panic", recovered, "stack", string(debug.Stack()))
		}
	}()

	summary, err := a.syncer.RunOnce(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		a.logger.Error("poll failed", "error", err, "elapsed", time.Since(startedAt))
		return
	}

	a.logger.Info(
		"poll completed",
		"resources",
		summary.Resources,
		"audio files",
		summary.AudioFiles,
		"text files",
		summary.TextFiles,
		"processed",
		summary.ProcessedFiles,
		"failed",
		summary.FailedFiles,
		"elapsed",
		time.Since(startedAt),
	)
}

package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"sozvon-konspekt/internal/transcriber"
	"sozvon-konspekt/internal/yadisk"
)

type Syncer struct {
	logger       *slog.Logger
	disk         *yadisk.Client
	processor    transcriber.Processor
	folder       string
	convertAudio bool
}

type RunSummary struct {
	Resources      int
	AudioFiles     int
	TextFiles      int
	ProcessedFiles int
	FailedFiles    int
}

type candidate struct {
	audio    yadisk.Resource
	textPath string
}

func New(logger *slog.Logger, disk *yadisk.Client, processor transcriber.Processor, folder string, convertAudio bool) *Syncer {
	return &Syncer{
		logger:       logger,
		disk:         disk,
		processor:    processor,
		folder:       folder,
		convertAudio: convertAudio,
	}
}

func (s *Syncer) RunOnce(ctx context.Context) (RunSummary, error) {
	resources, err := s.disk.ListFolder(ctx, s.folder)
	if err != nil {
		return RunSummary{}, fmt.Errorf("list folder: %w", err)
	}

	audioCount, textCount := countFilesByType(resources)
	summary := RunSummary{
		Resources:  len(resources),
		AudioFiles: audioCount,
		TextFiles:  textCount,
	}

	candidates := s.collectCandidates(resources)
	if len(candidates) == 0 {
		return summary, nil
	}

	for _, item := range candidates {
		if err := s.processCandidate(ctx, item); err != nil {
			if errors.Is(err, context.Canceled) {
				return summary, err
			}

			summary.FailedFiles++
			s.logger.Error("file failed", "file", item.audio.Name, "error", err)
			continue
		}

		summary.ProcessedFiles++
	}

	return summary, nil
}

func (s *Syncer) collectCandidates(resources []yadisk.Resource) []candidate {
	audioFiles := make([]yadisk.Resource, 0)
	textFiles := make(map[string]yadisk.Resource)

	for _, resource := range resources {
		if resource.Type != "file" {
			continue
		}

		extension := strings.ToLower(path.Ext(resource.Name))
		switch extension {
		case ".m4a":
			audioFiles = append(audioFiles, resource)
		case ".txt":
			key := stemKey(resource.Name)
			if existing, ok := textFiles[key]; ok {
				textFiles[key] = pickPreferredText(existing, resource)
				continue
			}
			textFiles[key] = resource
		}
	}

	sort.Slice(audioFiles, func(i int, j int) bool {
		if audioFiles[i].Modified.Equal(audioFiles[j].Modified) {
			return audioFiles[i].Name < audioFiles[j].Name
		}
		return audioFiles[i].Modified.Before(audioFiles[j].Modified)
	})

	candidates := make([]candidate, 0)
	for _, audio := range audioFiles {
		textPath := textPathFor(audio)
		textResource, ok := textFiles[stemKey(audio.Name)]
		if ok && textResource.Size > 0 {
			continue
		}

		candidates = append(candidates, candidate{
			audio:    audio,
			textPath: textPath,
		})
	}

	return candidates
}

func (s *Syncer) processCandidate(ctx context.Context, item candidate) error {
	startedAt := time.Now()

	tempFile, err := os.CreateTemp("", "transcription-input-*.m4a")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	defer os.Remove(tempPath)

	s.logger.Info("downloading", "file", item.audio.Name, "size", formatSize(item.audio.Size))

	downloadStartedAt := time.Now()
	if err := s.disk.DownloadToFile(ctx, item.audio.Path, tempPath); err != nil {
		return fmt.Errorf("download audio: %w", err)
	}
	downloadElapsed := time.Since(downloadStartedAt)

	s.logger.Info("downloaded",
		"file", item.audio.Name,
		"elapsed", downloadElapsed.Round(time.Millisecond),
	)

	audioPath := tempPath
	var convertCleanup func()

	if s.convertAudio {
		convertStartedAt := time.Now()
		convertedPath, cleanup, convertErr := convertAudioForInference(ctx, tempPath)
		convertElapsed := time.Since(convertStartedAt)

		if convertErr != nil {
			if strings.Contains(convertErr.Error(), "ffmpeg not found") {
				s.logger.Error("conversion unavailable: ffmpeg not found, sending original",
					"file", item.audio.Name,
				)
			} else {
				s.logger.Error("conversion failed, sending original",
					"file", item.audio.Name,
					"error", convertErr,
				)
			}
		} else {
			origInfo, _ := os.Stat(tempPath)
			convInfo, _ := os.Stat(convertedPath)
			origSize := int64(0)
			convSize := int64(0)
			if origInfo != nil {
				origSize = origInfo.Size()
			}
			if convInfo != nil {
				convSize = convInfo.Size()
			}
			s.logger.Info("converted audio",
				"file", item.audio.Name,
				"original size", formatSize(origSize),
				"compressed size", formatSize(convSize),
				"elapsed", convertElapsed.Round(time.Millisecond),
			)
			audioPath = convertedPath
			convertCleanup = cleanup
		}
	}

	defer func() {
		if convertCleanup != nil {
			convertCleanup()
		}
	}()

	transcribingLogArgs := []any{"file", item.audio.Name}
	audioDuration, err := parseM4ADuration(audioPath)
	if err != nil {
		s.logger.Warn("audio duration parse failed", "file", item.audio.Name, "error", err)
	}
	if audioDuration > 0 {
		transcribingLogArgs = append(transcribingLogArgs, "duration", formatDuration(audioDuration))
	}
	s.logger.Info("transcribing", transcribingLogArgs...)

	inferenceStartedAt := time.Now()
	result, err := s.processor.Process(ctx, transcriber.Input{
		LocalPath:  audioPath,
		RemotePath: item.audio.Path,
		Name:       item.audio.Name,
		Size:       item.audio.Size,
	})
	if err != nil {
		return fmt.Errorf("process audio: %w", err)
	}
	inferenceElapsed := time.Since(inferenceStartedAt)

	uploadStartedAt := time.Now()
	if err := s.disk.UploadBytes(ctx, item.textPath, []byte(result.Text), true); err != nil {
		return fmt.Errorf("upload text: %w", err)
	}
	uploadElapsed := time.Since(uploadStartedAt)

	completedLogArgs := []any{
		"text size", formatSize(int64(len(result.Text))),
		"download", downloadElapsed.Round(time.Millisecond),
		"inference", inferenceElapsed.Round(time.Millisecond),
		"upload", uploadElapsed.Round(time.Millisecond),
		"total", time.Since(startedAt).Round(time.Millisecond),
	}
	if result.Usage.Available {
		completedLogArgs = append(
			completedLogArgs,
			"input tokens", result.Usage.InputTokens,
			"output tokens", result.Usage.OutputTokens,
			"total tokens", result.Usage.TotalTokens,
			"cost", fmt.Sprintf("$%.2f", result.Usage.Cost),
			"cost ≈ RUB", fmt.Sprintf("%.2f", result.Usage.Cost*100),
		)
	}
	s.logger.Info("transcription completed", completedLogArgs...)
	return nil
}

func countFilesByType(resources []yadisk.Resource) (int, int) {
	audioCount := 0
	textCount := 0

	for _, resource := range resources {
		if resource.Type != "file" {
			continue
		}

		switch strings.ToLower(path.Ext(resource.Name)) {
		case ".m4a":
			audioCount++
		case ".txt":
			textCount++
		}
	}

	return audioCount, textCount
}

func textPathFor(audio yadisk.Resource) string {
	stem := strings.TrimSuffix(audio.Name, path.Ext(audio.Name))
	return path.Join(path.Dir(audio.Path), stem+".txt")
}

func stemKey(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))
}

func pickPreferredText(left yadisk.Resource, right yadisk.Resource) yadisk.Resource {
	if left.Size > 0 && right.Size == 0 {
		return left
	}

	if right.Size > 0 && left.Size == 0 {
		return right
	}

	if right.Modified.After(left.Modified) {
		return right
	}

	return left
}

func formatSize(bytes int64) string {
	const unit = 1024
	abs := bytes
	if abs < 0 {
		abs = -abs
	}
	if abs < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div := int64(unit)
	exp := 0
	for n := abs / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	suffix := string("KMGTPE"[exp])
	value := float64(bytes) / float64(div)
	rounded := math.Round(value*10) / 10
	return fmt.Sprintf("%.1f %sB", rounded, suffix)
}

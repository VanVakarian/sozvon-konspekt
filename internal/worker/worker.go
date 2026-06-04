package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"sozvon-konspekt/internal/transcriber"
	"sozvon-konspekt/internal/yadisk"
)

type Syncer struct {
	logger     *slog.Logger
	disk       *yadisk.Client
	processor  transcriber.Processor
	folder     string
	staleAfter time.Duration
}

type candidate struct {
	audio              yadisk.Resource
	textExists         bool
	refreshPlaceholder bool
	textPath           string
}

func New(logger *slog.Logger, disk *yadisk.Client, processor transcriber.Processor, folder string, staleAfter time.Duration) *Syncer {
	return &Syncer{
		logger:     logger,
		disk:       disk,
		processor:  processor,
		folder:     folder,
		staleAfter: staleAfter,
	}
}

func (s *Syncer) RunOnce(ctx context.Context) error {
	resources, err := s.disk.ListFolder(ctx, s.folder)
	if err != nil {
		return fmt.Errorf("list folder: %w", err)
	}

	candidates := s.collectCandidates(resources, time.Now())
	if len(candidates) == 0 {
		s.logger.Debug("nothing to process", "folder", s.folder)
		return nil
	}

	for _, item := range candidates {
		if err := s.processCandidate(ctx, item); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}

			s.logger.Error("processing failed", "audio_path", item.audio.Path, "text_path", item.textPath, "error", err)
		}
	}

	return nil
}

func (s *Syncer) collectCandidates(resources []yadisk.Resource, now time.Time) []candidate {
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
		if !ok {
			candidates = append(candidates, candidate{
				audio:    audio,
				textPath: textPath,
			})
			continue
		}

		if textResource.Size > 0 {
			continue
		}

		if now.Sub(textResource.Modified) < s.staleAfter {
			continue
		}

		candidates = append(candidates, candidate{
			audio:              audio,
			textExists:         true,
			refreshPlaceholder: true,
			textPath:           textPath,
		})
	}

	return candidates
}

func (s *Syncer) processCandidate(ctx context.Context, item candidate) error {
	if item.refreshPlaceholder {
		if err := s.disk.UploadBytes(ctx, item.textPath, nil, true); err != nil {
			return fmt.Errorf("refresh placeholder: %w", err)
		}

		s.logger.Info("placeholder refreshed", "audio_path", item.audio.Path, "text_path", item.textPath)
	} else {
		if err := s.disk.UploadBytes(ctx, item.textPath, nil, false); err != nil {
			if errors.Is(err, yadisk.ErrAlreadyExists) {
				s.logger.Warn("placeholder already exists", "audio_path", item.audio.Path, "text_path", item.textPath)
				return nil
			}

			return fmt.Errorf("create placeholder: %w", err)
		}

		s.logger.Info("placeholder created", "audio_path", item.audio.Path, "text_path", item.textPath)
	}

	tempFile, err := os.CreateTemp("", "sozvon-*.m4a")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	defer os.Remove(tempPath)

	if err := s.disk.DownloadToFile(ctx, item.audio.Path, tempPath); err != nil {
		return fmt.Errorf("download audio: %w", err)
	}

	text, err := s.processor.Process(ctx, transcriber.Input{
		LocalPath:  tempPath,
		RemotePath: item.audio.Path,
		Name:       item.audio.Name,
		Size:       item.audio.Size,
	})
	if err != nil {
		return fmt.Errorf("process audio: %w", err)
	}

	if err := s.disk.UploadBytes(ctx, item.textPath, []byte(text), true); err != nil {
		return fmt.Errorf("upload text: %w", err)
	}

	s.logger.Info("file processed", "audio_path", item.audio.Path, "text_path", item.textPath)
	return nil
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

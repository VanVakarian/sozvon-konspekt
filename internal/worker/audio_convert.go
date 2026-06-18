package worker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

func convertAudioForInference(ctx context.Context, inputPath string) (string, func(), error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return inputPath, nil, fmt.Errorf("ffmpeg not found: %w", err)
	}

	convertedFile, err := os.CreateTemp("", "sozvon-compressed-*.m4a")
	if err != nil {
		return inputPath, nil, fmt.Errorf("create temp file for compressed audio: %w", err)
	}
	convertedPath := convertedFile.Name()
	convertedFile.Close()

	cmd := exec.CommandContext(ctx,
		"ffmpeg",
		"-i", inputPath,
		"-vn",
		"-ac", "1",
		"-ar", "16000",
		"-c:a", "aac",
		"-b:a", "32k",
		"-y",
		convertedPath,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		os.Remove(convertedPath)
		stderrStr := stderr.String()
		if len(stderrStr) > 500 {
			stderrStr = stderrStr[len(stderrStr)-500:]
		}
		return inputPath, nil, fmt.Errorf("ffmpeg: %w\n%s", err, stderrStr)
	}

	return convertedPath, func() { os.Remove(convertedPath) }, nil
}

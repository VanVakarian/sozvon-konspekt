package transcriber

import (
	"context"
	"fmt"
	"os"
	"time"
)

type Input struct {
	LocalPath  string
	RemotePath string
	Name       string
	Size       int64
}

type Processor interface {
	Process(context.Context, Input) (string, error)
}

type Stub struct{}

func NewStub() Stub {
	return Stub{}
}

func (Stub) Process(ctx context.Context, input Input) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	info, err := os.Stat(input.LocalPath)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"Transcription is not configured yet.\n\nFile: %s\nRemote path: %s\nDownloaded size: %d bytes\nGenerated at: %s\n",
		input.Name,
		input.RemotePath,
		info.Size(),
		time.Now().UTC().Format(time.RFC3339),
	), nil
}

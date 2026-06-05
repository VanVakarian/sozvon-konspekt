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

type Result struct {
	Text  string
	Usage Usage
}

type Usage struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	Cost         float64
	Available    bool
}

type Processor interface {
	Process(context.Context, Input) (Result, error)
}

type Stub struct{}

func NewStub() Stub {
	return Stub{}
}

func (Stub) Process(ctx context.Context, input Input) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}

	info, err := os.Stat(input.LocalPath)
	if err != nil {
		return Result{}, err
	}

	return Result{
		Text: fmt.Sprintf(
			"Transcription is not configured yet.\n\nFile: %s\nRemote path: %s\nDownloaded size: %d bytes\nGenerated at: %s\n",
			input.Name,
			input.RemotePath,
			info.Size(),
			time.Now().UTC().Format(time.RFC3339),
		),
	}, nil
}

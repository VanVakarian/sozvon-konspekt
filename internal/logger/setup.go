package logger

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type lockedFile struct {
	mu sync.Mutex
	f  *os.File
}

func (w *lockedFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Write(p)
}

func Setup(logDir string, opts *HandlerOptions) (*slog.Logger, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, err
	}

	logPath := filepath.Join(logDir, "bot.log")

	stat, statErr := os.Stat(logPath)
	if statErr == nil && stat.Size() > 100*1024*1024 {
		ts := time.Now().Format("2006-01-02T150405")
		archivedPath := filepath.Join(logDir, "bot-"+ts+".log")
		_ = os.Rename(logPath, archivedPath)
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}

	lf := &lockedFile{f: f}
	writer := io.MultiWriter(os.Stdout, lf)

	return slog.New(NewHandler(writer, opts)), nil
}

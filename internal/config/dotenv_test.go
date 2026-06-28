package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPickEnvFilePrefersBareDotEnv(t *testing.T) {
	tempDir := t.TempDir()
	writeEnvFile(t, filepath.Join(tempDir, ".env"), "FOO=bare\n")
	writeEnvFile(t, filepath.Join(tempDir, ".env.95-182-83-180"), "FOO=server\n")
	chdirForTest(t, tempDir)

	got, err := pickEnvFile()
	if err != nil {
		t.Fatalf("pickEnvFile() error = %v", err)
	}
	if got != ".env" {
		t.Fatalf("pickEnvFile() = %q, want %q", got, ".env")
	}
}

func TestPickEnvFileFallsBackToSuffixedFileWhenBareMissing(t *testing.T) {
	tempDir := t.TempDir()
	writeEnvFile(t, filepath.Join(tempDir, ".env.95-182-83-180"), "FOO=server\n")
	chdirForTest(t, tempDir)

	got, err := pickEnvFile()
	if err != nil {
		t.Fatalf("pickEnvFile() error = %v", err)
	}
	if got != ".env.95-182-83-180" {
		t.Fatalf("pickEnvFile() = %q, want %q", got, ".env.95-182-83-180")
	}
}

func TestPickEnvFileUsesFirstSuffixedMatchAlphabetically(t *testing.T) {
	tempDir := t.TempDir()
	writeEnvFile(t, filepath.Join(tempDir, ".env.95-182-83-180"), "FOO=fr\n")
	writeEnvFile(t, filepath.Join(tempDir, ".env.local"), "FOO=local\n")
	chdirForTest(t, tempDir)

	got, err := pickEnvFile()
	if err != nil {
		t.Fatalf("pickEnvFile() error = %v", err)
	}
	if got != ".env.95-182-83-180" {
		t.Fatalf("pickEnvFile() = %q, want %q (lexicographic first)", got, ".env.95-182-83-180")
	}
}

func TestPickEnvFileSkipsExampleFile(t *testing.T) {
	tempDir := t.TempDir()
	writeEnvFile(t, filepath.Join(tempDir, ".env.example"), "FOO=example\n")
	writeEnvFile(t, filepath.Join(tempDir, ".env.local"), "FOO=real\n")
	chdirForTest(t, tempDir)

	got, err := pickEnvFile()
	if err != nil {
		t.Fatalf("pickEnvFile() error = %v", err)
	}
	if got != ".env.local" {
		t.Fatalf("pickEnvFile() = %q, want %q (example file must never win)", got, ".env.local")
	}
}

func TestPickEnvFileErrorsWhenOnlyExampleFileExists(t *testing.T) {
	tempDir := t.TempDir()
	writeEnvFile(t, filepath.Join(tempDir, ".env.example"), "FOO=example\n")
	chdirForTest(t, tempDir)

	if _, err := pickEnvFile(); err == nil {
		t.Fatal("pickEnvFile() error = nil, want error (example file alone must not count as a real candidate)")
	}
}

func TestPickEnvFileErrorsWhenNoCandidateExists(t *testing.T) {
	tempDir := t.TempDir()
	chdirForTest(t, tempDir)

	if _, err := pickEnvFile(); err == nil {
		t.Fatal("pickEnvFile() error = nil, want error (no .env or .env.<id> present)")
	}
}

func writeEnvFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd error = %v", err)
		}
	})
}

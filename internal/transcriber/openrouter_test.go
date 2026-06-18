package transcriber

import (
	"testing"
)

func TestNewOpenRouter_MissingAPIKey(t *testing.T) {
	_, err := NewOpenRouter(OpenRouterConfig{
		Model:          "google/gemini-2.5-pro",
		PromptFilePath: "testdata/prompt.txt",
	})
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestNewOpenRouter_MissingModel(t *testing.T) {
	_, err := NewOpenRouter(OpenRouterConfig{
		APIKey:         "test-key",
		PromptFilePath: "testdata/prompt.txt",
	})
	if err == nil {
		t.Fatal("expected error for missing model")
	}
}

func TestNewOpenRouter_MissingPromptFile(t *testing.T) {
	_, err := NewOpenRouter(OpenRouterConfig{
		APIKey:         "test-key",
		Model:          "google/gemini-2.5-pro",
		PromptFilePath: "nonexistent.prompt.txt",
	})
	if err == nil {
		t.Fatal("expected error for missing prompt file")
	}
}

func TestDetectAudioFormat(t *testing.T) {
	tests := []struct {
		localPath string
		name      string
		want      string
	}{
		{"/tmp/audio.m4a", "recording.m4a", "m4a"},
		{"/tmp/audio.MP3", "podcast.MP3", "mp3"},
		{"/tmp/audio.wav", "voice.wav", "wav"},
		{"/tmp/audio.mp3", "voice.mp3", "mp3"},
		{"/tmp/audio.aac", "voice.aac", "aac"},
		{"/tmp/audio.ogg", "voice.ogg", "ogg"},
		{"/tmp/audio.flac", "voice.flac", "flac"},
		{"/tmp/audio.pcm16", "voice.pcm16", "pcm16"},
		{"/tmp/audio.webm", "voice.webm", "webm"},
		{"", "recording.m4a", "m4a"},
		{"/tmp/audio.xyz", "recording.xyz", ""},
		{"/tmp/audio", "recording", ""},
	}

	for _, tt := range tests {
		format, err := detectAudioFormat(tt.localPath, tt.name)
		if tt.want == "" {
			if err == nil {
				t.Errorf("detectAudioFormat(%q, %q) expected error, got format=%q", tt.localPath, tt.name, format)
			}
		} else {
			if err != nil {
				t.Errorf("detectAudioFormat(%q, %q) unexpected error: %v", tt.localPath, tt.name, err)
			}
			if format != tt.want {
				t.Errorf("detectAudioFormat(%q, %q) = %q, want %q", tt.localPath, tt.name, format, tt.want)
			}
		}
	}
}


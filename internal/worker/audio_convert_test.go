package worker

import (
	"encoding/binary"
	"os"
	"os/exec"
	"testing"
)

func TestConvertAudioForInference_FFmpegNotInstalled(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		t.Skip("ffmpeg is installed, skipping fallback test")
	}

	_, _, err := convertAudioForInference(t.Context(), "nonexistent.m4a")
	if err == nil {
		t.Fatal("expected error when ffmpeg is not installed")
	}
}

func TestConvertAudioForInference_WithFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	wavPath := createTestWAV(t)

	convertedPath, cleanup, err := convertAudioForInference(t.Context(), wavPath)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	defer cleanup()

	if convertedPath == wavPath {
		t.Fatal("expected a converted path, got original path")
	}

	info, err := os.Stat(convertedPath)
	if err != nil {
		t.Fatalf("stat converted file: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("converted file is empty")
	}
	if info.Size() >= 100000 {
		t.Errorf("converted file too large: %d bytes, expected compressed output", info.Size())
	}
}

func createTestWAV(t *testing.T) string {
	t.Helper()

	f, err := os.CreateTemp("", "test-audio-*.wav")
	if err != nil {
		t.Fatal(err)
	}

	sampleRate := uint32(44100)
	numChannels := uint16(1)
	bitsPerSample := uint16(16)
	numSamples := sampleRate * 2
	dataSize := uint32(numSamples) * uint32(numChannels) * uint32(bitsPerSample/8)

	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], 36+dataSize)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], numChannels)
	binary.LittleEndian.PutUint32(header[24:28], sampleRate)
	binary.LittleEndian.PutUint32(header[28:32], sampleRate*uint32(numChannels)*uint32(bitsPerSample/8))
	binary.LittleEndian.PutUint16(header[32:34], numChannels*bitsPerSample/8)
	binary.LittleEndian.PutUint16(header[34:36], bitsPerSample)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataSize)

	f.Write(header)
	f.Write(make([]byte, dataSize))
	f.Close()

	t.Cleanup(func() { os.Remove(f.Name()) })
	return f.Name()
}

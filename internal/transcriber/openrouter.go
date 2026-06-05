package transcriber

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const openRouterBaseURL = "https://openrouter.ai/api/v1"

type OpenRouterConfig struct {
	APIKey         string
	Model          string
	PromptFilePath string
	Timeout        time.Duration
}

type OpenRouter struct {
	client *openai.Client
	model  string
	prompt string
}

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string            `json:"role"`
	Content []chatContentPart `json:"content"`
}

type chatContentPart struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	InputAudio *inputAudioPart `json:"input_audio,omitempty"`
}

type inputAudioPart struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

type chatCompletionResponse struct {
	Choices []chatChoice `json:"choices"`
	Usage   *chatUsage   `json:"usage,omitempty"`
}

type chatUsage struct {
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	Cost             float64 `json:"cost"`
}

type chatChoice struct {
	Message chatResponseMessage `json:"message"`
}

type chatResponseMessage struct {
	Content string `json:"content"`
}

func NewOpenRouter(cfg OpenRouterConfig) (*OpenRouter, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("openrouter api key is required")
	}

	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("openrouter model is required")
	}

	if strings.TrimSpace(cfg.PromptFilePath) == "" {
		return nil, errors.New("prompt file path is required")
	}

	promptBytes, err := os.ReadFile(cfg.PromptFilePath)
	if err != nil {
		return nil, fmt.Errorf("read prompt file: %w", err)
	}

	prompt := strings.TrimSpace(string(promptBytes))
	if prompt == "" {
		return nil, errors.New("prompt file is empty")
	}

	httpClient := &http.Client{Timeout: cfg.Timeout}
	client := openai.NewClient(
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(openRouterBaseURL),
		option.WithHTTPClient(httpClient),
		option.WithHeader("X-Title", "sozvon-konspekt"),
	)

	return &OpenRouter{
		client: &client,
		model:  cfg.Model,
		prompt: prompt,
	}, nil
}

func (o *OpenRouter) Process(ctx context.Context, input Input) (Result, error) {
	audioBytes, err := os.ReadFile(input.LocalPath)
	if err != nil {
		return Result{}, fmt.Errorf("read audio file: %w", err)
	}

	format, err := detectAudioFormat(input.LocalPath, input.Name)
	if err != nil {
		return Result{}, err
	}

	request := chatCompletionRequest{
		Model: o.model,
		Messages: []chatMessage{
			{
				Role: "user",
				Content: []chatContentPart{
					{
						Type: "text",
						Text: o.prompt,
					},
					{
						Type: "input_audio",
						InputAudio: &inputAudioPart{
							Data:   base64.StdEncoding.EncodeToString(audioBytes),
							Format: format,
						},
					},
				},
			},
		},
	}

	var response chatCompletionResponse
	if err := o.client.Post(ctx, "chat/completions", request, &response); err != nil {
		return Result{}, fmt.Errorf("openrouter request failed: %w", err)
	}

	if len(response.Choices) == 0 {
		return Result{}, errors.New("openrouter returned no choices")
	}

	text := strings.TrimSpace(response.Choices[0].Message.Content)
	if text == "" {
		return Result{}, errors.New("openrouter returned empty transcription")
	}

	result := Result{Text: text}
	if response.Usage != nil {
		result.Usage = Usage{
			InputTokens:  response.Usage.PromptTokens,
			OutputTokens: response.Usage.CompletionTokens,
			TotalTokens:  response.Usage.TotalTokens,
			Cost:         response.Usage.Cost,
			Available:    true,
		}
	}

	return result, nil

}

func detectAudioFormat(localPath string, name string) (string, error) {
	for _, candidate := range []string{localPath, name} {
		extension := strings.ToLower(strings.TrimPrefix(filepath.Ext(candidate), "."))
		switch extension {
		case "wav", "mp3", "aiff", "aac", "ogg", "flac", "m4a", "pcm16", "pcm24", "webm":
			return extension, nil
		}
	}

	return "", fmt.Errorf("unsupported audio format for %q", name)
}

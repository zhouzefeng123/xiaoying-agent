package provider

import (
	"context"
	"time"

	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/message"
)

// Exported variables for testing purposes
var (
	MockResponse       func(ctx context.Context, messages []message.Message, tools []tools.BaseTool) (*ProviderResponse, error)
	MockStreamResponse func(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent
	MockTranscribe     func(ctx context.Context, audio []byte, filename string) (string, error)
	MockGenerateSpeech func(ctx context.Context, text string, voice string) ([]byte, error)
)

type MockClient struct {
	options providerClientOptions
}

func newMockClient(options providerClientOptions) *MockClient {
	return &MockClient{
		options: options,
	}
}

func (c *MockClient) send(ctx context.Context, messages []message.Message, tools []tools.BaseTool) (*ProviderResponse, error) {
	if MockResponse != nil {
		return MockResponse(ctx, messages, tools)
	}

	return &ProviderResponse{
		Content: "1, 2, 3, 4, 5",
		Usage: TokenUsage{
			InputTokens:  10,
			OutputTokens: 5,
		},
		FinishReason: message.FinishReasonEndTurn,
	}, nil
}

func (c *MockClient) stream(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
	if MockStreamResponse != nil {
		return MockStreamResponse(ctx, messages, tools)
	}

	ch := make(chan ProviderEvent)
	go func() {
		defer close(ch)

		// Simulate thinking/processing time
		time.Sleep(100 * time.Millisecond)

		tokens := []string{"1", ", ", "2", ", ", "3", ", ", "4", ", ", "5"}

		ch <- ProviderEvent{
			Type:    EventContentStart,
			Content: "",
		}

		for _, token := range tokens {
			select {
			case <-ctx.Done():
				return
			case ch <- ProviderEvent{
				Type:    EventContentDelta,
				Content: token,
			}:
				time.Sleep(200 * time.Millisecond) // Slow down to see the stream
			}
		}

		ch <- ProviderEvent{
			Type: EventContentStop,
		}

		ch <- ProviderEvent{
			Type: EventComplete,
			Response: &ProviderResponse{
				Content: "1, 2, 3, 4, 5",
				Usage: TokenUsage{
					InputTokens:  10,
					OutputTokens: 9,
				},
				FinishReason: message.FinishReasonEndTurn,
			},
		}
	}()
	return ch
}

func (c *MockClient) transcribe(ctx context.Context, audio []byte, filename string) (string, error) {
	if MockTranscribe != nil {
		return MockTranscribe(ctx, audio, filename)
	}
	return "Mock transcription: Hello world", nil
}

func (c *MockClient) generateSpeech(ctx context.Context, text string, voice string) ([]byte, error) {
	if MockGenerateSpeech != nil {
		return MockGenerateSpeech(ctx, text, voice)
	}
	return []byte("Mock audio data"), nil
}

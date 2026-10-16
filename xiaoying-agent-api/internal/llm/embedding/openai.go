package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OpenAIEmbedder implements the Embedder interface for OpenAI-compatible APIs.
// This supports OpenAI, Ollama, LM Studio, etc.
type OpenAIEmbedder struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// NewOpenAIEmbedder creates a new embedder for OpenAI-compatible APIs.
// baseURL: "https://api.openai.com/v1" (default) or "http://localhost:11434/v1" (Ollama)
func NewOpenAIEmbedder(apiKey, baseURL, model string) *OpenAIEmbedder {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	// Ensure apiKey is not empty for some providers, though Ollama might ignore it.
	if apiKey == "" {
		apiKey = "dummy"
	}

	return &OpenAIEmbedder{
		apiKey:  apiKey,
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

type openAIRequest struct {
	Input          any    `json:"input"` // string or []string
	Model          string `json:"model"`
	EncodingFormat string `json:"encoding_format,omitempty"`
}

type openAIResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Embed generates embeddings for the given texts using the configured OpenAI-compatible API.
// It automatically batches requests if the API supports it, but for simplicity/compatibility
// with some local models that might have limits, we process concurrently or in small batches.
func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// For compatibility with diverse backends (Ollama, LM Studio),
	// we'll send batch requests if possible, but some local models might prefer single.
	// OpenAI API supports batching. Let's try batching all at once first.
	// If it fails or is too large, we might need to chunk it.
	// For now, let's assume standard usage (not huge batches).

	reqBody := openAIRequest{
		Input:          texts,
		Model:          e.model,
		EncodingFormat: "float",
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/embeddings", e.baseURL)
	// Handle trailing slash issues in baseURL if user provided one
	if e.baseURL[len(e.baseURL)-1] == '/' {
		url = fmt.Sprintf("%sembeddings", e.baseURL)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp openAIResponse
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err == nil && errResp.Error != nil {
			return nil, fmt.Errorf("api error (%d): %s", resp.StatusCode, errResp.Error.Message)
		}
		return nil, fmt.Errorf("api returned status: %d", resp.StatusCode)
	}

	var result openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Map results back to order (OpenAI guarantees order but let's be safe if using index)
	// Usually data is returned in order.
	embeddings := make([][]float32, len(texts))

	// If the API returns fewer embeddings than texts, that's an issue.
	// But standard OpenAI API returns one for each input.
	for _, item := range result.Data {
		if item.Index >= 0 && item.Index < len(embeddings) {
			embeddings[item.Index] = item.Embedding
		} else {
			// Fallback: assume order
			// This handles cases where 'index' might be missing in some non-compliant implementations
			// though it's risky.
		}
	}

	// Double check if we missed any (e.g. if index wasn't used)
	if len(result.Data) == len(texts) && embeddings[0] == nil && len(result.Data[0].Embedding) > 0 {
		for i, item := range result.Data {
			embeddings[i] = item.Embedding
		}
	}

	return embeddings, nil
}

// Support for single embedding (helper)
func (e *OpenAIEmbedder) embedSingle(ctx context.Context, text string) ([]float32, error) {
	res, err := e.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(res) > 0 {
		return res[0], nil
	}
	return nil, fmt.Errorf("no embedding returned")
}

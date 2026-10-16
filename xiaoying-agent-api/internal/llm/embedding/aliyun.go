package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

type AliyunEmbedder struct {
	apiKey string
	client *http.Client
}

func NewAliyunEmbedder(apiKey string) *AliyunEmbedder {
	return &AliyunEmbedder{
		apiKey: apiKey,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

type multiModalContent struct {
	Text  string `json:"text,omitempty"`
	Image string `json:"image,omitempty"`
}

type multiModalInput struct {
	Contents []multiModalContent `json:"contents"`
}

type aliyunMultiModalRequest struct {
	Model string          `json:"model"`
	Input multiModalInput `json:"input"`
}

type aliyunMultiModalResponse struct {
	Output struct {
		Embeddings []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"embeddings"`
	} `json:"output"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// Embed generates embeddings for the given texts using tongyi-embedding-vision-flash.
// Since the multimodal API handles one input per request, this function processes texts concurrently.
// It supports "Text", "Image URL", and "Text + Markdown Image" hybrid inputs.
func (e *AliyunEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	embeddings := make([][]float32, len(texts))
	var wg sync.WaitGroup
	var errMutex sync.Mutex
	var firstErr error

	// Create a semaphore to limit concurrency (e.g., 5 concurrent requests)
	sem := make(chan struct{}, 5)

	for i, text := range texts {
		wg.Add(1)
		go func(idx int, txt string) {
			defer wg.Done()

			// Acquire semaphore
			sem <- struct{}{}
			defer func() { <-sem }()

			if ctx.Err() != nil {
				return
			}

			emb, err := e.embedSingle(ctx, txt)
			if err != nil {
				errMutex.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMutex.Unlock()
				return
			}
			embeddings[idx] = emb
		}(i, text)
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	return embeddings, nil
}

func (e *AliyunEmbedder) embedSingle(ctx context.Context, input string) ([]float32, error) {
	contents := parseContent(input)
	if len(contents) == 0 {
		return nil, nil // Or return zero vector?
	}

	reqBody := aliyunMultiModalRequest{
		Model: "tongyi-embedding-vision-flash",
		Input: multiModalInput{
			Contents: contents,
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://dashscope.aliyuncs.com/api/v1/services/embeddings/multimodal-embedding/multimodal-embedding", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp aliyunMultiModalResponse
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return nil, fmt.Errorf("aliyun embedding api error: status code %d, code: %s, message: %s", resp.StatusCode, errResp.Code, errResp.Message)
	}

	var result aliyunMultiModalResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if result.Code != "" {
		return nil, fmt.Errorf("aliyun embedding api error: %s - %s", result.Code, result.Message)
	}

	if len(result.Output.Embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}

	return result.Output.Embeddings[0].Embedding, nil
}

// Regex to find markdown images: ![alt](url)
var markdownImageRegex = regexp.MustCompile(`!\[.*?\]\((.*?)\)`)

// Regex to check if a string is purely an image URL (simple check)
var rawImageUrlRegex = regexp.MustCompile(`^https?://.*\.(?:jpg|jpeg|png|bmp|webp|gif)(\?.*)?$`)

func parseContent(input string) []multiModalContent {
	input = strings.TrimSpace(input)
	var contents []multiModalContent

	// 1. Check if the entire input is a raw image URL
	if rawImageUrlRegex.MatchString(input) {
		return []multiModalContent{{Image: input}}
	}

	// 2. Check for markdown images
	matches := markdownImageRegex.FindAllStringSubmatchIndex(input, -1)
	if len(matches) == 0 {
		// No images, treat as pure text
		if input != "" {
			return []multiModalContent{{Text: input}}
		}
		return nil
	}

	// 3. Interleave Text and Image
	lastIndex := 0
	for _, match := range matches {
		// match[0], match[1] is the range of the whole match ![...](...)
		// match[2], match[3] is the range of the URL group
		start := match[0]
		end := match[1]
		urlStart := match[2]
		urlEnd := match[3]

		// Text before the image
		if start > lastIndex {
			textPart := strings.TrimSpace(input[lastIndex:start])
			if textPart != "" {
				contents = append(contents, multiModalContent{Text: textPart})
			}
		}

		// The Image
		imageUrl := input[urlStart:urlEnd]
		contents = append(contents, multiModalContent{Image: imageUrl})

		lastIndex = end
	}

	// Remaining text after the last image
	if lastIndex < len(input) {
		textPart := strings.TrimSpace(input[lastIndex:])
		if textPart != "" {
			contents = append(contents, multiModalContent{Text: textPart})
		}
	}

	return contents
}

package tools

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auramate/auramate/internal/message"
)

type blockingVisualAnalyzer struct{}

func (blockingVisualAnalyzer) Analyze(ctx context.Context, prompt string, imageContent message.BinaryContent) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func TestVisualAnalysisToolTimesOut(t *testing.T) {
	oldTimeout := visualAnalysisTimeout
	visualAnalysisTimeout = 10 * time.Millisecond
	defer func() { visualAnalysisTimeout = oldTimeout }()

	imagePath := filepath.Join(t.TempDir(), "image.png")
	file, err := os.Create(imagePath)
	if err != nil {
		t.Fatalf("create image: %v", err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		file.Close()
		t.Fatalf("encode image: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close image: %v", err)
	}

	tool, err := NewVisualAnalysisTool(blockingVisualAnalyzer{})
	if err != nil {
		t.Fatalf("NewVisualAnalysisTool: %v", err)
	}
	resp, err := tool.Run(context.Background(), ToolCall{Input: `{"image_path":"` + strings.ReplaceAll(imagePath, `\`, `\\`) + `","prompt":"describe"}`})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !resp.IsError || !strings.Contains(resp.Content, "视觉模型调用超时") {
		t.Fatalf("expected timeout error response, got %#v", resp)
	}
}

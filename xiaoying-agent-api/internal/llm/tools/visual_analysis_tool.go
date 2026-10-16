package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"mime"
	"os"
	"path/filepath"
	"time"

	"github.com/auramate/auramate/internal/message"
	"golang.org/x/image/draw"
)

const (
	VisualAnalysisToolName = "visual-analysis"
	maxImageWidth          = 1920
	maxImageHeight         = 1080
	jpegQuality            = 85
)

var visualAnalysisTimeout = 60 * time.Second

type visualAnalysisTool struct {
	analyzer VisualAnalyzer
}

type VisualAnalyzer interface {
	Analyze(ctx context.Context, prompt string, imageContent message.BinaryContent) (string, error)
}

type VisualAnalysisParams struct {
	ImagePath string `json:"image_path"`
	Prompt    string `json:"prompt"`
}

func (t *visualAnalysisTool) Info() ToolInfo {
	return ToolInfo{
		Name:        VisualAnalysisToolName,
		Description: "强大的视觉分析工具。使用视觉模型分析图片或截图。当标准 DOM 解析失效、需要解析复杂网页内容、识别验证码或 Canvas、或用户询问图片内容时使用。",
		Parameters: map[string]any{
			"image_path": map[string]any{
				"type":        "string",
				"description": "本地图片文件的绝对路径。",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "关于图片的提示词或问题。",
			},
		},
		Required: []string{"image_path", "prompt"},
	}
}

func NewVisualAnalysisTool(analyzer VisualAnalyzer) (BaseTool, error) {
	if analyzer == nil {
		return nil, fmt.Errorf("visual analyzer is required")
	}

	return &visualAnalysisTool{
		analyzer: analyzer,
	}, nil
}

func (t *visualAnalysisTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params VisualAnalysisParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("参数解析错误: %s", err)), nil
	}

	if params.ImagePath == "" {
		return NewTextErrorResponse("image_path 是必须的"), nil
	}
	if params.Prompt == "" {
		return NewTextErrorResponse("prompt 是必须的"), nil
	}

	// Encode image
	imageContent, err := loadImageContent(params.ImagePath)
	if err != nil {
		return NewTextErrorResponse(fmt.Sprintf("图片处理失败: %s", err)), nil
	}

	ctx = SetRequestKind(ctx, "visual_analysis")
	analysisCtx, cancel := context.WithTimeout(ctx, visualAnalysisTimeout)
	defer cancel()

	content, err := t.analyzer.Analyze(analysisCtx, params.Prompt, imageContent)
	if analysisCtx.Err() == context.DeadlineExceeded {
		return NewTextErrorResponse("视觉模型调用超时，请稍后重试或直接使用已上传的图片信息。"), nil
	}
	if err != nil {
		return NewTextErrorResponse(fmt.Sprintf("视觉模型调用失败: %s", err)), nil
	}

	return NewTextResponse(content), nil
}

func loadImageContent(path string) (message.BinaryContent, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return message.BinaryContent{}, fmt.Errorf("file not found: %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return message.BinaryContent{}, err
	}

	mimeType := mime.TypeByExtension(filepath.Ext(path))
	if mimeType == "" {
		mimeType = "image/png"
	}

	compressed, newMIME, err := compressImageIfNeeded(data, mimeType)
	if err != nil {
		return message.BinaryContent{Path: path, MIMEType: mimeType, Data: data}, nil
	}

	return message.BinaryContent{Path: path, MIMEType: newMIME, Data: compressed}, nil
}

func compressImageIfNeeded(data []byte, mimeType string) ([]byte, string, error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("decode failed: %w", err)
	}

	bounds := img.Bounds()
	origW := bounds.Dx()
	origH := bounds.Dy()

	if origW <= maxImageWidth && origH <= maxImageHeight {
		return data, mimeType, nil
	}

	scaleX := float64(maxImageWidth) / float64(origW)
	scaleY := float64(maxImageHeight) / float64(origH)
	scale := scaleX
	if scaleY < scaleX {
		scale = scaleY
	}

	newW := int(float64(origW) * scale)
	newH := int(float64(origH) * scale)
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}

	resized := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.CatmullRom.Scale(resized, resized.Bounds(), img, bounds, draw.Over, nil)

	var buf bytes.Buffer
	switch format {
	case "png":
		if err := png.Encode(&buf, resized); err != nil {
			return nil, "", fmt.Errorf("png encode failed: %w", err)
		}
		return buf.Bytes(), "image/png", nil
	default:
		if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return nil, "", fmt.Errorf("jpeg encode failed: %w", err)
		}
		return buf.Bytes(), "image/jpeg", nil
	}
}

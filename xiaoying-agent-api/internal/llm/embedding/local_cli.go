package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	localOpenVINOEmbedDefaultDevice    = "CPU"
	localOpenVINOEmbedDefaultMaxLength = 512
	localOpenVINOEmbedDefaultTimeout   = 60 * time.Second
)

type LocalCLIEmbedder struct {
	binaryPath string
	modelDir   string
	device     string
	maxLength  int
}

type localCLIEmbedRequest struct {
	Texts     []string `json:"texts"`
	MaxLength int      `json:"maxLength,omitempty"`
}

type localCLIEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Dimension  int         `json:"dimension"`
	Error      string      `json:"error,omitempty"`
}

func NewLocalCLIEmbedder(modelName string, modelDirOverride string) (*LocalCLIEmbedder, error) {
	binaryPath, err := resolveOpenVINOEmbedBinaryPath()
	if err != nil {
		return nil, err
	}

	modelDir, err := resolveOpenVINOEmbedModelDir(modelName, modelDirOverride)
	if err != nil {
		return nil, err
	}

	device := strings.TrimSpace(os.Getenv("AURAMATE_OPENVINO_EMBED_DEVICE"))
	if device == "" {
		device = localOpenVINOEmbedDefaultDevice
	}

	maxLength := localOpenVINOEmbedDefaultMaxLength
	if rawMaxLength := strings.TrimSpace(os.Getenv("AURAMATE_OPENVINO_EMBED_MAX_LENGTH")); rawMaxLength != "" {
		parsed, parseErr := strconv.Atoi(rawMaxLength)
		if parseErr == nil && parsed > 0 {
			maxLength = parsed
		}
	}

	return &LocalCLIEmbedder{
		binaryPath: binaryPath,
		modelDir:   modelDir,
		device:     device,
		maxLength:  maxLength,
	}, nil
}

func (e *LocalCLIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	requestBody := localCLIEmbedRequest{Texts: texts}
	if e.maxLength > 0 {
		requestBody.MaxLength = e.maxLength
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal OpenVINO embedding request: %w", err)
	}

	execCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		execCtx, cancel = context.WithTimeout(ctx, localOpenVINOEmbedDefaultTimeout)
	}
	defer cancel()

	cmd := exec.CommandContext(execCtx, e.binaryPath, "--model-dir", e.modelDir, "--device", e.device)
	cmd.Stdin = bytes.NewReader(payload)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrText := strings.TrimSpace(stderr.String())
		if stderrText == "" {
			stderrText = strings.TrimSpace(stdout.String())
		}
		if stderrText != "" {
			return nil, fmt.Errorf("OpenVINO embedding helper failed: %w, output: %s", err, stderrText)
		}
		return nil, fmt.Errorf("OpenVINO embedding helper failed: %w", err)
	}

	var response localCLIEmbedResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("failed to decode OpenVINO embedding response: %w", err)
	}

	if response.Error != "" {
		return nil, fmt.Errorf("OpenVINO embedding helper error: %s", response.Error)
	}

	if len(response.Embeddings) != len(texts) {
		return nil, fmt.Errorf("OpenVINO embedding helper returned %d embeddings for %d texts", len(response.Embeddings), len(texts))
	}

	if response.Dimension > 0 {
		for index, vector := range response.Embeddings {
			if len(vector) != response.Dimension {
				return nil, fmt.Errorf("OpenVINO embedding helper returned inconsistent dimension at index %d: want %d, got %d", index, response.Dimension, len(vector))
			}
		}
	}

	return response.Embeddings, nil
}

func (e *LocalCLIEmbedder) BinaryPath() string {
	return e.binaryPath
}

func (e *LocalCLIEmbedder) ModelDir() string {
	return e.modelDir
}

func resolveOpenVINOEmbedBinaryPath() (string, error) {
	binaryNames := []string{"ov-embed"}
	if runtime.GOOS == "windows" {
		binaryNames = []string{"ov-embed.exe", "auramate-openvino-embed.exe"}
	}

	searchPatterns := []string{
		filepath.Join("openvino_embed", "bin", "%s"),
		filepath.Join("openvino_embed", "%s"),
		filepath.Join("release_bundle", "openvino_embed", "bin", "%s"),
		filepath.Join("release_bundle", "openvino_embed", "%s"),
		filepath.Join("backend", "tools", "openvino_embed", "dist", "%s"),
		filepath.Join("backend", "tools", "openvino_embed", "build", "Release", "%s"),
		filepath.Join("backend", "tools", "openvino_embed", "build", "%s"),
		"%s",
	}

	return resolveLocalFileResource(
		[]string{"AURAMATE_OPENVINO_EMBED_BINARY_PATH"},
		binaryNames,
		searchPatterns,
		"OpenVINO 向量可执行文件未找到，请确认 openvino_embed 目录中包含 ov-embed 或通过 AURAMATE_OPENVINO_EMBED_BINARY_PATH 指向兼容运行时",
	)
}

func resolveOpenVINOEmbedModelDir(modelName string, modelDirOverride string) (string, error) {
	if modelDirOverride != "" {
		if info, err := os.Stat(modelDirOverride); err == nil && info.IsDir() {
			return filepath.Clean(modelDirOverride), nil
		}
		return "", fmt.Errorf("configured OpenVINO model directory does not exist: %s", modelDirOverride)
	}

	candidates := make([]string, 0, 10)
	appendCandidate := func(path string) {
		if path == "" {
			return
		}
		candidates = append(candidates, path)
	}

	if modelName != "" {
		normalizedName := strings.NewReplacer("/", "_", "\\", "_").Replace(modelName)
		appendCandidate(filepath.Join("openvino_embed", "models", modelName))
		appendCandidate(filepath.Join("release_bundle", "openvino_embed", "models", modelName))
		appendCandidate(filepath.Join("backend", "tools", "openvino_embed", "models", modelName))
		if normalizedName != modelName {
			appendCandidate(filepath.Join("openvino_embed", "models", normalizedName))
			appendCandidate(filepath.Join("release_bundle", "openvino_embed", "models", normalizedName))
			appendCandidate(filepath.Join("backend", "tools", "openvino_embed", "models", normalizedName))
		}
	}

	appendCandidate(filepath.Join("openvino_embed", "model"))
	appendCandidate(filepath.Join("release_bundle", "openvino_embed", "model"))
	appendCandidate(filepath.Join("backend", "tools", "openvino_embed", "model"))

	return resolveLocalDirResource(
		[]string{"AURAMATE_OPENVINO_EMBED_MODEL_DIR"},
		candidates,
		"OpenVINO 向量模型目录未找到，请确认 openvino_embed/model 或 openvino_embed/models/<model> 已准备完成",
	)
}

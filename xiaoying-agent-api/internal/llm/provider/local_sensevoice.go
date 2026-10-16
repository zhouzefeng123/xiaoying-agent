package provider

import (
	"path/filepath"
	"runtime"
)

const (
	localSenseVoiceRuntimeName     = "sherpa-onnx-offline"
	localSenseVoiceRuntimeVersion  = "v1.12.36"
	localSenseVoiceModelName       = "SenseVoice Small"
	localSenseVoiceModelRelease    = "2024-07-17-int8"
	localSenseVoiceModelDirName    = "sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2024-07-17"
	localSenseVoiceModelFileName   = "model.int8.onnx"
	localSenseVoiceTokensFileName  = "tokens.txt"
	localSenseVoiceDefaultLanguage = "zh"
	localSenseVoiceUseITN          = true
)

type LocalSenseVoiceStatus struct {
	Ready          bool   `json:"ready"`
	BinaryFound    bool   `json:"binaryFound"`
	ModelFound     bool   `json:"modelFound"`
	TokensFound    bool   `json:"tokensFound"`
	BinaryPath     string `json:"binaryPath,omitempty"`
	ModelPath      string `json:"modelPath,omitempty"`
	TokensPath     string `json:"tokensPath,omitempty"`
	BinaryError    string `json:"binaryError,omitempty"`
	ModelError     string `json:"modelError,omitempty"`
	TokensError    string `json:"tokensError,omitempty"`
	Platform       string `json:"platform"`
	RuntimeName    string `json:"runtimeName"`
	RuntimeVersion string `json:"runtimeVersion"`
	ModelName      string `json:"modelName"`
	ModelRelease   string `json:"modelRelease"`
	ModelFileName  string `json:"modelFileName"`
	TokensFileName string `json:"tokensFileName"`
	Language       string `json:"language"`
	UseITN         bool   `json:"useITN"`
}

func GetLocalSenseVoiceStatus() LocalSenseVoiceStatus {
	status := LocalSenseVoiceStatus{
		Platform:       runtime.GOOS + "/" + runtime.GOARCH,
		RuntimeName:    localSenseVoiceRuntimeName,
		RuntimeVersion: localSenseVoiceRuntimeVersion,
		ModelName:      localSenseVoiceModelName,
		ModelRelease:   localSenseVoiceModelRelease,
		ModelFileName:  localSenseVoiceModelFileName,
		TokensFileName: localSenseVoiceTokensFileName,
		Language:       localSenseVoiceDefaultLanguage,
		UseITN:         localSenseVoiceUseITN,
	}

	if binaryPath, err := resolveSenseVoiceBinaryPath(); err == nil {
		status.BinaryFound = true
		status.BinaryPath = binaryPath
	} else {
		status.BinaryError = err.Error()
	}

	if modelPath, err := resolveSenseVoiceModelPath(); err == nil {
		status.ModelFound = true
		status.ModelPath = modelPath
		status.ModelFileName = filepath.Base(modelPath)
	} else {
		status.ModelError = err.Error()
	}

	if tokensPath, err := resolveSenseVoiceTokensPath(); err == nil {
		status.TokensFound = true
		status.TokensPath = tokensPath
		status.TokensFileName = filepath.Base(tokensPath)
	} else {
		status.TokensError = err.Error()
	}

	status.Ready = status.BinaryFound && status.ModelFound && status.TokensFound
	return status
}

func resolveSenseVoiceBinaryPath() (string, error) {
	binaryNames := []string{"sherpa-onnx-offline"}
	if runtime.GOOS == "windows" {
		binaryNames = []string{
			"sherpa-onnx-offline.exe",
			"auramate-sensevoice.exe",
			"sensevoice-offline.exe",
		}
	} else {
		binaryNames = []string{
			"sherpa-onnx-offline",
			"auramate-sensevoice",
			"sensevoice-offline",
		}
	}

	searchPatterns := []string{
		filepath.Join("sensevoice", "bin", "%s"),
		filepath.Join("release_bundle", "sensevoice", "bin", "%s"),
		filepath.Join("backend", "bin", "sensevoice", "%s"),
		filepath.Join("backend", "tools", "sensevoice", "%s"),
		filepath.Join("bin", "%s"),
		"%s",
	}

	return resolveLocalResource(
		[]string{"AURAMATE_SENSEVOICE_ONNX_PATH", "AURAMATE_SHERPA_ONNX_OFFLINE_PATH"},
		binaryNames,
		searchPatterns,
		"SenseVoice ONNX 可执行文件未找到，请确认打包资源目录 sensevoice/bin 中包含 sherpa-onnx-offline 或通过 AURAMATE_SENSEVOICE_ONNX_PATH 指向兼容运行时",
	)
}

func resolveSenseVoiceModelPath() (string, error) {
	searchPatterns := []string{
		filepath.Join("sensevoice", "models", "%s"),
		filepath.Join("sensevoice", "models", localSenseVoiceModelDirName, "%s"),
		filepath.Join("release_bundle", "sensevoice", "models", "%s"),
		filepath.Join("release_bundle", "sensevoice", "models", localSenseVoiceModelDirName, "%s"),
		filepath.Join("backend", "models", "sensevoice", "%s"),
		filepath.Join("models", "%s"),
		"%s",
	}

	return resolveLocalResource(
		[]string{"AURAMATE_SENSEVOICE_MODEL_PATH"},
		[]string{localSenseVoiceModelFileName},
		searchPatterns,
		"SenseVoice ONNX 模型文件未找到，请确认打包资源目录 sensevoice/models 中包含 model.int8.onnx",
	)
}

func resolveSenseVoiceTokensPath() (string, error) {
	searchPatterns := []string{
		filepath.Join("sensevoice", "models", "%s"),
		filepath.Join("sensevoice", "models", localSenseVoiceModelDirName, "%s"),
		filepath.Join("release_bundle", "sensevoice", "models", "%s"),
		filepath.Join("release_bundle", "sensevoice", "models", localSenseVoiceModelDirName, "%s"),
		filepath.Join("backend", "models", "sensevoice", "%s"),
		filepath.Join("models", "%s"),
		"%s",
	}

	return resolveLocalResource(
		[]string{"AURAMATE_SENSEVOICE_TOKENS_PATH"},
		[]string{localSenseVoiceTokensFileName},
		searchPatterns,
		"SenseVoice 词表文件未找到，请确认打包资源目录 sensevoice/models 中包含 tokens.txt",
	)
}

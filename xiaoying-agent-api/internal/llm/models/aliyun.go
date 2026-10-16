package models

const (
	QwenMax          ModelID = "qwen-max"
	QwenPlus         ModelID = "qwen3.5-plus"
	Qwen36Plus       ModelID = "qwen3.6-plus"
	QwenTurbo        ModelID = "qwen-turbo"
	QwenLong         ModelID = "qwen-long"
	QwenCoderPlus    ModelID = "qwen-coder-plus"
	QwenCoderTurbo   ModelID = "qwen-coder-turbo"
	QwenVLPlus       ModelID = "qwen-vl-plus"
	QwenVLMax        ModelID = "qwen-vl-max"
	QwenAudioTurbo   ModelID = "qwen-audio-turbo"
	Qwen3ASRFlash    ModelID = "qwen3-asr-flash"
	SenseVoiceV1     ModelID = "sensevoice-v1"
	DeepSeekV3       ModelID = "deepseek-v3"
	AliyunDeepSeekR1 ModelID = "deepseek-r1"
	DeepSeekV3_2     ModelID = "deepseek-v3.2"
	KimiK2           ModelID = "kimi-k2"
	MiniMaxM21       ModelID = "minimax-m2.1"
)

var deepSeekR1Temp = 0.6

var AliyunModels = map[ModelID]Model{
	MiniMaxM21: {
		ID:                  MiniMaxM21,
		Name:                "MiniMax M2.1",
		Provider:            ProviderAliyun,
		APIModel:            "MiniMax-M2.1",
		ContextWindow:       128_000,
		DefaultMaxTokens:    8192,
		SupportsAttachments: true,
	},
	KimiK2: {
		ID:                  KimiK2,
		Name:                "Kimi K2",
		Provider:            ProviderAliyun,
		APIModel:            "kimi-k2-thinking",
		ContextWindow:       128_000,
		DefaultMaxTokens:    8192,
		SupportsAttachments: true,
		CanReason:           true,
	},
	DeepSeekV3: {
		ID:                  DeepSeekV3,
		Name:                "DeepSeek V3",
		Provider:            ProviderAliyun,
		APIModel:            "deepseek-v3",
		ContextWindow:       64_000,
		DefaultMaxTokens:    8192,
		SupportsAttachments: false,
		CanReason:           false,
	},
	AliyunDeepSeekR1: {
		ID:                  AliyunDeepSeekR1,
		Name:                "DeepSeek R1",
		Provider:            ProviderAliyun,
		APIModel:            "deepseek-r1",
		ContextWindow:       256_000,
		DefaultMaxTokens:    8192,
		SupportsAttachments: false,
		CanReason:           false,
		DefaultTemperature:  &deepSeekR1Temp,
	},
	DeepSeekV3_2: {
		ID:                  DeepSeekV3_2,
		Name:                "DeepSeek V3.2",
		Provider:            ProviderAliyun,
		APIModel:            "deepseek-v3.2", // Mapping to v3 as v3.2 is likely a user alias for v3
		ContextWindow:       256_000,
		DefaultMaxTokens:    8192,
		SupportsAttachments: false,
	},
	QwenMax: {
		ID:                  QwenMax,
		Name:                "Qwen Max",
		Provider:            ProviderAliyun,
		APIModel:            "qwen3-max",
		CostPer1MIn:         0.0, // varies
		CostPer1MOut:        0.0,
		ContextWindow:       262_144, // Updated based on Qwen3 specs
		DefaultMaxTokens:    8192,
		SupportsAttachments: true,
	},
	QwenPlus: {
		ID:                  QwenPlus,
		Name:                "Qwen Plus",
		Provider:            ProviderAliyun,
		APIModel:            "qwen3.5-plus",
		ContextWindow:       1_000_000, // 1M tokens
		DefaultMaxTokens:    8192,
		SupportsAttachments: true,
	},
	Qwen36Plus: {
		ID:                  Qwen36Plus,
		Name:                "Qwen 3.6 Plus",
		Provider:            ProviderAliyun,
		APIModel:            "qwen3.6-plus",
		ContextWindow:       256_000,
		DefaultMaxTokens:    8192,
		SupportsAttachments: true,
	},
	QwenTurbo: {
		ID:                  QwenTurbo,
		Name:                "Qwen Turbo",
		Provider:            ProviderAliyun,
		APIModel:            "qwen-turbo",
		ContextWindow:       1_000_000,
		DefaultMaxTokens:    8192,
		SupportsAttachments: true,
	},
	QwenLong: {
		ID:                  QwenLong,
		Name:                "Qwen Long",
		Provider:            ProviderAliyun,
		APIModel:            "qwen-long",
		ContextWindow:       10_000_000,
		DefaultMaxTokens:    2000,
		SupportsAttachments: true,
	},
	QwenCoderPlus: {
		ID:                  QwenCoderPlus,
		Name:                "Qwen Coder Plus",
		Provider:            ProviderAliyun,
		APIModel:            "qwen-coder-plus",
		ContextWindow:       131_072,
		DefaultMaxTokens:    8192,
		SupportsAttachments: true,
	},
	QwenCoderTurbo: {
		ID:                  QwenCoderTurbo,
		Name:                "Qwen Coder Turbo",
		Provider:            ProviderAliyun,
		APIModel:            "qwen-coder-turbo",
		ContextWindow:       131_072,
		DefaultMaxTokens:    8192,
		SupportsAttachments: true,
	},
	QwenVLPlus: {
		ID:                  QwenVLPlus,
		Name:                "Qwen3 VL Plus",
		Provider:            ProviderAliyun,
		APIModel:            "qwen3-vl-plus",
		ContextWindow:       256_000,
		DefaultMaxTokens:    8192,
		SupportsAttachments: true,
	},
	QwenVLMax: {
		ID:                  QwenVLMax,
		Name:                "Qwen3 VL Max",
		Provider:            ProviderAliyun,
		APIModel:            "qwen3-vl-max",
		ContextWindow:       32_000,
		DefaultMaxTokens:    2048,
		SupportsAttachments: true,
	},
	QwenAudioTurbo: {
		ID:                  QwenAudioTurbo,
		Name:                "Qwen Audio Turbo",
		Provider:            ProviderAliyun,
		APIModel:            "qwen-audio-turbo",
		ContextWindow:       128_000,
		DefaultMaxTokens:    2048,
		SupportsAttachments: true,
	},
	Qwen3ASRFlash: {
		ID:                  Qwen3ASRFlash,
		Name:                "Qwen3 ASR Flash",
		Provider:            ProviderAliyun,
		APIModel:            "qwen3-asr-flash",
		ContextWindow:       128_000,
		DefaultMaxTokens:    2048,
		SupportsAttachments: true,
	},
}

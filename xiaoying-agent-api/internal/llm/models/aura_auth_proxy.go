package models

const (
	AuraAuthProxyContextWindow int64   = 256_000
	DeepSeekV4Flash            ModelID = "deepseek-v4-flash"
	DeepSeekV4Pro              ModelID = "deepseek-v4-pro"
	KimiK26                    ModelID = "kimi-k2.6"
	GLM51                      ModelID = "glm-5.1"
	MiniMaxM27                 ModelID = "minimax-m2.7"
	DoubaoSeed20Pro            ModelID = "doubao-seed-2.0-pro"
)

var AuraAuthProxyModels = map[ModelID]Model{
	DoubaoSeed20Pro: {
		ID:                  DoubaoSeed20Pro,
		Name:                "Doubao Seed 2.0 Pro",
		Provider:            ProviderCustom,
		APIModel:            string(DoubaoSeed20Pro),
		ContextWindow:       AuraAuthProxyContextWindow,
		DefaultMaxTokens:    8192,
		SupportsAttachments: true,
		CanReason:           false,
	},
	DeepSeekV4Flash: {
		ID:                  DeepSeekV4Flash,
		Name:                "deepseek-v4-flash",
		Provider:            ProviderCustom,
		APIModel:            string(DeepSeekV4Flash),
		ContextWindow:       AuraAuthProxyContextWindow,
		DefaultMaxTokens:    8192,
		SupportsAttachments: true,
		CanReason:           true,
	},
	DeepSeekV4Pro: {
		ID:                  DeepSeekV4Pro,
		Name:                "deepseek-v4-pro",
		Provider:            ProviderCustom,
		APIModel:            string(DeepSeekV4Pro),
		ContextWindow:       AuraAuthProxyContextWindow,
		DefaultMaxTokens:    8192,
		SupportsAttachments: true,
		CanReason:           true,
	},
	KimiK26: {
		ID:                  KimiK26,
		Name:                "kimi-k2.6",
		Provider:            ProviderCustom,
		APIModel:            string(KimiK26),
		ContextWindow:       AuraAuthProxyContextWindow,
		DefaultMaxTokens:    8192,
		SupportsAttachments: true,
		CanReason:           true,
	},
	GLM51: {
		ID:                  GLM51,
		Name:                "glm-5.1",
		Provider:            ProviderCustom,
		APIModel:            string(GLM51),
		ContextWindow:       AuraAuthProxyContextWindow,
		DefaultMaxTokens:    8192,
		SupportsAttachments: true,
		CanReason:           true,
	},
	MiniMaxM27: {
		ID:                  MiniMaxM27,
		Name:                "minimax-m2.7",
		Provider:            ProviderCustom,
		APIModel:            string(MiniMaxM27),
		ContextWindow:       AuraAuthProxyContextWindow,
		DefaultMaxTokens:    8192,
		SupportsAttachments: true,
		CanReason:           true,
	},
}

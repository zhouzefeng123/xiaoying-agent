package models

const (
	DeepSeekChat  ModelID = "deepseek-chat"
	DeepSeekCoder ModelID = "deepseek-coder"
	DeepSeekR1    ModelID = "deepseek-reasoner"
)

var DeepSeekModels = map[ModelID]Model{
	DeepSeekChat: {
		ID:                  DeepSeekChat,
		Name:                "DeepSeek Chat",
		Provider:            ProviderDeepSeek,
		APIModel:            "deepseek-chat",
		CostPer1MIn:         0.14, // Approx $0.14
		CostPer1MInCached:   0.014,
		CostPer1MOutCached:  0.0,
		CostPer1MOut:        0.28, // Approx $0.28
		ContextWindow:       96_000,
		DefaultMaxTokens:    8000,
		SupportsAttachments: false,
	},
	DeepSeekCoder: {
		ID:                  DeepSeekCoder,
		Name:                "DeepSeek Coder",
		Provider:            ProviderDeepSeek,
		APIModel:            "deepseek-coder",
		CostPer1MIn:         0.14,
		CostPer1MInCached:   0.014,
		CostPer1MOutCached:  0.0,
		CostPer1MOut:        0.28,
		ContextWindow:       96_000,
		DefaultMaxTokens:    8000,
		SupportsAttachments: false,
	},
	DeepSeekR1: {
		ID:                  DeepSeekR1,
		Name:                "DeepSeek R1 (Reasoner)",
		Provider:            ProviderDeepSeek,
		APIModel:            "deepseek-reasoner",
		CostPer1MIn:         0.55,
		CostPer1MInCached:   0.14,
		CostPer1MOutCached:  0.0,
		CostPer1MOut:        2.19,
		ContextWindow:       96_000,
		DefaultMaxTokens:    8000,
		CanReason:           true,
		SupportsAttachments: false,
	},
}

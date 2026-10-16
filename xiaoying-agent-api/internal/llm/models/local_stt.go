package models

const (
	LocalSenseVoice ModelID = "local-sensevoice"
)

func init() {
	SupportedModels[LocalSenseVoice] = Model{
		ID:                  LocalSenseVoice,
		Name:                "SenseVoice Small ONNX 本地语音识别",
		Provider:            ProviderLocal,
		APIModel:            "local-sensevoice",
		SupportsAttachments: false,
	}

	if _, exists := ProviderPopularity[ProviderLocal]; !exists {
		ProviderPopularity[ProviderLocal] = 0
	}
}

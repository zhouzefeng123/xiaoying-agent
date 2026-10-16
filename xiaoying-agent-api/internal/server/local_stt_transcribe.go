package server

import (
	"context"
	"fmt"
	"os"

	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/provider"
)

func (s *Server) newLocalSTTProvider(modelID models.ModelID) (provider.Provider, error) {
	targetModel, found := models.SupportedModels[modelID]
	if !found {
		return nil, fmt.Errorf("local stt model not found: %s", modelID)
	}

	apiKey := ""
	if s.app.Config != nil {
		if providerConfig, ok := s.app.Config.Providers[targetModel.Provider]; ok {
			apiKey = providerConfig.APIKey
		}
	}
	if apiKey == "" {
		switch targetModel.Provider {
		case models.ProviderAliyun:
			apiKey = os.Getenv("ALIYUN_API_KEY")
			if apiKey == "" {
				apiKey = os.Getenv("DASHSCOPE_API_KEY")
			}
		case models.ProviderOpenAI:
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
	}

	return provider.NewProvider(
		targetModel.Provider,
		provider.WithAPIKey(apiKey),
		provider.WithModel(targetModel),
	)
}

func (s *Server) transcribeLocalAudio(ctx context.Context, modelID models.ModelID, audioData []byte, filename string) (string, error) {
	localProvider, err := s.newLocalSTTProvider(modelID)
	if err != nil {
		return "", err
	}

	return localProvider.Transcribe(ctx, audioData, filename)
}

func (s *Server) transcribeLocalSenseVoiceAudio(ctx context.Context, audioData []byte, filename string) (string, error) {
	return s.transcribeLocalAudio(ctx, models.LocalSenseVoice, audioData, filename)
}
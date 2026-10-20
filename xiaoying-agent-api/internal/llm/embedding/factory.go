package embedding

import (
	"fmt"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/logging"
)

const (
	FixedEmbeddingProvider = models.ProviderLocal
	FixedEmbeddingModel    = "all-MiniLM-L6-v2"
)

func NewConfiguredEmbedder(cfg *config.Config) (Embedder, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	embedder, err := NewLocalCLIEmbedder(FixedEmbeddingModel, "")
	if err != nil {
		logging.Warn("Local OpenVINO embedder unavailable; continuing without vector embeddings", "error", err)
		return nil, nil
	}

	return embedder, nil
}

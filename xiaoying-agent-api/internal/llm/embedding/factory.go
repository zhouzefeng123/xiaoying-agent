package embedding

import (
	"fmt"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
)

const (
	FixedEmbeddingProvider = models.ProviderLocal
	FixedEmbeddingModel    = "all-MiniLM-L6-v2"
)

func NewConfiguredEmbedder(cfg *config.Config) (Embedder, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	return NewLocalCLIEmbedder(FixedEmbeddingModel, "")
}

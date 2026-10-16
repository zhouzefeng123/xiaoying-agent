package vision

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/provider"
	"github.com/auramate/auramate/internal/llm/routing"
	"github.com/auramate/auramate/internal/message"
)

const visualAnalysisMaxTokens = 2048

type Analyzer struct {
	apiKey string
}

func NewAnalyzer(apiKey string) (*Analyzer, error) {
	return &Analyzer{apiKey: apiKey}, nil
}

func (a *Analyzer) Analyze(ctx context.Context, prompt string, imageContent message.BinaryContent) (string, error) {
	client, err := buildVisualAnalysisProvider(ctx, a.apiKey)
	if err != nil {
		return "", err
	}

	resp, err := client.SendMessages(ctx, []message.Message{
		{
			Role: message.System,
			Parts: []message.ContentPart{
				message.TextContent{Text: "你是一个视觉分析工具。请结合图片内容准确回答用户问题，必要时提取其中的文本。"},
			},
		},
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: prompt},
				imageContent,
			},
		},
	}, nil)
	if err != nil {
		return "", err
	}

	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return "", fmt.Errorf("视觉模型返回了空结果")
	}

	return content, nil
}

func buildVisualAnalysisProvider(_ context.Context, apiKey string) (provider.Provider, error) {
	cfg := config.Get()
	if cfg == nil {
		return nil, fmt.Errorf("visual-analysis config unavailable")
	}

	candidateModels := routing.ResolveAgentModelCandidates(config.AgentVisual, "", cfg.Agents)
	if len(candidateModels) == 0 {
		return nil, fmt.Errorf("visual-analysis requires a configured visual model")
	}

	visualCfg := cfg.Agents[config.AgentVisual]
	visualProvider, _, err := routing.BuildProviderChain(config.AgentVisual, candidateModels, "", visualAnalysisMaxTokens, visualCfg.ReasoningEffort)
	if err != nil {
		return nil, err
	}
	return visualProvider, nil
}

func newVisualProvider(model models.Model, apiKey string, baseURL string) (provider.Provider, error) {
	opts := []provider.ProviderClientOption{
		provider.WithAPIKey(apiKey),
		provider.WithModel(model),
		provider.WithMaxTokens(visualAnalysisMaxTokens),
	}

	if baseURL != "" {
		opts = append(opts, provider.WithOpenAIOptions(
			provider.WithOpenAIBaseURL(baseURL),
		))
	}

	return provider.NewProvider(model.Provider, opts...)
}

func resolveVisualAnalysisEnvAPIKey() string {
	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("ALIYUN_API_KEY")
	}

	return apiKey
}

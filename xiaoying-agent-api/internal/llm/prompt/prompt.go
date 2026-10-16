package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/logging"
)

// GetBaseSystemPrompt returns the minimal core protocols for ALL agents.
// It includes: Role, Thinking, Metacognition, Loyalty, Performance, Cleanup.
// Note: It does NOT include Verification/Debugging/Memory, which are specific to certain agents.
func GetBaseSystemPrompt() string {
	return fmt.Sprintf(`
# Role: 小英（XiaoYing） - Professional AI Assistant
- **Tone**: Concise, Professional (Chinese/中文).

%s

%s

%s

%s

%s`, ThinkingProtocol, MetacognitionProtocol, LoyaltyProtocol, PerformanceProtocol, CleanupProtocol)
}

func GetAgentPrompt(agentName config.AgentName, provider models.ModelProvider) string {
	basePrompt := ""
	switch agentName {
	case config.AgentCoder:
		basePrompt = CoderPrompt(provider)
	case config.AgentTitle:
		basePrompt = TitlePrompt(provider)
	case config.AgentTask:
		basePrompt = TaskPrompt(provider)
	case config.AgentSummarizer:
		basePrompt = SummarizerPrompt(provider)
	case config.AgentLife:
		basePrompt = LifePrompt(provider)
	case config.AgentNormal:
		basePrompt = NormalPrompt(provider)
	default:
		basePrompt = "You are 小英（XiaoYing）, a helpful assistant."
	}

	if agentName == config.AgentCoder || agentName == config.AgentTask {
		// Add context from project-specific instruction files if they exist
		contextContent := getContextFromPaths()
		logging.Debug("Context content", "Context", contextContent)
		if contextContent != "" {
			// CACHE OPTIMIZATION: Project Context is heavy and semi-static.
			// It should be injected AFTER base protocols but BEFORE dynamic info?
			// Currently basePrompt includes dynamic info at the end (for Coder).
			// This is suboptimal.
			// Ideally: [Protocols] -> [Project Context] -> [Dynamic Info]
			// But refactoring CoderPrompt to return parts is complex.
			// For now, we append it. If CoderPrompt puts dynamic info at the end,
			// then Project Context is appended AFTER dynamic info, which is ... okay?
			// Actually, if CoderPrompt ends with Time/Date, and we append Context here,
			// then Context is also invalidated by Time/Date changes.
			// FIX: We need to inject Context BEFORE dynamic info.
			// However, without changing CoderPrompt signature, we can't easily insert in middle.
			// Given that Project Context changes rarely compared to Time,
			// appending it at the very end means:
			// Prefix = [Protocols] -> [LSP]
			// Dynamic = [Time/Env]
			// Suffix = [Project Context]
			// The Prefix is cached. The Dynamic breaks cache. The Suffix is re-processed.
			// This is BAD.

			// If we can't change CoderPrompt easily, we accept this for now.
			// OR, we assume CoderPrompt structure is: Base + LSP + Env.
			// We are appending here.

			if agentName == config.AgentCoder {
				return fmt.Sprintf("%s\n\n# Project-Specific Context\n Make sure to follow the instructions in the context below\n%s", basePrompt, contextContent)
			}
			return fmt.Sprintf("%s\n\n# Project-Specific Context\n Make sure to follow the instructions in the context below\n%s\n\n%s", basePrompt, contextContent, LanguageProtocol)
		}
	}

	// Default language instruction
	return fmt.Sprintf("%s\n\n%s", basePrompt, LanguageProtocol)
}

var (
	onceContext    sync.Once
	contextContent string
)

func GetContextFromPaths() string {
	return getContextFromPaths()
}

func getContextFromPaths() string {
	onceContext.Do(func() {
		var (
			cfg          = config.Get()
			workDir      = cfg.WorkingDir
			contextPaths = cfg.ContextPaths
		)

		contextContent = processContextPaths(workDir, contextPaths)
	})

	return contextContent
}

func processContextPaths(workDir string, paths []string) string {
	var (
		wg       sync.WaitGroup
		resultCh = make(chan string)
	)

	// Track processed files to avoid duplicates
	processedFiles := make(map[string]bool)
	var processedMutex sync.Mutex

	for _, path := range paths {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()

			if strings.HasSuffix(p, "/") {
				filepath.WalkDir(filepath.Join(workDir, p), func(path string, d os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if !d.IsDir() {
						// Check if we've already processed this file (case-insensitive)
						processedMutex.Lock()
						lowerPath := strings.ToLower(path)
						if !processedFiles[lowerPath] {
							processedFiles[lowerPath] = true
							processedMutex.Unlock()

							if result := processFile(path); result != "" {
								resultCh <- result
							}
						} else {
							processedMutex.Unlock()
						}
					}
					return nil
				})
			} else {
				fullPath := filepath.Join(workDir, p)

				// Check if we've already processed this file (case-insensitive)
				processedMutex.Lock()
				lowerPath := strings.ToLower(fullPath)
				if !processedFiles[lowerPath] {
					processedFiles[lowerPath] = true
					processedMutex.Unlock()

					result := processFile(fullPath)
					if result != "" {
						resultCh <- result
					}
				} else {
					processedMutex.Unlock()
				}
			}
		}(path)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	results := make([]string, 0)
	for result := range resultCh {
		results = append(results, result)
	}

	// Sort results to ensure deterministic order for caching
	// This is CRITICAL for Prefix Caching stability across server restarts
	sort.Strings(results)

	return strings.Join(results, "\n")
}

func processFile(filePath string) string {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	return "# From:" + filePath + "\n" + string(content)
}

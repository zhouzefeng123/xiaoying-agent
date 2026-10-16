package tools

import (
	"fmt"
	"strings"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/lsp"
)

func lspClientsSnapshot(registry *lsp.Registry) map[string]*lsp.Client {
	if registry == nil {
		return nil
	}
	return registry.Snapshot()
}

func readyLSPClientsSnapshot(registry *lsp.Registry) map[string]*lsp.Client {
	ready := make(map[string]*lsp.Client)
	for name, client := range lspClientsSnapshot(registry) {
		if client != nil && client.GetServerState() == lsp.StateReady {
			ready[name] = client
		}
	}
	return ready
}

func configuredLSPLanguages() map[string]bool {
	languages := map[string]bool{}
	effectiveLSPConfigs := config.EffectiveLSPConfigs(config.Get())
	if len(effectiveLSPConfigs) == 0 {
		return languages
	}
	for language, lspCfg := range effectiveLSPConfigs {
		if lspCfg.Disabled {
			continue
		}
		languages[strings.ToLower(strings.TrimSpace(language))] = true
	}
	return languages
}

func lspUnavailableSummary(registry *lsp.Registry) string {
	statuses := lsp.KnownOrConfiguredStatuses(registry, configuredLSPLanguages())
	if len(statuses) == 0 {
		return "no ready LSP clients available"
	}

	lines := []string{"no ready LSP clients available. Current status:"}
	for _, status := range statuses {
		if status.Phase == lsp.RuntimePhaseReady {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s", status.Summary()))
	}
	if len(lines) == 1 {
		lines = append(lines, "- LSP startup has not been triggered yet")
	}
	return strings.Join(lines, "\n")
}

func lspStatusNoteForFile(filePath string, registry *lsp.Registry) string {
	language := lsp.ServerKeyForFile(filePath)
	if language == "" {
		return ""
	}
	if registry != nil {
		if status, ok := registry.Status(language); ok {
			if status.Phase == lsp.RuntimePhaseReady {
				return ""
			}
			return fmt.Sprintf("\n<lsp_status>\n%s\n</lsp_status>\n", status.Summary())
		}
	}
	if !configuredLSPLanguages()[language] {
		return ""
	}

	status, ok := lsp.KnownOrConfiguredStatus(language, registry)
	if !ok || status.Phase == lsp.RuntimePhaseReady {
		return ""
	}
	return fmt.Sprintf("\n<lsp_status>\n%s\n</lsp_status>\n", status.Summary())
}

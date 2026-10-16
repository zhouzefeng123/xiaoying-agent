package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/lsp"
	"github.com/auramate/auramate/internal/lsp/protocol"
)

const (
	DiagnosticsToolName    = "get_diagnostics"
	diagnosticsDescription = `Gets code diagnostics (errors, warnings).
Usage:
- file_path: File to check (empty for project).
Features:
- Groups by severity.
- Integrates with LSP.
`
)

type DiagnosticsParams struct {
	FilePath string `json:"file_path"`
}
type diagnosticsTool struct {
	lspRegistry *lsp.Registry
}

func NewDiagnosticsTool(lspRegistry *lsp.Registry) BaseTool {
	return &diagnosticsTool{
		lspRegistry,
	}
}

func (b *diagnosticsTool) Info() ToolInfo {
	return ToolInfo{
		Name:        DiagnosticsToolName,
		Description: diagnosticsDescription,
		Parameters: map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The path to the file to get diagnostics for (leave w empty for project diagnostics)",
			},
		},
		Required: []string{},
	}
}

func (b *diagnosticsTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params DiagnosticsParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}

	lsps := lspClientsSnapshot(b.lspRegistry)

	if len(lsps) == 0 {
		return NewTextErrorResponse(lspUnavailableSummary(b.lspRegistry)), nil
	}

	if params.FilePath != "" {
		notifyLspOpenFile(ctx, params.FilePath, b.lspRegistry)
		waitForLspDiagnostics(ctx, params.FilePath, b.lspRegistry)
	}

	output := getDiagnostics(params.FilePath, b.lspRegistry)
	output += lspStatusNoteForFile(params.FilePath, b.lspRegistry)

	return NewTextResponse(output), nil
}

func notifyLspOpenFile(ctx context.Context, filePath string, registry *lsp.Registry) {
	for _, client := range lspClientsSnapshot(registry) {
		err := client.OpenFile(ctx, filePath)
		if err != nil {
			continue
		}
	}
}

func waitForLspDiagnostics(ctx context.Context, filePath string, registry *lsp.Registry) {
	lsps := lspClientsSnapshot(registry)
	if len(lsps) == 0 {
		return
	}

	diagChan := make(chan struct{}, 1)

	for _, client := range lsps {
		originalDiags := client.GetDiagnostics()

		handler := func(params json.RawMessage) {
			lsp.HandleDiagnostics(client, params)
			var diagParams protocol.PublishDiagnosticsParams
			if err := json.Unmarshal(params, &diagParams); err != nil {
				return
			}

			if diagParams.URI.Path() == filePath || hasDiagnosticsChanged(client.GetDiagnostics(), originalDiags) {
				select {
				case diagChan <- struct{}{}:
				default:
				}
			}
		}

		client.RegisterNotificationHandler("textDocument/publishDiagnostics", handler)

		if client.IsFileOpen(filePath) {
			err := client.NotifyChange(ctx, filePath)
			if err != nil {
				continue
			}
		} else {
			err := client.OpenFile(ctx, filePath)
			if err != nil {
				continue
			}
		}
	}

	select {
	case <-diagChan:
	case <-time.After(500 * time.Millisecond):
	case <-ctx.Done():
	}
}

func hasDiagnosticsChanged(current, original map[protocol.DocumentUri][]protocol.Diagnostic) bool {
	for uri, diags := range current {
		origDiags, exists := original[uri]
		if !exists || len(diags) != len(origDiags) {
			return true
		}
	}
	return false
}

func getDiagnostics(filePath string, registry *lsp.Registry) string {
	lsps := lspClientsSnapshot(registry)
	fileDiagnostics := []string{}
	projectDiagnostics := []string{}

	formatDiagnostic := func(pth string, diagnostic protocol.Diagnostic, source string) string {
		severity := "Info"
		switch diagnostic.Severity {
		case protocol.SeverityError:
			severity = "Error"
		case protocol.SeverityWarning:
			severity = "Warn"
		case protocol.SeverityHint:
			severity = "Hint"
		}

		location := fmt.Sprintf("%s:%d:%d", pth, diagnostic.Range.Start.Line+1, diagnostic.Range.Start.Character+1)

		sourceInfo := ""
		if diagnostic.Source != "" {
			sourceInfo = diagnostic.Source
		} else if source != "" {
			sourceInfo = source
		}

		codeInfo := ""
		if diagnostic.Code != nil {
			codeInfo = fmt.Sprintf("[%v]", diagnostic.Code)
		}

		tagsInfo := ""
		if len(diagnostic.Tags) > 0 {
			tags := []string{}
			for _, tag := range diagnostic.Tags {
				switch tag {
				case protocol.Unnecessary:
					tags = append(tags, "unnecessary")
				case protocol.Deprecated:
					tags = append(tags, "deprecated")
				}
			}
			if len(tags) > 0 {
				tagsInfo = fmt.Sprintf(" (%s)", strings.Join(tags, ", "))
			}
		}

		return fmt.Sprintf("%s: %s [%s]%s%s %s",
			severity,
			location,
			sourceInfo,
			codeInfo,
			tagsInfo,
			diagnostic.Message)
	}

	for lspName, client := range lsps {
		diagnostics := client.GetDiagnostics()
		if len(diagnostics) > 0 {
			for location, diags := range diagnostics {
				isCurrentFile := location.Path() == filePath

				for _, diag := range diags {
					formattedDiag := formatDiagnostic(location.Path(), diag, lspName)

					if isCurrentFile {
						fileDiagnostics = append(fileDiagnostics, formattedDiag)
					} else {
						projectDiagnostics = append(projectDiagnostics, formattedDiag)
					}
				}
			}
		}
	}

	sort.Slice(fileDiagnostics, func(i, j int) bool {
		iIsError := strings.HasPrefix(fileDiagnostics[i], "Error")
		jIsError := strings.HasPrefix(fileDiagnostics[j], "Error")
		if iIsError != jIsError {
			return iIsError // Errors come first
		}
		return fileDiagnostics[i] < fileDiagnostics[j] // Then alphabetically
	})

	sort.Slice(projectDiagnostics, func(i, j int) bool {
		iIsError := strings.HasPrefix(projectDiagnostics[i], "Error")
		jIsError := strings.HasPrefix(projectDiagnostics[j], "Error")
		if iIsError != jIsError {
			return iIsError
		}
		return projectDiagnostics[i] < projectDiagnostics[j]
	})

	output := ""

	if len(fileDiagnostics) > 0 {
		output += "\n<file_diagnostics>\n"
		if len(fileDiagnostics) > 10 {
			output += strings.Join(fileDiagnostics[:10], "\n")
			output += fmt.Sprintf("\n... and %d more diagnostics", len(fileDiagnostics)-10)
		} else {
			output += strings.Join(fileDiagnostics, "\n")
		}
		output += "\n</file_diagnostics>\n"
	}

	if len(projectDiagnostics) > 0 {
		output += "\n<project_diagnostics>\n"
		if len(projectDiagnostics) > 10 {
			output += strings.Join(projectDiagnostics[:10], "\n")
			output += fmt.Sprintf("\n... and %d more diagnostics", len(projectDiagnostics)-10)
		} else {
			output += strings.Join(projectDiagnostics, "\n")
		}
		output += "\n</project_diagnostics>\n"
	}

	if len(fileDiagnostics) > 0 || len(projectDiagnostics) > 0 {
		fileErrors := countSeverity(fileDiagnostics, "Error")
		fileWarnings := countSeverity(fileDiagnostics, "Warn")
		projectErrors := countSeverity(projectDiagnostics, "Error")
		projectWarnings := countSeverity(projectDiagnostics, "Warn")

		output += "\n<diagnostic_summary>\n"
		output += fmt.Sprintf("Current file: %d errors, %d warnings\n", fileErrors, fileWarnings)
		output += fmt.Sprintf("Project: %d errors, %d warnings\n", projectErrors, projectWarnings)
		output += "</diagnostic_summary>\n"
	}

	return output
}

func countSeverity(diagnostics []string, severity string) int {
	count := 0
	for _, diag := range diagnostics {
		if strings.HasPrefix(diag, severity) {
			count++
		}
	}
	return count
}

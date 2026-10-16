package server

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/auramate/auramate/internal/lsp"
	"github.com/auramate/auramate/internal/message"
)

var (
	lspCodeFencePattern = regexp.MustCompile("(?m)```\\s*([A-Za-z0-9_+.-]+)")
	lspFilePathPattern  = regexp.MustCompile(`(?i)[A-Za-z0-9_./\\:-]+\.(go|py|ts|tsx|js|jsx|mts|cts|rs)\b`)
)

func shouldStartLSPForClientMessage(msg ClientMessage) bool {
	return isCodingLSPRequest(msg.Mode, msg.Model)
}

func isCodingLSPRequest(mode string, model string) bool {
	if strings.EqualFold(strings.TrimSpace(mode), "coding") {
		return true
	}

	switch strings.TrimSpace(model) {
	case "aura-coder", "coder":
		return true
	default:
		return false
	}
}

func inferLSPLanguagesFromClientMessage(msg ClientMessage, workspaceRoot string) []string {
	if !shouldStartLSPForClientMessage(msg) {
		return nil
	}

	languages := map[string]bool{}
	addLanguagesFromText(languages, msg.Content)
	addLanguagesFromAttachments(languages, msg.Attachments)

	if len(languages) == 0 {
		addLanguagesFromWorkspaceMarkers(languages, workspaceRoot)
	}

	return orderedLanguages(languages)
}

func inferLSPLanguagesFromChatRequest(req ChatCompletionRequest, mode string, model string, workspaceRoot string) []string {
	if !isCodingLSPRequest(mode, model) {
		return nil
	}

	languages := map[string]bool{}
	for _, msg := range req.Messages {
		addLanguagesFromText(languages, msg.Content)
	}

	if len(languages) == 0 {
		addLanguagesFromWorkspaceMarkers(languages, workspaceRoot)
	}

	return orderedLanguages(languages)
}

func addLanguagesFromAttachments(languages map[string]bool, attachments []message.Attachment) {
	for _, attachment := range attachments {
		addLanguageFromFilePath(languages, attachment.FilePath)
		addLanguageFromFilePath(languages, attachment.FileName)
	}
}

func addLanguagesFromText(languages map[string]bool, text string) {
	for _, match := range lspCodeFencePattern.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		addLanguage(languages, match[1])
	}

	for _, match := range lspFilePathPattern.FindAllString(text, -1) {
		addLanguageFromFilePath(languages, match)
	}
}

func addLanguagesFromWorkspaceMarkers(languages map[string]bool, workspaceRoot string) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return
	}

	markers := map[string][]string{
		"go":         {"go.mod"},
		"python":     {"pyproject.toml", "requirements.txt", "setup.py"},
		"typescript": {"package.json", "tsconfig.json", "jsconfig.json"},
		"rust":       {"Cargo.toml"},
	}

	for language, names := range markers {
		for _, name := range names {
			if fileExists(filepath.Join(workspaceRoot, name)) {
				addLanguage(languages, language)
				break
			}
		}
	}
}

func addLanguageFromFilePath(languages map[string]bool, filePath string) {
	addLanguage(languages, lsp.ServerKeyForFile(filePath))
}

func addLanguage(languages map[string]bool, language string) {
	key := lsp.ServerKeyForLanguage(language)
	if key != "" {
		languages[key] = true
	}
}

func orderedLanguages(languages map[string]bool) []string {
	ordered := make([]string, 0, len(languages))
	for language := range languages {
		ordered = append(ordered, language)
	}
	sort.Strings(ordered)
	return ordered
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

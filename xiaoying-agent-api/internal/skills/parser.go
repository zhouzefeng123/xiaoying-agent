package skills

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/auramate/auramate/internal/llm/tools"
)

// SkillDefinition represents a parsed skill from a script
type SkillDefinition struct {
	Name            string
	Description     string
	Parameters      map[string]any
	Required        []string
	ScriptPath      string
	Language        string            // "python", "powershell", or "markdown"
	Content         string            // For markdown skills
	ArgStyles       map[string]string // "flag" or "value" for boolean args
	DefaultActive   bool              // Whether the skill should be active by default
	CallFormat      string            // "cli_flags" (default), "cli_positional", "json_stdin", "template", "document"
	CommandTemplate string            // For "template" format: "python {{script_path}} --search {{keywords}}"
	EnvMapping      map[string]string // Maps param name to env var name: {"api_key": "MY_SECRET_KEY"}
}

// ToToolInfo converts a SkillDefinition to a ToolInfo
func (s *SkillDefinition) ToToolInfo() tools.ToolInfo {
	desc := s.Description

	// Inject usage instructions into the description
	if s.Content != "" {
		desc = desc + "\n\n<skills_instructions>\n" + s.Content + "\n</skills_instructions>"
	}

	// For document-only skills, we ensure they don't look like executable tools
	// by overriding the parameters to just accept an intent.
	params := s.Parameters
	required := s.Required

	if s.CallFormat == "document" || s.Language == "markdown" {
		// Document skills just need the agent to declare its intent before reading the manual
		params = map[string]any{
			"action_intent": map[string]string{
				"type":        "string",
				"description": "Briefly state what you intend to do (e.g., 'read instructions'). This tool does NOT execute actions, it returns the instruction manual for you to read and act upon.",
			},
		}
		required = []string{"action_intent"}
	}

	return tools.ToolInfo{
		Name:        s.Name,
		Description: desc,
		Parameters:  params,
		Required:    required,
	}
}

// Parser defines the interface for script parsers
type Parser interface {
	Parse(filePath string) (*SkillDefinition, error)
	CanParse(filePath string) bool
}

// PythonParser parses Python scripts for tool definitions
type PythonParser struct{}

func (p *PythonParser) CanParse(filePath string) bool {
	// LEGACY: We no longer parse Python files directly.
	// All skills must be loaded via SKILL.md
	return false
}

// Parse is a legacy stub
func (p *PythonParser) Parse(filePath string) (*SkillDefinition, error) {
	return nil, fmt.Errorf("direct python parsing is deprecated, use SKILL.md")
}

// scanArgparseDefinitions scans a python file for argparse definitions
// This is used as a helper by ClaudeSkillParser as a last resort
func scanArgparseDefinitions(scriptPath string) (map[string]any, []string) {
	params := map[string]any{}
	required := []string{}

	file, err := os.Open(scriptPath)
	if err != nil {
		return params, required
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	// Regex for argparse/click
	// Matches: '--name'
	reArgName := regexp.MustCompile(`['"]--([a-zA-Z0-9_-]+)['"]`)
	reHelp := regexp.MustCompile(`help\s*=\s*['"]([^'"]+)['"]`)
	reType := regexp.MustCompile(`type\s*=\s*([a-zA-Z0-9_]+)`)
	reRequired := regexp.MustCompile(`required\s*=\s*True`)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "add_argument") || strings.Contains(line, "click.option") {
			// Extract name
			nameMatch := reArgName.FindStringSubmatch(line)
			if nameMatch == nil {
				continue
			}
			name := nameMatch[1]

			// Extract help
			desc := fmt.Sprintf("Parameter %s", name)
			helpMatch := reHelp.FindStringSubmatch(line)
			if helpMatch != nil {
				desc = helpMatch[1]
			}

			// Extract type
			typ := "string"
			typeMatch := reType.FindStringSubmatch(line)
			if typeMatch != nil {
				typ = jsonTypeFromPython(typeMatch[1])
			}
			// Check for store_true -> boolean
			if strings.Contains(line, "store_true") || strings.Contains(line, "is_flag=True") {
				typ = "boolean"
			}

			params[name] = map[string]string{
				"type":        typ,
				"description": desc,
			}

			// Check required (Explicitly required=True for argparse flags)
			if reRequired.MatchString(line) {
				required = append(required, name)
			}
		}
	}
	return params, required
}

func jsonTypeFromPython(pyType string) string {
	switch strings.ToLower(pyType) {
	case "str", "string":
		return "string"
	case "int", "integer":
		return "integer"
	case "float", "number":
		return "number"
	case "bool", "boolean":
		return "boolean"
	default:
		return "string"
	}
}

// detectArgStyles scans the python script to infer argument passing styles
func detectArgStyles(scriptPath string) map[string]string {
	styles := make(map[string]string)
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return styles
	}
	src := string(content)
	argNameRe := regexp.MustCompile(`['"]--([a-zA-Z0-9_-]+)['"]`)

	for _, call := range extractPythonCallBlocks(src, "add_argument") {
		nameMatch := argNameRe.FindStringSubmatch(call)
		if len(nameMatch) < 2 {
			continue
		}

		name := nameMatch[1]
		if strings.Contains(call, "action='store_true'") || strings.Contains(call, `action="store_true"`) {
			styles[name] = "flag"
			continue
		}

		if regexp.MustCompile(`\btype\s*=`).MatchString(call) {
			if _, ok := styles[name]; !ok {
				styles[name] = "value"
			}
		}
	}

	// click is_flag=True: @click.option('--name', ... is_flag=True)
	reClickFlag := regexp.MustCompile(`(?s)click\.option\s*\(\s*['"]--([a-zA-Z0-9_-]+)['"].*?is_flag=True`)
	matchesClick := reClickFlag.FindAllStringSubmatch(src, -1)
	for _, m := range matchesClick {
		styles[m[1]] = "flag"
	}

	return styles
}

func extractPythonCallBlocks(src string, funcName string) []string {
	var blocks []string
	needle := funcName + "("
	searchStart := 0

	for {
		idx := strings.Index(src[searchStart:], needle)
		if idx == -1 {
			return blocks
		}
		start := searchStart + idx
		openParen := start + len(funcName)
		depth := 0
		inSingleQuote := false
		inDoubleQuote := false
		escaped := false

		for i := openParen; i < len(src); i++ {
			ch := src[i]

			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' && (inSingleQuote || inDoubleQuote) {
				escaped = true
				continue
			}
			if ch == '\'' && !inDoubleQuote {
				inSingleQuote = !inSingleQuote
				continue
			}
			if ch == '"' && !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
				continue
			}
			if inSingleQuote || inDoubleQuote {
				continue
			}

			switch ch {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					blocks = append(blocks, src[start:i+1])
					searchStart = i + 1
					goto nextCall
				}
			}
		}

		return blocks

	nextCall:
	}
}

// PowerShellParser parses PowerShell scripts
type PowerShellParser struct{}

func (p *PowerShellParser) CanParse(filePath string) bool {
	// LEGACY: We no longer parse PowerShell files directly.
	return false
}

// Parse extracts tool info from PowerShell comments
// Convention:
// # @tool <name>
// # @description <description>
// # @param <name> <type> <description>
func (p *PowerShellParser) Parse(filePath string) (*SkillDefinition, error) {
	return nil, fmt.Errorf("direct powershell parsing is deprecated, use SKILL.md")
}

func jsonTypeFromPS(psType string) string {
	// Simple mapping
	return "string"
}

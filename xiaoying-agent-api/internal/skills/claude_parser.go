package skills

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ClaudeSkillParser parses Claude Code style SKILL.md files
type ClaudeSkillParser struct{}

func (p *ClaudeSkillParser) CanParse(filePath string) bool {
	return strings.EqualFold(filepath.Base(filePath), "SKILL.md")
}

type SkillFrontmatter struct {
	Name            string                      `yaml:"name"`
	Description     string                      `yaml:"description"`
	Language        string                      `yaml:"language"`
	ScriptPath      string                      `yaml:"script_path"`
	CallFormat      string                      `yaml:"call_format"`
	CommandTemplate string                      `yaml:"command_template"`
	EnvMapping      map[string]string           `yaml:"env_mapping"`
	Parameters      map[string]SkillParameterFM `yaml:"parameters"`
}

type SkillParameterFM struct {
	Type        string `yaml:"type"`
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
}

func (p *ClaudeSkillParser) Parse(filePath string) (*SkillDefinition, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	// Split Frontmatter and Content
	// Frontmatter is between the first two "---" lines
	var frontmatterData []byte
	var markdownContent []byte

	// Remove UTF-8 BOM if present
	content = bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))

	// Normalize line endings to LF for consistent splitting
	content = bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))

	parts := bytes.SplitN(content, []byte("\n---\n"), 2)
	if len(parts) == 2 && bytes.HasPrefix(content, []byte("---")) {
		// Found valid frontmatter structure
		// Remove the first "---"
		frontmatterData = bytes.TrimPrefix(parts[0], []byte("---\n"))
		frontmatterData = bytes.TrimPrefix(frontmatterData, []byte("---"))
		markdownContent = parts[1]
	} else {
		// No frontmatter found
		// Validation: If the file is named "SKILL.md", it MUST have frontmatter.
		if strings.EqualFold(filepath.Base(filePath), "SKILL.md") {
			return nil, fmt.Errorf("invalid SKILL.md format: missing frontmatter (--- ... ---) in %s", filePath)
		}

		// No frontmatter, treat whole file as content
		markdownContent = content
	}

	var fm SkillFrontmatter
	if len(frontmatterData) > 0 {
		if err := yaml.Unmarshal(frontmatterData, &fm); err != nil {
			// If parsing fails, just log/ignore and proceed with defaults
			fmt.Printf("Warning: Failed to parse frontmatter for %s: %v\n", filePath, err)
		}
	}

	// Determine Name
	name := fm.Name
	if name == "" {
		// Use parent directory name as skill name
		dir := filepath.Dir(filePath)
		name = filepath.Base(dir)
		// If directly in skills root (unlikely for SKILL.md structure but possible), use "skill"
		if name == "." || name == string(filepath.Separator) {
			name = "unnamed_skill"
		}
	}

	// Determine Description
	description := fm.Description
	if description == "" {
		// Use first non-empty line of markdown as description (truncated)
		lines := strings.Split(string(markdownContent), "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				description = trimmed
				if len(description) > 100 {
					description = description[:97] + "..."
				}
				break
			}
		}
		if description == "" {
			description = "No description provided"
		}
	}

	language := "markdown"
	if fm.Language != "" {
		language = fm.Language
	}

	// Declare params and required early so they can be used in the document-only block
	var params map[string]any = map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	var required []string = []string{}

	// Prefer parameters defined in Frontmatter
	if len(fm.Parameters) > 0 {
		props := make(map[string]any)
		for key, val := range fm.Parameters {
			props[key] = map[string]any{
				"type":        val.Type,
				"description": val.Description,
			}
			if val.Required {
				required = append(required, key)
			}
		}
		params["properties"] = props
	}

	if fm.CallFormat == "document" || fm.ScriptPath == "" {
		// Document-only skill (Agent-Native operation mode)
		// We don't need a script_path. We just provide the markdown content to the agent.
		return &SkillDefinition{
			Name:            name,
			Description:     description,
			Parameters:      params,
			Required:        required,
			ScriptPath:      "",
			Language:        "markdown",
			Content:         string(markdownContent),
			ArgStyles:       nil,
			CallFormat:      "document",
			CommandTemplate: fm.CommandTemplate,
			EnvMapping:      fm.EnvMapping,
		}, nil
	}

	// Validate script file for executable skills
	scriptPath := filePath
	if fm.ScriptPath != "" {
		// Resolve relative path
		if !filepath.IsAbs(fm.ScriptPath) {
			dir := filepath.Dir(filePath)
			scriptPath = filepath.Join(dir, fm.ScriptPath)
		} else {
			scriptPath = fm.ScriptPath
		}
	}

	// Fallback for legacy scripts: scan argparse definitions if no parameters in frontmatter
	if len(fm.Parameters) == 0 && (language == "python" || strings.HasSuffix(scriptPath, ".py")) {
		if _, err := os.Stat(scriptPath); err == nil {
			argParams, argRequired := scanArgparseDefinitions(scriptPath)
			if len(argParams) > 0 {
				params = argParams
				required = argRequired
			}
		}
	}

	argStyles := detectArgStyles(scriptPath)

	return &SkillDefinition{
		Name:            name,
		Description:     description,
		Parameters:      params,
		Required:        required,
		ScriptPath:      scriptPath,
		Language:        language,
		Content:         string(markdownContent),
		ArgStyles:       argStyles,
		CallFormat:      fm.CallFormat,
		CommandTemplate: fm.CommandTemplate,
		EnvMapping:      fm.EnvMapping,
	}, nil
}

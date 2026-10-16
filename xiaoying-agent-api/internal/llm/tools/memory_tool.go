package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/auramate/auramate/internal/memory"
)

const (
	MemoryToolName    = "manage_core_memory"
	memoryDescription = `Manages long-term memory (add/update/delete/list/search/link).
Usage:
- action: add/update/delete/list/search/link.
- content: Memory text.
- category: habit/preference/instruction/experience/fact.
- link: Use 'link' action to connect two memories. Requires 'id' (source) and 'target_id'.

Call this tool immediately when you detect:
- stable user preferences or defaults,
- explicit standing instructions,
- reusable project conventions,
- lessons from a failed attempt worth remembering.

Do NOT use it for temporary session-only details or raw logs.
User-provided secrets, credentials, codes, tokens, and IDs may be stored when they are durable facts that the agent will likely need again.
`
)

type MemoryToolParams struct {
	Action      string `json:"action"` // add, update, delete, list, search, link
	ID          string `json:"id,omitempty"`
	TargetID    string `json:"target_id,omitempty"` // For link action
	Category    string `json:"category,omitempty"`  // habit, preference, instruction, fact
	Content     string `json:"content,omitempty"`
	Source      string `json:"source,omitempty"`
	Query       string `json:"query,omitempty"`
	Keywords    string `json:"keywords,omitempty"`
	Title       string `json:"title,omitempty"`
	Scope       string `json:"scope,omitempty"`
	Relation    string `json:"relation,omitempty"`     // For link action (e.g. "related_to")
	TargetTopic string `json:"target_topic,omitempty"` // Auto-link helper
	Force       bool   `json:"force,omitempty"`
}

type memoryTool struct {
	memories memory.Service
}

func NewMemoryTool(memories memory.Service) BaseTool {
	return &memoryTool{
		memories: memories,
	}
}

func (t *memoryTool) Info() ToolInfo {
	return ToolInfo{
		Name:        MemoryToolName,
		Description: memoryDescription,
		Parameters: map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "The action to perform",
				"enum":        []string{"add", "update", "delete", "list", "search", "link"},
			},
			"id": map[string]any{
				"type":        "string",
				"description": "Memory ID (required for update/delete/link source)",
			},
			"target_id": map[string]any{
				"type":        "string",
				"description": "Target Memory ID (required for link)",
			},
			"category": map[string]any{
				"type":        "string",
				"description": "Memory category: 'habit' (L1: persistent user habits), 'preference' (L1: user likes/dislikes), 'instruction' (L2: explicit rules), 'fact' (L3: knowledge), 'experience' (L2: learned pitfalls)",
				"enum":        []string{"habit", "preference", "instruction", "fact", "experience"},
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The main content of the memory",
			},
			"source": map[string]any{
				"type":        "string",
				"description": "Source of the memory (e.g., 'user_input', 'inference')",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query for semantic search",
			},
			"keywords": map[string]any{
				"type":        "string",
				"description": "Keywords for the memory (optional)",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "Title of the memory (optional)",
			},
			"scope": map[string]any{
				"type":        "string",
				"description": "Scope: 'user' or 'project' (optional)",
			},
			"relation": map[string]any{
				"type":        "string",
				"description": "Relation type for link (e.g. 'related_to', 'supports', 'requires'). Default: 'related_to'",
			},
			"target_topic": map[string]any{
				"type":        "string",
				"description": "When adding a new memory, you can specify an existing topic it relates to. The system will auto-link them.",
			},
			"force": map[string]any{
				"type":        "boolean",
				"description": "Set to true to bypass conflict detection when adding a new memory",
			},
		},
		Required: []string{"action"},
	}
}

func (t *memoryTool) formatContent(params MemoryToolParams) string {
	content := params.Content
	// Prepend Title and Scope
	prefix := ""
	if params.Scope != "" {
		prefix += fmt.Sprintf("[Scope: %s] ", params.Scope)
	}
	if params.Title != "" {
		prefix += fmt.Sprintf("**%s**\n", params.Title)
	}

	finalContent := prefix + content

	// Append Keywords
	if params.Keywords != "" {
		finalContent += fmt.Sprintf("\n(Keywords: %s)", params.Keywords)
	}
	return finalContent
}

func allowMemoryForceOverride(source string) bool {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "user_input", "user", "api_user":
		return true
	default:
		return false
	}
}

func (t *memoryTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params MemoryToolParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("invalid parameters: %s", err)), nil
	}

	switch params.Action {
	case "add":
		if params.Category == "" || params.Content == "" {
			return NewTextErrorResponse("category and content are required for 'add'"), nil
		}
		source := params.Source
		if source == "" {
			source = "inference"
		}

		// Enrich content with metadata
		enrichedContent := t.formatContent(params)

		writeReq := memory.DefaultWriteRequest(params.Category, enrichedContent, source)
		writeReq.Trigger = "manage_core_memory:add"
		writeReq.AllowOverwrite = params.Force && allowMemoryForceOverride(source)
		if params.Title != "" {
			writeReq.Evidence = append(writeReq.Evidence, params.Title)
		}
		writeResult, err := t.memories.AddWithPolicy(ctx, writeReq)
		if err != nil {
			return NewTextErrorResponse(fmt.Sprintf("failed to add memory: %s", err)), nil
		}
		m := writeResult.Memory

		// Phase 2 Optimization: Support auto-linking on creation
		if params.TargetTopic != "" && m != nil {
			relation := "related_to"
			if params.Relation != "" {
				relation = params.Relation
			}
			// Search for the target topic
			targets, searchErr := t.memories.Search(ctx, params.TargetTopic, 1)
			if searchErr == nil && len(targets) > 0 {
				targetMem := targets[0]
				if targetMem.ID != m.ID {
					_ = t.memories.Link(ctx, m.ID, targetMem.ID, relation, 0.9)
				}
			}
		}

		switch writeResult.Decision.Action {
		case memory.MemoryWriteActionReusedExisting:
			return NewTextResponse(fmt.Sprintf("Memory reused existing ID: %s\nreason_codes=%v", m.ID, writeResult.Decision.ReasonCodes)), nil
		case memory.MemoryWriteActionOverwroteExisting:
			return NewTextResponse(fmt.Sprintf("Memory overwrote existing ID: %s\nreason_codes=%v", m.ID, writeResult.Decision.ReasonCodes)), nil
		case memory.MemoryWriteActionRejectedConflict:
			return NewTextResponse(fmt.Sprintf("Memory conflict rejected; kept existing ID: %s\nreason_codes=%v", m.ID, writeResult.Decision.ReasonCodes)), nil
		}
		return NewTextResponse(fmt.Sprintf("Memory added with ID: %s\nreason_codes=%v", m.ID, writeResult.Decision.ReasonCodes)), nil

	case "update":
		if params.ID == "" || params.Category == "" || params.Content == "" {
			return NewTextErrorResponse("id, category, and content are required for 'update'"), nil
		}
		source := params.Source
		if source == "" {
			source = "inference"
		}

		// Enrich content with metadata
		enrichedContent := t.formatContent(params)

		_, err := t.memories.Update(ctx, params.ID, params.Category, enrichedContent, source)
		if err != nil {
			return NewTextErrorResponse(fmt.Sprintf("failed to update memory: %s", err)), nil
		}
		return NewTextResponse("Memory updated successfully"), nil

	case "delete":
		if params.ID == "" {
			return NewTextErrorResponse("id is required for 'delete'"), nil
		}
		err := t.memories.Delete(ctx, params.ID)
		if err != nil {
			return NewTextErrorResponse(fmt.Sprintf("failed to delete memory: %s", err)), nil
		}
		return NewTextResponse("Memory deleted successfully"), nil

	case "list":
		var list []string
		var err error

		rawMems, err := t.memories.List(ctx)
		if params.Category != "" {
			rawMems, err = t.memories.ListByCategory(ctx, params.Category)
		}

		if err != nil {
			return NewTextErrorResponse(fmt.Sprintf("failed to list memories: %s", err)), nil
		}

		for _, m := range rawMems {
			list = append(list, fmt.Sprintf("[%s] %s (ID: %s)", m.Category, m.Content, m.ID))
		}

		if len(list) == 0 {
			return NewTextResponse("No memories found."), nil
		}
		return NewTextResponse(strings.Join(list, "\n")), nil

	case "search":
		if params.Query == "" {
			return NewTextErrorResponse("query is required for 'search'"), nil
		}

		searchResult, err := t.memories.SearchWithPolicy(ctx, memory.DefaultSearchRequest(params.Query, 10))
		if params.Category != "" {
			searchReq := memory.DefaultCategorySearchRequest(params.Query, params.Category, 10)
			searchReq.Stage = "tool_search"
			searchResult, err = t.memories.SearchByCategoryWithPolicy(ctx, searchReq)
		}
		if err != nil {
			return NewTextErrorResponse(fmt.Sprintf("failed to search memories: %s", err)), nil
		}
		rawMems := searchResult.Memories

		var list []string
		for _, m := range rawMems {
			list = append(list, fmt.Sprintf("[%s] %s (ID: %s)", m.Category, m.Content, m.ID))
		}

		if len(list) == 0 {
			return NewTextResponse("No matching memories found."), nil
		}
		list = append(list, fmt.Sprintf("decision: stage=%s channels=%v fallback=%s reasons=%v", searchResult.Decision.Stage, searchResult.Decision.UsedChannels, searchResult.Decision.FallbackPath, searchResult.Decision.ReasonCodes))
		return NewTextResponse(strings.Join(list, "\n")), nil

	case "link":
		if params.ID == "" || params.TargetID == "" {
			return NewTextErrorResponse("id (source) and target_id are required for 'link'"), nil
		}
		relation := "related_to"
		if params.Relation != "" {
			relation = params.Relation
		}

		if err := t.memories.Link(ctx, params.ID, params.TargetID, relation, 1.0); err != nil {
			return NewTextErrorResponse(fmt.Sprintf("failed to link memories: %s", err)), nil
		}
		return NewTextResponse(fmt.Sprintf("Linked memory %s to %s with relation '%s'", params.ID, params.TargetID, relation)), nil

	default:
		return NewTextErrorResponse(fmt.Sprintf("unknown action: %s", params.Action)), nil
	}
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/memory"
	"github.com/gofrs/flock"
	"github.com/google/uuid"
)

const (
	ManagePlanToolName    = "manage_plan"
	managePlanDescription = `管理当前项目的高级任务计划。
用法：
- action: 'add', 'list', 'complete', 'remove', 'update', 'archive'。
- item: 计划项内容（add 操作必需）。
- index: 要完成/移除/更新的项的索引（从 1 开始）。兼容旧调用。
- id: 要完成/移除/更新的项 ID。优先使用 list 输出里的 ID，比 index 更稳定。
- status: 'update' 操作的新状态 ('pending', 'in_progress', 'completed') 或 'list' 操作的过滤器。
- filename: (可选) 计划文件的名称（默认为 'TODO.md'）。
- depends_on: (可选) 此新任务依赖的任务索引列表（例如 [1, 2]）。
- due_date: (可选) 任务截止日期 (YYYY-MM-DD)。
- priority: (可选) 新任务的优先级 ('high', 'medium', 'low')。

'archive' 操作会将所有已完成的任务移动到归档文件（例如 'TODO_ARCHIVE.md'）。任务标记为 'completed' 后会保留在当前列表中，直到显式执行 'archive'。

重要提示：
1. 计划以结构化格式 (JSON) 存储，但为了可读性会镜像到 'TODO.md'。
2. 使用此工具来规划复杂任务并跟踪进度。
`
)

type ManagePlanParams struct {
	Action    string   `json:"action"`
	Item      string   `json:"item,omitempty"`
	Index     int      `json:"index,omitempty"`
	ID        string   `json:"id,omitempty"`
	Status    string   `json:"status,omitempty"`     // For 'list' (filter) or 'update' (new status)
	FileName  string   `json:"filename,omitempty"`   // For multi-file support
	DependsOn []int    `json:"depends_on,omitempty"` // For 'add' (indices of tasks this depends on)
	DueDate   string   `json:"due_date,omitempty"`   // For 'add' (YYYY-MM-DD)
	Priority  string   `json:"priority,omitempty"`   // For 'add' (high, medium, low)
	Tags      []string `json:"tags,omitempty"`       // For 'add' (list of tags)
	Search    string   `json:"search,omitempty"`     // For 'list' (keyword search)
	SortBy    string   `json:"sort_by,omitempty"`    // For 'list' (priority)
}

// PlanEntry represents a single task in structured storage
type PlanEntry struct {
	ID           string   `json:"id,omitempty"`
	Item         string   `json:"item"`
	Status       string   `json:"status"` // pending, in_progress, completed
	Priority     string   `json:"priority,omitempty"`
	DueDate      string   `json:"due_date,omitempty"`
	DependsOn    []int    `json:"depends_on,omitempty"`
	DependsOnIDs []string `json:"depends_on_ids,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

type archivedPlanEntry struct {
	PlanEntry
	ArchivedAt int64 `json:"archived_at"`
}

type planSnapshotItem struct {
	ID           string   `json:"id"`
	DisplayID    string   `json:"display_id"`
	Index        int      `json:"index,omitempty"`
	Title        string   `json:"title"`
	Status       string   `json:"status"`
	Priority     string   `json:"priority,omitempty"`
	DueDate      string   `json:"due_date,omitempty"`
	DependsOnIDs []string `json:"depends_on_ids,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

type planSnapshotMetadata struct {
	Type           string             `json:"type"`
	Action         string             `json:"action"`
	BaseName       string             `json:"base_name"`
	FileName       string             `json:"file_name"`
	Revision       int64              `json:"revision"`
	Items          []planSnapshotItem `json:"items"`
	ViewItems      []planSnapshotItem `json:"view_items,omitempty"`
	IsFullSnapshot bool               `json:"is_full_snapshot"`
	Affected       *planSnapshotItem  `json:"affected,omitempty"`
	Removed        *planSnapshotItem  `json:"removed,omitempty"`
	Archived       []planSnapshotItem `json:"archived,omitempty"`
	ArchivedCount  int                `json:"archived_count,omitempty"`
	Empty          bool               `json:"empty,omitempty"`
	Error          string             `json:"error,omitempty"`
}

type planMarkdownMetadata struct {
	ID           string
	Priority     string
	DueDate      string
	DependsOnIDs []string
	Tags         []string
}

const activePlanStateKeyPrefix = "active_plan:"

type managePlanTool struct {
	memories memory.Service
	mu       sync.Mutex
}

func NewManagePlanTool(memories memory.Service) BaseTool {
	return &managePlanTool{
		memories: memories,
	}
}

func planSnapshotItemFromEntry(entry PlanEntry, index int) planSnapshotItem {
	return planSnapshotItem{
		ID:           strings.TrimSpace(entry.ID),
		DisplayID:    planIDDisplay(entry.ID),
		Index:        index,
		Title:        strings.TrimSpace(entry.Item),
		Status:       normalizePlanStatus(entry.Status),
		Priority:     strings.ToLower(strings.TrimSpace(entry.Priority)),
		DueDate:      strings.TrimSpace(entry.DueDate),
		DependsOnIDs: append([]string(nil), entry.DependsOnIDs...),
		Tags:         append([]string(nil), entry.Tags...),
	}
}

func planSnapshotItemsFromEntries(plans []PlanEntry) []planSnapshotItem {
	items := make([]planSnapshotItem, 0, len(plans))
	for idx, entry := range plans {
		items = append(items, planSnapshotItemFromEntry(entry, idx+1))
	}
	return items
}

func completedPlanEntries(plans []PlanEntry) []PlanEntry {
	completed := make([]PlanEntry, 0)
	for _, entry := range plans {
		if normalizePlanStatus(entry.Status) == "completed" {
			completed = append(completed, entry)
		}
	}
	return completed
}

func hasActivePlanEntries(plans []PlanEntry) bool {
	for _, entry := range plans {
		if normalizePlanStatus(entry.Status) != "completed" {
			return true
		}
	}
	return false
}

func withPlanSnapshotMetadata(response ToolResponse, action, baseName string, revision int64, items []PlanEntry, affected, removed *PlanEntry, archived []PlanEntry, archivedCount int, errText string) ToolResponse {
	return withPlanSnapshotMetadataView(response, action, baseName, revision, items, nil, affected, removed, archived, archivedCount, errText)
}

func withPlanSnapshotMetadataView(response ToolResponse, action, baseName string, revision int64, items, viewItems []PlanEntry, affected, removed *PlanEntry, archived []PlanEntry, archivedCount int, errText string) ToolResponse {
	snapshotItems := planSnapshotItemsFromEntries(items)
	meta := planSnapshotMetadata{
		Type:           "plan_snapshot",
		Action:         strings.TrimSpace(action),
		BaseName:       sanitizePlanBaseName(baseName),
		FileName:       sanitizePlanBaseName(baseName) + ".md",
		Revision:       revision,
		Items:          snapshotItems,
		IsFullSnapshot: true,
		Archived:       planSnapshotItemsFromEntries(archived),
		ArchivedCount:  archivedCount,
		Empty:          len(items) == 0,
		Error:          strings.TrimSpace(errText),
	}
	if viewItems != nil {
		meta.ViewItems = planSnapshotItemsFromEntries(viewItems)
	}
	if affected != nil {
		item := planSnapshotItemFromEntry(*affected, 0)
		meta.Affected = &item
	}
	if removed != nil {
		item := planSnapshotItemFromEntry(*removed, 0)
		meta.Removed = &item
	}
	return WithResponseMetadata(response, meta)
}

func (t *managePlanTool) Info() ToolInfo {
	return ToolInfo{
		Name:        ManagePlanToolName,
		Description: managePlanDescription,
		Parameters: map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"add", "list", "complete", "remove", "update", "archive"},
				"description": "要执行的操作。",
			},
			"item": map[string]any{
				"type":        "string",
				"description": "计划项的内容（用于 'add'）。",
			},
			"index": map[string]any{
				"type":        "integer",
				"description": "项的索引（用于 'complete', 'remove' 或 'update'）。兼容旧调用。",
			},
			"id": map[string]any{
				"type":        "string",
				"description": "项 ID（用于 'complete', 'remove' 或 'update'）。优先于 index，支持唯一前缀。",
			},
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"pending", "in_progress", "completed", "all"},
				"description": "'list' 操作的过滤器（默认为 'all'）或 'update' 操作的新状态 ('pending', 'in_progress', 'completed')。",
			},
			"filename": map[string]any{
				"type":        "string",
				"description": "计划列表的可选文件名（默认为 'TODO.md'）。用于多项目或分类任务。",
			},
			"priority": map[string]any{
				"type":        "string",
				"enum":        []string{"high", "medium", "low"},
				"description": "新任务的优先级（用于 'add' 操作）。",
			},
			"depends_on": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "integer",
				},
				"description": "此新任务依赖的任务索引列表（用于 'add' 操作）。",
			},
			"due_date": map[string]any{
				"type":        "string",
				"description": "任务截止日期 (YYYY-MM-DD)（用于 'add' 操作）。",
			},
			"tags": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "标签列表（用于 'add'）或按标签过滤（用于 'list'）。",
			},
			"search": map[string]any{
				"type":        "string",
				"description": "在任务项中搜索的关键字（用于 'list'）。",
			},
			"sort_by": map[string]any{
				"type":        "string",
				"enum":        []string{"priority", "due_date"},
				"description": "'list' 操作的排序顺序。",
			},
		},
		Required: []string{"action"},
	}
}

func (t *managePlanTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	// Concurrency Safety: Lock the tool execution (In-process)
	t.mu.Lock()
	defer t.mu.Unlock()

	var params ManagePlanParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("无效的 JSON: %v", err)), nil
	}

	workspace := config.WorkingDirectory()
	baseName := resolvePlanBaseName(ctx, params.FileName)

	jsonFile := filepath.Join(workspace, baseName+".json")
	mdFile := filepath.Join(workspace, baseName+".md")

	// Lock on the base name to cover both files
	lockFile := filepath.Join(workspace, baseName+".lock")

	// File Lock (Cross-process) with Smart Retry
	fileLock := flock.New(lockFile)
	// Retry 25 times, wait 200ms (Total 5s) to handle Windows file locking contention from concurrent parallel requests
	locked, err := acquireLockWithRetry(ctx, fileLock, 25, 200*time.Millisecond)
	if err != nil {
		return NewTextErrorResponse(fmt.Sprintf("无法获取文件锁 %s: %v", baseName, err)), nil
	}
	if !locked {
		return NewTextErrorResponse(fmt.Sprintf("无法获取文件锁 %s (重试后资源仍忙)", baseName)), nil
	}
	defer fileLock.Unlock()

	// LOAD DATA (JSON or Migrate from MD)
	plans, err := loadPlanData(jsonFile, mdFile)
	if err != nil {
		return NewTextErrorResponse(fmt.Sprintf("无法加载计划: %v", err)), nil
	}

	// Helper to save and sync
	saveAndSync := func(plans []PlanEntry) error {
		if err := savePlanData(jsonFile, mdFile, plans); err != nil {
			return err
		}
		return nil
	}

	// Check if migration happened (Data loaded but JSON missing)
	if _, err := os.Stat(jsonFile); os.IsNotExist(err) && len(plans) > 0 {
		// Persist the migrated data immediately
		if err := saveAndSync(plans); err != nil {
			// Log error but continue (don't block read)
			fmt.Printf("无法持久化迁移数据: %v\n", err)
		}
	}

	rememberOnSuccess := func(itemCount int) {
		if shouldRememberPlanBaseName(ctx, params, itemCount) {
			rememberActivePlanFile(ctx, baseName)
		}
	}
	withMeta := func(resp ToolResponse, action string, snapshot []PlanEntry, affected, removed *PlanEntry, archived []PlanEntry, archivedCount int, errText string) ToolResponse {
		return withPlanSnapshotMetadata(resp, action, baseName, nextPlanRevision(workspace, baseName), snapshot, affected, removed, archived, archivedCount, errText)
	}
	withMetaView := func(resp ToolResponse, action string, snapshot, view []PlanEntry, affected, removed *PlanEntry, archived []PlanEntry, archivedCount int, errText string) ToolResponse {
		return withPlanSnapshotMetadataView(resp, action, baseName, nextPlanRevision(workspace, baseName), snapshot, view, affected, removed, archived, archivedCount, errText)
	}

	switch params.Action {
	case "add":
		if params.Item == "" {
			msg := "'add' 操作必须提供 item"
			return withMeta(NewTextErrorResponse(msg), "add", plans, nil, nil, nil, 0, msg), nil
		}

		// Validate dependencies
		if len(params.DependsOn) > 0 {
			for _, depIndex := range params.DependsOn {
				if depIndex < 1 || depIndex > len(plans) {
					msg := fmt.Sprintf("无效的依赖索引: %d. 任务列表有 %d 项。", depIndex, len(plans))
					return withMeta(NewTextErrorResponse(msg), "add", plans, nil, nil, nil, 0, msg), nil
				}
			}
		}

		// Validate due date
		if params.DueDate != "" {
			if _, err := time.Parse("2006-01-02", params.DueDate); err != nil {
				msg := "无效的 due_date 格式。请使用 YYYY-MM-DD (例如 2025-02-20)。"
				return withMeta(NewTextErrorResponse(msg), "add", plans, nil, nil, nil, 0, msg), nil
			}
		}

		newEntry := PlanEntry{
			ID:           newPlanEntryID(),
			Item:         params.Item,
			Status:       "pending",
			Priority:     strings.ToLower(params.Priority),
			DueDate:      params.DueDate,
			DependsOnIDs: resolveDependencyIDs(plans, params.DependsOn),
			Tags:         params.Tags,
		}

		plans = append(plans, newEntry)
		if err := saveAndSync(plans); err != nil {
			msg := fmt.Sprintf("保存失败: %v", err)
			return withMeta(NewTextErrorResponse(msg), "add", plans, &newEntry, nil, nil, 0, msg), nil
		}
		rememberOnSuccess(len(plans))
		return withMeta(NewTextResponse(fmt.Sprintf("已添加: %s (ID: %s%s)", params.Item, planIDDisplay(newEntry.ID), formatPlanBaseNameHint(baseName))), "add", plans, &newEntry, nil, nil, 0, ""), nil

	case "list":
		if len(plans) == 0 {
			return withMeta(NewTextResponse("计划列表为空。"), "list", plans, nil, nil, nil, 0, ""), nil
		}

		type PlanView struct {
			OriginalIndex int
			Entry         PlanEntry
		}

		var filtered []PlanView
		for i, entry := range plans {
			match := false
			if params.Status == "" || params.Status == "all" {
				match = true
			} else if params.Status == entry.Status {
				match = true
			}

			// Filter by Search (Case-insensitive)
			if match && params.Search != "" {
				if !strings.Contains(strings.ToLower(entry.Item), strings.ToLower(params.Search)) {
					match = false
				}
			}

			// Filter by Tags (AND logic: must contain ALL requested tags)
			if match && len(params.Tags) > 0 {
				entryTags := make(map[string]bool)
				for _, t := range entry.Tags {
					entryTags[strings.ToLower(t)] = true
				}
				for _, reqTag := range params.Tags {
					if !entryTags[strings.ToLower(reqTag)] {
						match = false
						break
					}
				}
			}

			if match {
				filtered = append(filtered, PlanView{OriginalIndex: i + 1, Entry: entry})
			}
		}

		if len(filtered) == 0 {
			if params.Status != "" && params.Status != "all" {
				rememberOnSuccess(len(plans))
				return withMetaView(NewTextResponse(fmt.Sprintf("没有状态为 '%s' 的任务。", params.Status)), "list", plans, []PlanEntry{}, nil, nil, nil, 0, ""), nil
			}
			rememberOnSuccess(len(plans))
			return withMetaView(NewTextResponse("计划列表为空。"), "list", plans, []PlanEntry{}, nil, nil, nil, 0, ""), nil
		}

		// Sorting
		if params.SortBy == "priority" {
			sort.SliceStable(filtered, func(i, j int) bool {
				return getPriorityVal(filtered[i].Entry.Priority) > getPriorityVal(filtered[j].Entry.Priority)
			})
		} else if params.SortBy == "due_date" {
			sort.SliceStable(filtered, func(i, j int) bool {
				di, hasDi := parseDate(filtered[i].Entry.DueDate)
				dj, hasDj := parseDate(filtered[j].Entry.DueDate)

				if hasDi && !hasDj {
					return true
				}
				if !hasDi && hasDj {
					return false
				}
				if hasDi && hasDj {
					return di.Before(dj)
				}
				return filtered[i].OriginalIndex < filtered[j].OriginalIndex
			})
		}

		// Format Output
		var output []string
		filteredPlans := make([]PlanEntry, 0, len(filtered))
		now := time.Now().Truncate(24 * time.Hour)

		for _, view := range filtered {
			entry := view.Entry
			filteredPlans = append(filteredPlans, entry)
			mark := "[ ]"
			if entry.Status == "completed" {
				mark = "[x]"
			} else if entry.Status == "in_progress" {
				mark = "[-]"
			}

			line := fmt.Sprintf("- %s %s", mark, entry.Item)

			// Add Metadata to display
			if entry.Priority != "" {
				line += fmt.Sprintf(" [%s]", strings.Title(entry.Priority))
			}

			if entry.DueDate != "" {
				line += fmt.Sprintf(" due_date: %s", entry.DueDate)
				if d, ok := parseDate(entry.DueDate); ok && d.Before(now) && entry.Status != "completed" {
					line += " **[已过期]**"
				}
			}

			if len(entry.DependsOnIDs) > 0 {
				line += fmt.Sprintf(" depends_on_ids: [%s]", strings.Join(shortDependencyIDs(entry.DependsOnIDs), ", "))
			}

			if len(entry.Tags) > 0 {
				for _, t := range entry.Tags {
					line += fmt.Sprintf(" #%s", t)
				}
			}

			output = append(output, fmt.Sprintf("[ID: %s] [索引: %d] %s", planIDDisplay(entry.ID), view.OriginalIndex, line))
		}

		rememberOnSuccess(len(filteredPlans))
		return withMetaView(NewTextResponse(fmt.Sprintf("%s 中的任务:\n```markdown\n%s\n```", baseName+".md", strings.Join(output, "\n"))), "list", plans, filteredPlans, nil, nil, nil, 0, ""), nil

	case "complete":
		if params.Index < 1 || params.Index > len(plans) {
			if strings.TrimSpace(params.ID) == "" {
				msg := fmt.Sprintf("无效的索引: %d", params.Index)
				return withMeta(NewTextErrorResponse(msg), "complete", plans, nil, nil, nil, 0, msg), nil
			}
		}

		idx, entry, err := resolvePlanTarget(plans, params)
		if err != nil {
			msg := err.Error()
			return withMeta(NewTextErrorResponse(msg), "complete", plans, nil, nil, nil, 0, msg), nil
		}

		// Check dependencies
		if len(entry.DependsOnIDs) > 0 {
			var pendingDeps []string
			for _, depID := range entry.DependsOnIDs {
				depIdx := findPlanIndexByID(plans, depID)
				if depIdx >= 0 && plans[depIdx].Status != "completed" {
					pendingDeps = append(pendingDeps, planIDDisplay(plans[depIdx].ID))
				}
			}
			if len(pendingDeps) > 0 {
				msg := fmt.Sprintf("无法完成任务 %s。它依赖于未完成的任务: %v。请先完成它们。", planTargetDisplay(entry, params), pendingDeps)
				return withMeta(NewTextErrorResponse(msg), "complete", plans, &entry, nil, nil, 0, msg), nil
			}
		}

		plans[idx].Status = "completed"
		affectedEntry := plans[idx]
		completedItemText := plans[idx].Item

		if err := saveAndSync(plans); err != nil {
			msg := fmt.Sprintf("保存失败: %v", err)
			return withMeta(NewTextErrorResponse(msg), "complete", plans, &affectedEntry, nil, nil, 0, msg), nil
		}
		rememberOnSuccess(len(plans))

		msg := fmt.Sprintf("已完成: %s", completedItemText)
		if !hasActivePlanEntries(plans) {
			msg += " 当前没有未完成任务；如果用户目标已经完成，请执行 archive 收尾。"
		}
		return withMeta(NewTextResponse(msg), "complete", plans, &affectedEntry, nil, nil, 0, ""), nil

	case "update":
		if params.Status != "pending" && params.Status != "in_progress" && params.Status != "completed" {
			msg := "无效的状态。请使用 'pending', 'in_progress', 或 'completed'。"
			return withMeta(NewTextErrorResponse(msg), "update", plans, nil, nil, nil, 0, msg), nil
		}
		idx, entry, err := resolvePlanTarget(plans, params)
		if err != nil {
			msg := err.Error()
			return withMeta(NewTextErrorResponse(msg), "update", plans, nil, nil, nil, 0, msg), nil
		}

		plans[idx].Status = params.Status
		affectedEntry := plans[idx]

		if err := saveAndSync(plans); err != nil {
			msg := fmt.Sprintf("保存失败: %v", err)
			return withMeta(NewTextErrorResponse(msg), "update", plans, &affectedEntry, nil, nil, 0, msg), nil
		}
		rememberOnSuccess(len(plans))

		msg := fmt.Sprintf("已更新项 %s 的状态为 %s", planTargetDisplay(entry, params), params.Status)
		if params.Status == "completed" && !hasActivePlanEntries(plans) {
			msg += " 当前没有未完成任务；如果用户目标已经完成，请执行 archive 收尾。"
		}
		return withMeta(NewTextResponse(msg), "update", plans, &affectedEntry, nil, nil, 0, ""), nil

	case "remove":
		idx, entry, err := resolvePlanTarget(plans, params)
		if err != nil {
			msg := err.Error()
			return withMeta(NewTextErrorResponse(msg), "remove", plans, nil, nil, nil, 0, msg), nil
		}
		if dependents := findDependentPlanTargets(plans, entry.ID); len(dependents) > 0 {
			msg := fmt.Sprintf("无法移除任务 %s。仍有未完成任务依赖它: %s。", planTargetDisplay(entry, params), strings.Join(dependents, ", "))
			return withMeta(NewTextErrorResponse(msg), "remove", plans, &entry, nil, nil, 0, msg), nil
		}
		removed := entry.Item
		plans = append(plans[:idx], plans[idx+1:]...)

		if err := saveAndSync(plans); err != nil {
			msg := fmt.Sprintf("保存失败: %v", err)
			return withMeta(NewTextErrorResponse(msg), "remove", plans, nil, &entry, nil, 0, msg), nil
		}
		rememberOnSuccess(len(plans))
		return withMeta(NewTextResponse(fmt.Sprintf("已移除: %s", removed)), "remove", plans, nil, &entry, nil, 0, ""), nil

	case "archive":
		archived := completedPlanEntries(plans)
		archivedCount, newPlans, err := performArchive(workspace, baseName, plans)
		if err != nil {
			msg := fmt.Sprintf("归档失败: %v", err)
			return withMeta(NewTextErrorResponse(msg), "archive", plans, nil, nil, archived, 0, msg), nil
		}
		plans = newPlans
		if err := saveAndSync(plans); err != nil {
			msg := fmt.Sprintf("保存失败: %v", err)
			return withMeta(NewTextErrorResponse(msg), "archive", plans, nil, nil, archived, archivedCount, msg), nil
		}
		rememberOnSuccess(len(plans))
		return withMeta(NewTextResponse(fmt.Sprintf("已归档 %d 个完成的任务。", archivedCount)), "archive", plans, nil, nil, archived, archivedCount, ""), nil

	default:
		msg := "未知操作: " + params.Action
		return withMeta(NewTextErrorResponse(msg), params.Action, plans, nil, nil, nil, 0, msg), nil
	}
}

// Data Handling Functions

func loadPlanData(jsonPath, mdPath string) ([]PlanEntry, error) {
	// 1. Try JSON
	if _, err := os.Stat(jsonPath); err == nil {
		content, err := os.ReadFile(jsonPath)
		if err != nil {
			return nil, err
		}
		var plans []PlanEntry
		if err := json.Unmarshal(content, &plans); err != nil {
			return nil, fmt.Errorf("JSON 文件损坏: %v", err)
		}
		return normalizePlanEntries(plans), nil
	}

	// 2. Try MD (Migration)
	if _, err := os.Stat(mdPath); err == nil {
		return normalizePlanEntriesFromMarkdown(mdPath)
	}

	// 3. New
	return []PlanEntry{}, nil
}

func savePlanData(jsonPath, mdPath string, plans []PlanEntry) error {
	// 1. Write JSON
	data, err := json.MarshalIndent(plans, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(jsonPath, data); err != nil {
		return err
	}

	// 2. Write MD (View)
	var lines []string
	now := time.Now().Truncate(24 * time.Hour)
	for _, entry := range plans {
		mark := "[ ]"
		if entry.Status == "completed" {
			mark = "[x]"
		} else if entry.Status == "in_progress" {
			mark = "[-]"
		}

		line := fmt.Sprintf("- %s %s", mark, entry.Item)
		if id := strings.TrimSpace(entry.ID); id != "" {
			line += fmt.Sprintf(" {id:%s}", id)
		}

		if entry.Priority != "" {
			line += fmt.Sprintf(" [%s]", strings.Title(entry.Priority))
		}
		if entry.DueDate != "" {
			line += fmt.Sprintf(" due_date: %s", entry.DueDate)
			if d, ok := parseDate(entry.DueDate); ok && d.Before(now) && entry.Status != "completed" {
				line += " **[已过期]**"
			}
		}
		if len(entry.DependsOnIDs) > 0 {
			line += fmt.Sprintf(" depends_on_ids: [%s]", strings.Join(entry.DependsOnIDs, ", "))
		}
		if len(entry.Tags) > 0 {
			for _, t := range entry.Tags {
				line += fmt.Sprintf(" #%s", t)
			}
		}
		lines = append(lines, line)
	}

	return atomicWrite(mdPath, []byte(strings.Join(lines, "\n")))
}

func parseMarkdown(path string) ([]PlanEntry, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(content), "\n")
	var plans []PlanEntry

	dueRegex := regexp.MustCompile(`due_date: (\d{4}-\d{2}-\d{2})`)
	depRegex := regexp.MustCompile(`depends_on: \[([\d, ]+)\]`)
	depIDRegex := regexp.MustCompile(`depends_on_ids: \[([A-Za-z0-9, \-]+)\]`)
	tagRegex := regexp.MustCompile(`(?:^|\s)#(\w+)`)
	idRegex := regexp.MustCompile(`\{id:([A-Za-z0-9\-]+)\}`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		entry := PlanEntry{Status: "pending"}

		// Status
		if strings.HasPrefix(line, "- [x]") {
			entry.Status = "completed"
			line = strings.TrimPrefix(line, "- [x]")
		} else if strings.HasPrefix(line, "- [-]") {
			entry.Status = "in_progress"
			line = strings.TrimPrefix(line, "- [-]")
		} else if strings.HasPrefix(line, "- [ ]") {
			entry.Status = "pending"
			line = strings.TrimPrefix(line, "- [ ]")
		} else {
			// Skip malformed lines
			continue
		}
		line = strings.TrimSpace(line)

		// Tags (extract first to keep item clean)
		matches := tagRegex.FindAllStringSubmatch(line, -1)
		for _, m := range matches {
			if len(m) > 1 {
				entry.Tags = append(entry.Tags, m[1])
				line = strings.Replace(line, m[0], "", 1)
			}
		}
		line = strings.TrimSpace(line)

		// Priority
		lowerLine := strings.ToLower(line)
		if matches := idRegex.FindStringSubmatch(line); len(matches) > 1 {
			entry.ID = expandPlanEntryID(matches[1])
			line = strings.Replace(line, matches[0], "", 1)
		}
		line = strings.TrimSpace(line)
		lowerLine = strings.ToLower(line)
		if strings.Contains(lowerLine, "[high]") {
			entry.Priority = "high"
			line = strings.Replace(line, "[High]", "", -1)
			line = strings.Replace(line, "[high]", "", -1)
		} else if strings.Contains(lowerLine, "[medium]") {
			entry.Priority = "medium"
			line = strings.Replace(line, "[Medium]", "", -1)
			line = strings.Replace(line, "[medium]", "", -1)
		} else if strings.Contains(lowerLine, "[low]") {
			entry.Priority = "low"
			line = strings.Replace(line, "[Low]", "", -1)
			line = strings.Replace(line, "[low]", "", -1)
		}

		// Due Date
		if matches := dueRegex.FindStringSubmatch(line); len(matches) > 1 {
			entry.DueDate = matches[1]
			line = strings.Replace(line, matches[0], "", -1)
		}

		// Depends On
		if matches := depRegex.FindStringSubmatch(line); len(matches) > 1 {
			depStr := matches[1]
			depParts := strings.Split(depStr, ",")
			for _, p := range depParts {
				if idx, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
					entry.DependsOn = append(entry.DependsOn, idx)
				}
			}
			line = strings.Replace(line, matches[0], "", -1)
		}
		if matches := depIDRegex.FindStringSubmatch(line); len(matches) > 1 {
			depParts := strings.Split(matches[1], ",")
			for _, part := range depParts {
				if depID := expandPlanEntryID(strings.TrimSpace(part)); depID != "" {
					entry.DependsOnIDs = append(entry.DependsOnIDs, depID)
				}
			}
			line = strings.Replace(line, matches[0], "", -1)
		}

		// Cleanup leftovers
		line = strings.Replace(line, "**[已过期]**", "", -1)
		entry.Item = strings.TrimSpace(line)

		plans = append(plans, entry)
	}
	return plans, nil
}

func normalizePlanEntriesFromMarkdown(mdPath string) ([]PlanEntry, error) {
	plans, err := parseMarkdown(mdPath)
	if err != nil {
		return nil, err
	}
	return normalizePlanEntries(plans), nil
}

func normalizePlanEntries(plans []PlanEntry) []PlanEntry {
	seenIDs := make(map[string]struct{}, len(plans))
	shortToFull := make(map[string]string, len(plans))
	for idx := range plans {
		plans[idx].Status = normalizePlanStatus(plans[idx].Status)
		plans[idx].Priority = strings.ToLower(strings.TrimSpace(plans[idx].Priority))
		plans[idx].ID = expandPlanEntryID(plans[idx].ID)
		if plans[idx].ID == "" {
			plans[idx].ID = newPlanEntryID()
		}
		for {
			if _, exists := seenIDs[plans[idx].ID]; !exists {
				break
			}
			plans[idx].ID = newPlanEntryID()
		}
		seenIDs[plans[idx].ID] = struct{}{}
		shortToFull[planIDDisplay(plans[idx].ID)] = plans[idx].ID
	}
	for idx := range plans {
		if len(plans[idx].DependsOnIDs) == 0 && len(plans[idx].DependsOn) > 0 {
			plans[idx].DependsOnIDs = resolveDependencyIDs(plans, plans[idx].DependsOn)
		}
		if len(plans[idx].DependsOnIDs) > 0 {
			for depIdx, depID := range plans[idx].DependsOnIDs {
				depID = expandPlanEntryID(depID)
				if fullID, ok := shortToFull[planIDDisplay(depID)]; ok {
					plans[idx].DependsOnIDs[depIdx] = fullID
					continue
				}
				if fullID, ok := shortToFull[depID]; ok {
					plans[idx].DependsOnIDs[depIdx] = fullID
					continue
				}
				plans[idx].DependsOnIDs[depIdx] = depID
			}
		}
		plans[idx].DependsOn = nil
		plans[idx].DependsOnIDs = uniqueDependencyIDs(plans[idx].DependsOnIDs, plans[idx].ID)
	}
	return plans
}

func resolveDependencyIDs(plans []PlanEntry, dependsOn []int) []string {
	if len(dependsOn) == 0 {
		return nil
	}
	ids := make([]string, 0, len(dependsOn))
	for _, depIndex := range dependsOn {
		if depIndex < 1 || depIndex > len(plans) {
			continue
		}
		depID := strings.TrimSpace(plans[depIndex-1].ID)
		if depID != "" {
			ids = append(ids, depID)
		}
	}
	return uniqueDependencyIDs(ids, "")
}

func uniqueDependencyIDs(ids []string, selfID string) []string {
	seen := make(map[string]struct{}, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || id == selfID {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func normalizePlanStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed":
		return "completed"
	case "in_progress":
		return "in_progress"
	default:
		return "pending"
	}
}

func newPlanEntryID() string {
	return uuid.NewString()
}

func expandPlanEntryID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return ""
	}
	if len(id) >= 36 {
		return id
	}
	return id
}

func LoadActivePlanBaseName(sessionID string) string {
	return sanitizePlanBaseName(loadActivePlanFile(sessionID))
}

func LoadActivePlanSnapshotMetadata(sessionID string) (string, bool, error) {
	baseName := LoadActivePlanBaseName(sessionID)
	if baseName == "" {
		return "", false, nil
	}

	workspace := config.WorkingDirectory()
	plans, err := loadPlanData(
		filepath.Join(workspace, baseName+".json"),
		filepath.Join(workspace, baseName+".md"),
	)
	if err != nil {
		return "", false, err
	}

	response := withPlanSnapshotMetadata(
		NewTextResponse(""),
		"list",
		baseName,
		currentPlanRevision(workspace, baseName),
		plans,
		nil,
		nil,
		nil,
		0,
		"",
	)
	return response.Metadata, true, nil
}

func planIDDisplay(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func shortDependencyIDs(ids []string) []string {
	short := make([]string, 0, len(ids))
	for _, id := range ids {
		short = append(short, planIDDisplay(id))
	}
	return short
}

func resolvePlanTarget(plans []PlanEntry, params ManagePlanParams) (int, PlanEntry, error) {
	if id := strings.TrimSpace(params.ID); id != "" {
		idx, err := resolvePlanIndexByID(plans, id)
		if err != nil {
			return -1, PlanEntry{}, err
		}
		return idx, plans[idx], nil
	}
	if params.Index < 1 || params.Index > len(plans) {
		return -1, PlanEntry{}, fmt.Errorf("无效的索引: %d", params.Index)
	}
	return params.Index - 1, plans[params.Index-1], nil
}

func resolvePlanIndexByID(plans []PlanEntry, id string) (int, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return -1, fmt.Errorf("缺少计划项 id")
	}
	match := -1
	for idx, entry := range plans {
		candidate := strings.ToLower(strings.TrimSpace(entry.ID))
		if candidate == id || strings.HasPrefix(candidate, id) {
			if match >= 0 {
				return -1, fmt.Errorf("计划项 id 前缀不唯一: %s", id)
			}
			match = idx
		}
	}
	if match < 0 {
		return -1, fmt.Errorf("未找到计划项 id: %s", id)
	}
	return match, nil
}

func findPlanIndexByID(plans []PlanEntry, id string) int {
	for idx, entry := range plans {
		if strings.EqualFold(strings.TrimSpace(entry.ID), strings.TrimSpace(id)) {
			return idx
		}
	}
	return -1
}

func planTargetDisplay(entry PlanEntry, params ManagePlanParams) string {
	if strings.TrimSpace(params.ID) != "" {
		return fmt.Sprintf("ID %s", planIDDisplay(entry.ID))
	}
	if params.Index > 0 {
		return fmt.Sprintf("索引 %d", params.Index)
	}
	return fmt.Sprintf("ID %s", planIDDisplay(entry.ID))
}

func findDependentPlanTargets(plans []PlanEntry, targetID string) []string {
	dependents := make([]string, 0)
	for _, entry := range plans {
		if normalizePlanStatus(entry.Status) == "completed" {
			continue
		}
		for _, depID := range entry.DependsOnIDs {
			if strings.EqualFold(strings.TrimSpace(depID), strings.TrimSpace(targetID)) {
				dependents = append(dependents, fmt.Sprintf("%s(%s)", strings.TrimSpace(entry.Item), planIDDisplay(entry.ID)))
				break
			}
		}
	}
	return dependents
}

func performArchive(workspace, baseName string, plans []PlanEntry) (int, []PlanEntry, error) {
	var active []PlanEntry
	var completed []archivedPlanEntry

	for _, entry := range plans {
		if entry.Status == "completed" {
			completed = append(completed, archivedPlanEntry{
				PlanEntry:  entry,
				ArchivedAt: time.Now().UnixMilli(),
			})
		} else {
			active = append(active, entry)
		}
	}

	if len(completed) == 0 {
		return 0, plans, nil
	}

	archiveMarkdownPath := filepath.Join(workspace, baseName+"_ARCHIVE.md")
	archiveJSONPath := filepath.Join(workspace, baseName+"_ARCHIVE.jsonl")
	if err := appendArchivedMarkdown(archiveMarkdownPath, completed); err != nil {
		return 0, plans, err
	}
	if err := appendArchivedJSONL(archiveJSONPath, completed); err != nil {
		return 0, plans, err
	}

	return len(completed), active, nil
}

func resolvePlanBaseName(ctx context.Context, rawFileName string) string {
	if baseName := sanitizePlanBaseName(rawFileName); baseName != "" {
		return baseName
	}
	sessionID, messageID := GetContextValues(ctx)
	if sessionID != "" && AllowsImplicitPlanReuse(ctx) {
		if baseName := strings.TrimSpace(loadActivePlanFile(sessionID)); baseName != "" {
			return sanitizePlanBaseName(baseName)
		}
	}
	if baseName := autoPlanBaseNameForMessage(GetPlanScopeID(ctx)); baseName != "" {
		return baseName
	}
	if baseName := autoPlanBaseNameForMessage(messageID); baseName != "" {
		return baseName
	}
	return "TODO"
}

func autoPlanBaseNameForMessage(messageID string) string {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range messageID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
		if b.Len() >= 8 {
			break
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "TODO_" + strings.ToLower(b.String())
}

func shouldRememberPlanBaseName(ctx context.Context, params ManagePlanParams, itemCount int) bool {
	if sanitizePlanBaseName(params.FileName) != "" {
		return true
	}
	if AllowsImplicitPlanReuse(ctx) {
		return true
	}
	switch params.Action {
	case "add", "update", "complete", "remove", "archive":
		return true
	case "list":
		return itemCount > 0
	default:
		return false
	}
}

func formatPlanBaseNameHint(baseName string) string {
	baseName = sanitizePlanBaseName(baseName)
	if baseName == "" || strings.EqualFold(baseName, "TODO") {
		return ""
	}
	return fmt.Sprintf(", 计划: %s.md", baseName)
}

func sanitizePlanBaseName(raw string) string {
	baseName := strings.TrimSpace(raw)
	if baseName == "" {
		return ""
	}
	baseName = filepath.Base(baseName)
	if strings.HasSuffix(baseName, ".md") {
		baseName = strings.TrimSuffix(baseName, ".md")
	} else if strings.HasSuffix(baseName, ".json") {
		baseName = strings.TrimSuffix(baseName, ".json")
	}
	baseName = strings.TrimSpace(baseName)
	if baseName == "" {
		return ""
	}
	return baseName
}

func activePlanStateKey(sessionID string) string {
	return activePlanStateKeyPrefix + strings.TrimSpace(sessionID)
}

func loadActivePlanFile(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	workspace := config.WorkingDirectory()
	var baseName string
	err := withPlanStateRegistryLock(workspace, func() error {
		registry, err := loadActivePlanRegistry(workspace)
		if err != nil {
			return err
		}
		baseName = strings.TrimSpace(registry[activePlanStateKey(sessionID)])
		return nil
	})
	if err != nil {
		return ""
	}
	return baseName
}

func rememberActivePlanFile(ctx context.Context, baseName string) {
	baseName = sanitizePlanBaseName(baseName)
	if baseName == "" {
		return
	}
	sessionID, _ := GetContextValues(ctx)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	workspace := config.WorkingDirectory()
	_ = withPlanStateRegistryLock(workspace, func() error {
		registry, err := loadActivePlanRegistry(workspace)
		if err != nil {
			return err
		}
		registry[activePlanStateKey(sessionID)] = baseName
		return saveActivePlanRegistry(workspace, registry)
	})
}

func activePlanRegistryPath(workspace string) string {
	return filepath.Join(workspace, ".auramate", "active_plan_sessions.json")
}

func planStateRegistryLockPath(workspace string) string {
	return filepath.Join(workspace, ".auramate", "plan_state.lock")
}

func planRevisionRegistryPath(workspace string) string {
	return filepath.Join(workspace, ".auramate", "plan_revisions.json")
}

func withPlanStateRegistryLock(workspace string, fn func() error) error {
	lockPath := planStateRegistryLockPath(workspace)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return err
	}
	fileLock := flock.New(lockPath)
	locked, err := acquireLockWithRetry(context.Background(), fileLock, 25, 200*time.Millisecond)
	if err != nil {
		return err
	}
	if !locked {
		return fmt.Errorf("unable to acquire plan state lock")
	}
	defer fileLock.Unlock()
	return fn()
}

func nextPlanRevision(workspace, baseName string) int64 {
	baseName = sanitizePlanBaseName(baseName)
	if baseName == "" {
		baseName = "TODO"
	}
	var next int64
	err := withPlanStateRegistryLock(workspace, func() error {
		registry, err := loadPlanRevisionRegistry(workspace)
		if err != nil {
			return err
		}
		current := registry[baseName]
		next = current + 1
		if next <= 0 {
			next = time.Now().UnixMilli()
		}
		registry[baseName] = next
		return savePlanRevisionRegistry(workspace, registry)
	})
	if err != nil && next == 0 {
		return time.Now().UnixMilli()
	}
	if next == 0 {
		return time.Now().UnixMilli()
	}
	return next
}

func currentPlanRevision(workspace, baseName string) int64 {
	baseName = sanitizePlanBaseName(baseName)
	if baseName == "" {
		baseName = "TODO"
	}
	var current int64
	err := withPlanStateRegistryLock(workspace, func() error {
		registry, err := loadPlanRevisionRegistry(workspace)
		if err != nil {
			return err
		}
		current = registry[baseName]
		return nil
	})
	if err != nil {
		return 0
	}
	return current
}

func loadPlanRevisionRegistry(workspace string) (map[string]int64, error) {
	path := planRevisionRegistryPath(workspace)
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]int64{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(content) == 0 {
		return map[string]int64{}, nil
	}
	registry := make(map[string]int64)
	if err := json.Unmarshal(content, &registry); err != nil {
		return nil, err
	}
	return registry, nil
}

func savePlanRevisionRegistry(workspace string, registry map[string]int64) error {
	path := planRevisionRegistryPath(workspace)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}

func loadActivePlanRegistry(workspace string) (map[string]string, error) {
	path := activePlanRegistryPath(workspace)
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(content) == 0 {
		return map[string]string{}, nil
	}
	registry := make(map[string]string)
	if err := json.Unmarshal(content, &registry); err != nil {
		return nil, err
	}
	return registry, nil
}

func saveActivePlanRegistry(workspace string, registry map[string]string) error {
	path := activePlanRegistryPath(workspace)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}

func appendArchivedMarkdown(path string, entries []archivedPlanEntry) error {
	if len(entries) == 0 {
		return nil
	}
	var lines []string
	for _, entry := range entries {
		line := fmt.Sprintf("- [x] %s", entry.Item)
		if id := strings.TrimSpace(entry.ID); id != "" {
			line += fmt.Sprintf(" {id:%s}", id)
		}
		if entry.Priority != "" {
			line += fmt.Sprintf(" [%s]", strings.Title(entry.Priority))
		}
		if entry.DueDate != "" {
			line += fmt.Sprintf(" due_date: %s", entry.DueDate)
		}
		if len(entry.DependsOnIDs) > 0 {
			line += fmt.Sprintf(" depends_on_ids: [%s]", strings.Join(entry.DependsOnIDs, ", "))
		}
		if len(entry.Tags) > 0 {
			for _, tag := range entry.Tags {
				line += fmt.Sprintf(" #%s", tag)
			}
		}
		line += fmt.Sprintf(" archived_at: %d", entry.ArchivedAt)
		lines = append(lines, line)
	}
	return appendLines(path, lines)
}

func appendArchivedJSONL(path string, entries []archivedPlanEntry) error {
	if len(entries) == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	encoder := json.NewEncoder(f)
	for _, entry := range entries {
		if err := encoder.Encode(entry); err != nil {
			return err
		}
	}
	return nil
}

func appendLines(path string, lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		return err
	}
	return nil
}

func getPriorityVal(p string) int {
	switch p {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func parseDate(d string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", d)
	return t, err == nil
}

func atomicWrite(path string, data []byte) error {
	// Write to a temporary file first
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	// Atomic rename with retry for Windows
	// Windows file system might hold a lock briefly (e.g. Anti-virus, Indexing)
	var err error
	for i := 0; i < 5; i++ {
		err = os.Rename(tmpPath, path)
		if err == nil {
			return nil
		}
		// If rename failed, wait and retry
		time.Sleep(100 * time.Millisecond)
	}

	// If rename still fails, try to remove destination and rename again
	if err != nil {
		os.Remove(path)
		err = os.Rename(tmpPath, path)
	}

	return err
}

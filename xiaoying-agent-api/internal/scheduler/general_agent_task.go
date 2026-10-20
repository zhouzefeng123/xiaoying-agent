package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/logging"
	"github.com/google/uuid"
)

func (s *Scheduler) runGeneralAgentTask(ctx context.Context, task db.ScheduledTask) error {
	logging.Info("Running General Agent Task...", "id", task.ID)

	prompt := task.Parameters
	if prompt == "" {
		logging.Warn("General Agent Task has empty parameters (prompt)", "id", task.ID)
		return nil
	}

	// 1. Create a system session
	sessionID := "scheduler-agent-" + uuid.New().String()
	sess, err := s.sessions.CreateTaskSession(ctx, sessionID, "system_scheduler", "General Agent Task")
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	// 2. Initialize Agent
	ag, err := agent.NewAgent(
		config.AgentTask,
		s.sessions,
		s.messages,
		s.memories,
		agent.TaskAgentTools(s.q, s.db, s.memories, s.permissions, s.lspRegistry, s.skillsManager, s.mcpService, s.embedder),
		s.q,
		s.skillsManager,
		nil,
		s.embedder,
	)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	// Prepend system instruction to ensure the agent generates a meaningful result for notification
	var fullPrompt string
	if strings.Contains(prompt, "飞书") || strings.Contains(strings.ToLower(prompt), "feishu") {
		fullPrompt = fmt.Sprintf(`Target Task: %s

Available Tool Hint:
- Use "feishu-messenger" tool to check messages.
- Action: "list"
- Container ID: Extract from task (e.g. oc_...)
- Container ID Type: "chat"

System Instruction:
1. Execute the tool to get information.
2. If no new messages or relevant info is found, use "send_notification" tool to notify user about "No new messages".
3. If new messages are found, summarize them and use "send_notification" tool to send the summary.
4. IMPORTANT: When using "send_notification", you MUST provide "title" and "message" parameters. Do NOT use "content".
5. Do NOT output raw JSON.
6. ALWAYS end with a notification.
`, prompt)
	} else {
		fullPrompt = fmt.Sprintf(`Target Task: %s

System Instruction:
1. Analyze the task and select the appropriate tool from your toolkit.
2. Execute the tool to complete the task.
3. Use "send_notification" tool to notify the user about the result or summary.
4. IMPORTANT: When using "send_notification", you MUST provide "title" and "message" parameters. Do NOT use "content".
`, prompt)
	}

	// 3. Run the Agent
	// Using "normal" mode to allow full tool usage
	eventChan, err := ag.Run(ctx, sess.ID, fullPrompt, "normal")
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// 4. Wait for completion and notify
	var lastNotification string
	for event := range eventChan {
		if event.Error != nil {
			logging.Error("Agent execution error", "error", event.Error)
			return event.Error
		}

		// Capture notification events
		if event.Type == agent.AgentEventTypeNotification {
			var meta map[string]string
			if err := json.Unmarshal([]byte(event.Content), &meta); err == nil {
				lastNotification = fmt.Sprintf("Notification Sent: %s - %s", meta["title"], meta["message"])
			}
		}

		if event.Done {
			// Prioritize Agent's final response (summary)
			result := event.Message.Content().String()

			// Only fallback to tool output parsing if the agent returned absolutely nothing
			if result == "" {
				// If we have a notification, use that as the result
				if lastNotification != "" {
					result = lastNotification
				} else {
					logging.Warn("General Agent returned empty result, triggering LLM-based summarization", "id", task.ID)

					// Trigger a secondary run to summarize the tool outputs
					summaryPrompt := "The previous tool execution was successful but returned no text description. Please review the tool results above and provide a concise summary of the information found. If no useful information was found, state that clearly."

					sumEventChan, sumErr := ag.Run(ctx, sess.ID, summaryPrompt, "normal")
					if sumErr != nil {
						logging.Error("Failed to run summary generation", "error", sumErr)
						// Fallback to generic message if summarization fails
						result = "任务已完成，但未生成文本摘要或工具输出 (总结生成失败)。"
					} else {
						for sumEvent := range sumEventChan {
							if sumEvent.Error != nil {
								logging.Error("Summary generation error", "error", sumEvent.Error)
							}
							if sumEvent.Done {
								result = sumEvent.Message.Content().String()
							}
						}
					}

					// Final fallback if even the summary run returned empty
					if result == "" {
						result = "任务已完成，但未生成文本摘要或工具输出。"
					}
				}
			}

			logging.Info("General Agent Task Completed", "result", result)
			// Extract a brief title from the prompt (first 20 chars) or use a default
			title := "智能助手任务报告"
			runes := []rune(prompt)
			if len(runes) > 0 {
				length := 15
				if len(runes) < length {
					length = len(runes)
				}
				title = "任务: " + string(runes[:length]) + "..."
			}

			s.notifyTask(task, title, result)
		}
	}

	return nil
}

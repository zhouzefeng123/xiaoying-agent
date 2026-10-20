import { startTransition, useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { wsClient, type ServerEvent, type ClientMessage, type ContextStats, type ContextAssemblyReport } from '../lib/ws';
import { type Message, type ToolExecution, type TimelineEvent } from '../components/chat/MessageBubble';
import { toast } from 'sonner';
import { useTranslation } from 'react-i18next';
import { useNotifications } from '../contexts/useNotifications';
import { sessionService, type SessionMessageRecord } from '../services/sessionService';
import { countRenderableThinkingBlocks, isMeaningfulRenderableChatText, stripInjectedChatControlContent } from '../lib/chatContent';
import { sanitizeUTF8 } from '../lib/utils';
import {
    applyPlanProgressExecution,
    isPlanProgressTool,
    type PlanProgressExecution,
    type PlanProgressSnapshot,
} from '../lib/planProgress';
import { publishWordEditorAction, getWordEditorStateSnapshot } from '../components/word-editor/word-editor-bus';
import type { WordEditorActionPayload } from '../components/word-editor/types';

export interface PermissionRequest {
    id: string;
    tool_name: string;
    description: string;
    action: string;
    params: Record<string, unknown>;
    path: string;
}

export interface Thought {
    id: string;
    timestamp: number;
    type: 'instinct' | 'thought' | 'action' | 'observation' | 'heartbeat' | 'state_change';
    content: string;
    source?: string;
}

interface GitStatus {
    path: string;
    status: string;
}

interface GitStatusUpdatePayload {
    paths?: string[];
    updates?: GitStatus[];
}

export interface SendMessageOptions {
    appendUserMessage?: boolean;
    enableSubagents?: boolean;
    suppressErrorUi?: boolean;
    replaceLatestAssistantTurn?: boolean;
}

export interface ChatSendResult {
    status: 'success' | 'error' | 'canceled';
    errorCode?: number | null;
    errorMessage?: string;
    errorEvent?: ServerEvent;
    model?: string;
    ttftMs?: number;
    totalMs?: number;
    outputChars?: number;
}

export interface LoadSessionOptions {
    model?: string;
    mode?: string;
    enableSubagents?: boolean;
}

type ThoughtEventData = {
    type: Thought['type'];
    content: string;
    source?: string;
};

const INTERNAL_HIDDEN_USER_PROMPT_MARKER = '[AURAMATE_INTERNAL_USER_PROMPT:WORD_AUTO_RELOCATE]';

const notifyTaskCompleteIfAway = () => {
    const title = 'XiaoYing';
    const body = '任务已完成';

    if (window.electron?.showNotification) {
        window.electron.showNotification({ title, body });
        return;
    }

    if (document.visibilityState === 'visible' || !('Notification' in window)) {
        return;
    }

    if (Notification.permission === 'granted') {
        new Notification(title, { body });
        return;
    }

    if (Notification.permission === 'default') {
        void Notification.requestPermission().then((permission) => {
            if (permission === 'granted' && document.visibilityState !== 'visible') {
                new Notification(title, { body });
            }
        });
    }
};

const normalizeContextStats = (value: unknown): ContextStats | null => {
    if (!value || typeof value !== 'object') {
        return null;
    }

    const normalizeContextAssembly = (assembly: unknown): ContextAssemblyReport | undefined => {
        if (!assembly || typeof assembly !== 'object') {
            return undefined;
        }

        const candidate = assembly as Partial<ContextAssemblyReport>;
        const readText = (input: unknown): string | undefined => typeof input === 'string' && input.trim() ? input.trim() : undefined;
        const readCount = (input: unknown): number | undefined => Number.isFinite(Number(input)) ? Math.max(0, Number(input)) : undefined;
        const normalized: ContextAssemblyReport = {
            critical_cache: readText(candidate.critical_cache),
            dynamic_cache: readText(candidate.dynamic_cache),
            history_window_source: readText(candidate.history_window_source),
            history_window_fallback_reason: readText(candidate.history_window_fallback_reason),
            history_window_messages: readCount(candidate.history_window_messages),
            warm_context_source: readText(candidate.warm_context_source),
            warm_context_session_id: readText(candidate.warm_context_session_id),
            warm_context_status: readText(candidate.warm_context_status),
            capsule_memory_count: readCount(candidate.capsule_memory_count),
            turn_memory_count: readCount(candidate.turn_memory_count),
            warm_context_messages: readCount(candidate.warm_context_messages),
            memory_capsule: candidate.memory_capsule === true,
            turn_context: candidate.turn_context === true,
            implicit_global_fallback_skipped: candidate.implicit_global_fallback_skipped === true,
            working_state: candidate.working_state === true,
            mutation_journal: candidate.mutation_journal === true,
            dynamic_suffix: candidate.dynamic_suffix === true,
        };

        return Object.values(normalized).some((entry) => entry !== undefined && entry !== false)
            ? normalized
            : undefined;
    };

    const candidate = value as Partial<ContextStats>;
    const currentTokens = Number(candidate.current_tokens);
    const contextWindowTokens = Number(candidate.context_window_tokens);
    const fallbackMaxTokens = Number(candidate.max_tokens);
    const maxTokens = Number.isFinite(contextWindowTokens) && contextWindowTokens > 0
        ? contextWindowTokens
        : fallbackMaxTokens;

    if (!Number.isFinite(currentTokens) || !Number.isFinite(maxTokens) || maxTokens <= 0) {
        return null;
    }

    return {
        ...candidate,
        source: typeof candidate.source === 'string' ? candidate.source : 'estimate',
        current_tokens: Math.max(0, currentTokens),
        estimated_tokens: Number.isFinite(Number(candidate.estimated_tokens))
            ? Math.max(0, Number(candidate.estimated_tokens))
            : undefined,
        observed_prompt_tokens: Number.isFinite(Number(candidate.observed_prompt_tokens))
            ? Math.max(0, Number(candidate.observed_prompt_tokens))
            : undefined,
        remaining_tokens: Number.isFinite(Number(candidate.remaining_tokens))
            ? Math.max(0, Number(candidate.remaining_tokens))
            : undefined,
        max_tokens: maxTokens,
        context_window_tokens: Number.isFinite(contextWindowTokens) && contextWindowTokens > 0
            ? contextWindowTokens
            : maxTokens,
        assembly: normalizeContextAssembly(candidate.assembly),
        sections: Array.isArray(candidate.sections)
            ? candidate.sections.filter((section) => {
                return section &&
                    typeof section.key === 'string' &&
                    typeof section.label === 'string' &&
                    Number.isFinite(Number(section.tokens));
            }).map((section) => ({
                key: section.key,
                label: section.label,
                tokens: Math.max(0, Number(section.tokens)),
            }))
            : undefined,
    } as ContextStats;
};

const extractToolCallInput = (toolCall: any): string => {
    const rawInput = toolCall?.arguments ?? toolCall?.input;

    if (typeof rawInput === 'string') {
        return rawInput;
    }

    if (!rawInput || typeof rawInput !== 'object') {
        return '';
    }

    try {
        return Object.keys(rawInput).length > 0 ? JSON.stringify(rawInput) : '';
    } catch {
        return '';
    }
};

const getToolExecutionStableKey = (execution: ToolExecution): string => {
    const executionId = execution.executionId?.trim();
    if (executionId) {
        return `execution:${executionId}`;
    }
    return `tool-call:${execution.id}`;
};

const getToolExecutionCompletionKey = (execution: Pick<ToolExecution, 'id' | 'executionId'>): string => {
    const executionId = execution.executionId?.trim();
    if (executionId) {
        return `execution:${executionId}`;
    }
    return `tool-call:${execution.id}`;
};

const hasRealtimeToolExecution = (
    executionMap: Map<string, ToolExecution[]>,
    messageId: string | undefined,
    match: {
        executionId?: string;
        toolCallId?: string;
        toolIndex?: number;
        toolName?: string;
    }
): boolean => {
    if (!messageId) {
        return false;
    }

    const executions = executionMap.get(messageId);
    if (!executions || executions.length === 0) {
        return false;
    }

    if (match.executionId && executions.some((execution) => execution.executionId === match.executionId)) {
        return true;
    }

    if (match.toolCallId && executions.some((execution) => execution.id === match.toolCallId)) {
        return true;
    }

    if (typeof match.toolIndex === 'number') {
        const indexedExecution = executions[match.toolIndex];
        if (indexedExecution && (!match.toolName || indexedExecution.name === match.toolName)) {
            return true;
        }
    }

    if (match.toolName && executions.some((execution) => execution.status === 'running' && execution.name === match.toolName)) {
        return true;
    }

    return false;
};

const pickRicherText = (left?: string, right?: string): string => {
    const leftText = left ?? '';
    const rightText = right ?? '';
    if (rightText.trim().length === 0) {
        return leftText;
    }
    if (leftText.trim().length === 0) {
        return rightText;
    }
    return rightText.length >= leftText.length ? rightText : leftText;
};

const statusRank: Record<ToolExecution['status'], number> = {
    running: 0,
    completed: 1,
    failed: 2,
};

const mergeToolExecution = (current: ToolExecution, incoming: ToolExecution): ToolExecution => {
    const nextStatus = statusRank[incoming.status] >= statusRank[current.status]
        ? incoming.status
        : current.status;

    return {
        ...current,
        ...incoming,
        id: current.id || incoming.id,
        executionId: current.executionId || incoming.executionId,
        name: incoming.name || current.name,
        input: pickRicherText(current.input, incoming.input),
        output: pickRicherText(current.output, incoming.output),
        status: nextStatus,
        isCodeStream: Boolean(current.isCodeStream || incoming.isCodeStream),
        isFinalizing: nextStatus === 'running' && Boolean(current.isFinalizing || incoming.isFinalizing),
        metadata: incoming.metadata ?? current.metadata,
    };
};

const planExecutionFromToolResultPart = (part: unknown, index: number): PlanProgressExecution | null => {
    if (!part || typeof part !== 'object') return null;

    const record = part as Record<string, any>;
    const data = record.data && typeof record.data === 'object'
        ? record.data as Record<string, any>
        : record;
    const type = record.type ?? data.type;
    const hasToolResultShape = typeof data.tool_call_id === 'string'
        || typeof data.toolCallID === 'string'
        || typeof data.name === 'string'
        || data.metadata !== undefined;

    if (type !== 'tool_result' && type !== 'toolResult' && !hasToolResultShape) {
        return null;
    }

    const name = typeof data.name === 'string' ? data.name : '';
    if (!name || !isPlanProgressTool({ name })) {
        return null;
    }

    return {
        id: typeof data.tool_call_id === 'string'
            ? data.tool_call_id
            : typeof data.toolCallID === 'string'
                ? data.toolCallID
                : `history-plan-tool-${index}`,
        name,
        input: typeof data.input === 'string' ? data.input : '',
        output: typeof data.content === 'string' ? data.content : '',
        status: data.is_error || data.isError ? 'failed' : 'completed',
        metadata: data.metadata,
    };
};

const parseHistoryPlanExecutions = (partsRaw: string): PlanProgressExecution[] => {
    if (!partsRaw) return [];
    try {
        const parsed = JSON.parse(partsRaw);
        const parts = Array.isArray(parsed) ? parsed : [parsed];
        return parts
            .map((part, index) => planExecutionFromToolResultPart(part, index))
            .filter((execution): execution is PlanProgressExecution => Boolean(execution));
    } catch {
        return [];
    }
};

const buildPlanSnapshotFromExecutions = (executions: PlanProgressExecution[]): PlanProgressSnapshot | null => {
    return executions.reduce<PlanProgressSnapshot | null>((snapshot, execution) => {
        return applyPlanProgressExecution(snapshot, execution);
    }, null);
};

const normalizeRealtimeToolExecutions = (source: Map<string, ToolExecution[]>): Map<string, ToolExecution[]> => {
    const normalized = new Map<string, ToolExecution[]>();

    for (const [messageId, executions] of source.entries()) {
        const seen = new Map<string, number>();
        const resultExecs: ToolExecution[] = [];

        for (const execution of executions) {
            const stableKey = getToolExecutionStableKey(execution);
            const existingIndex = seen.get(stableKey);

            if (existingIndex === undefined) {
                seen.set(stableKey, resultExecs.length);
                resultExecs.push(execution);
                continue;
            }

            resultExecs[existingIndex] = mergeToolExecution(resultExecs[existingIndex], execution);
        }

        normalized.set(messageId, resultExecs);
    }

    return normalized;
};

const CODE_STREAM_TOOL_NAMES = new Set([
    'write_file',
    'replace_file',
    'write',
    'edit',
    'patch',
    'edit_file',
    'apply_patch',
    'create_file',
    'search_replace',
    'batch_write'
]);

const KNOWN_CHAT_ERROR_CODES = new Set([1400, 1403, 1500]);

const KNOWN_CHAT_ERROR_PATTERNS: Array<{ code: number; patterns: RegExp[] }> = [
    {
        code: 1403,
        patterns: [
            /资源使用超限/i,
            /quota exceeded/i,
            /insufficient quota/i,
            /余额不足/i,
            /额度不足/i,
            /用量超限/i,
        ],
    },
];

const tryExtractKnownErrorCodeFromText = (value: string): number | null => {
    const trimmed = value.trim();
    if (!trimmed) {
        return null;
    }

    for (const { code, patterns } of KNOWN_CHAT_ERROR_PATTERNS) {
        if (patterns.some((pattern) => pattern.test(trimmed))) {
            return code;
        }
    }

    return null;
};

const tryExtractKnownErrorCode = (value: unknown): number | null => {
    if (typeof value === 'number') {
        return KNOWN_CHAT_ERROR_CODES.has(value) ? value : null;
    }

    if (typeof value === 'string') {
        const trimmed = value.trim();
        if (!trimmed) {
            return null;
        }

        if (/^\d+$/.test(trimmed)) {
            const numericValue = Number(trimmed);
            return KNOWN_CHAT_ERROR_CODES.has(numericValue) ? numericValue : null;
        }

        try {
            const parsed = JSON.parse(trimmed);
            const nestedCode = tryExtractKnownErrorCode(parsed);
            if (nestedCode !== null) {
                return nestedCode;
            }
        } catch {
            // Ignore non-JSON content and continue with regex extraction.
        }

        const inferredCode = tryExtractKnownErrorCodeFromText(trimmed);
        if (inferredCode !== null) {
            return inferredCode;
        }

        const match = trimmed.match(/(?:^|[^\d])(1400|1403|1500)(?:[^\d]|$)/);
        if (!match) {
            return null;
        }

        const numericValue = Number(match[1]);
        return KNOWN_CHAT_ERROR_CODES.has(numericValue) ? numericValue : null;
    }

    if (value && typeof value === 'object') {
        const record = value as Record<string, unknown>;
        const candidateKeys = ['code', 'error_code', 'status', 'errno'];
        for (const key of candidateKeys) {
            const nestedCode = tryExtractKnownErrorCode(record[key]);
            if (nestedCode !== null) {
                return nestedCode;
            }
        }

        const nestedContainers = ['error', 'data', 'detail'];
        for (const key of nestedContainers) {
            const nestedCode = tryExtractKnownErrorCode(record[key]);
            if (nestedCode !== null) {
                return nestedCode;
            }
        }
    }

    return null;
};

let localEntityCounter = 0;

const createLocalEntityId = (prefix: string): string => {
    localEntityCounter += 1;
    if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
        return `${prefix}-${crypto.randomUUID()}`;
    }
    return `${prefix}-${Date.now().toString(36)}-${localEntityCounter.toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
};

const hasRunningToolExecution = (executions?: ToolExecution[]): boolean => {
    return Boolean(executions?.some((execution) => execution.status === 'running'));
};

const findLatestRunningToolExecutionMessageId = (
    executionsByMessage: Map<string, ToolExecution[]>,
    preferredMessageId?: string | null,
): string | null => {
    if (preferredMessageId && hasRunningToolExecution(executionsByMessage.get(preferredMessageId))) {
        return preferredMessageId;
    }

    const entries = Array.from(executionsByMessage.entries());
    for (let index = entries.length - 1; index >= 0; index -= 1) {
        const [messageId, executions] = entries[index];
        if (hasRunningToolExecution(executions)) {
            return messageId;
        }
    }

    return null;
};

const getKnownChatErrorCode = (event: Pick<ServerEvent, 'content' | 'data'>): number | null => {
    const fromData = tryExtractKnownErrorCode(event.data);
    if (fromData !== null) {
        return fromData;
    }

    return tryExtractKnownErrorCode(event.content);
};

const getErrorEventText = (event: Pick<ServerEvent, 'content' | 'data'>): string => {
    if (typeof event.content === 'string' && event.content.trim()) {
        return event.content.trim();
    }

    if (typeof event.data === 'string' && event.data.trim()) {
        return event.data.trim();
    }

    if (event.data && typeof event.data === 'object') {
        const record = event.data as Record<string, unknown>;
        const candidateKeys = ['message', 'error', 'detail'];
        for (const key of candidateKeys) {
            const value = record[key];
            if (typeof value === 'string' && value.trim()) {
                return value.trim();
            }
        }

        try {
            return JSON.stringify(event.data);
        } catch {
            return '';
        }
    }

    return '';
};

const isCancellationEvent = (event: Pick<ServerEvent, 'content' | 'data'>): boolean => {
    const rawText = getErrorEventText(event);
    if (!rawText) {
        return false;
    }

    return /(request cancel+ed by user|tool execution cancel+ed by user|script execution was cancel+ed|任务已被停止|任务已取消|cancel+ed by user)/i.test(rawText);
};

const isCodeStreamTool = (name?: string): boolean => {
    return typeof name === 'string' && CODE_STREAM_TOOL_NAMES.has(name);
};

const isHiddenSyntheticUserPrompt = (value?: string | null): boolean => {
    const normalized = value?.trim() ?? '';
    if (!normalized) {
        return false;
    }
    return normalized.includes(INTERNAL_HIDDEN_USER_PROMPT_MARKER);
};

const resolveWordDocumentPath = (document: { path?: string; file?: File } | null | undefined): string => {
    if (!document) {
        return '';
    }

    if (typeof document.path === 'string' && document.path.trim()) {
        return document.path.trim();
    }

    const fileWithPath = document.file as (File & { _absolutePath?: string; path?: string }) | undefined;
    const candidate = fileWithPath?._absolutePath ?? fileWithPath?.path;
    return typeof candidate === 'string' ? candidate.trim() : '';
};

export function useChat() {
    const { t } = useTranslation();
    const { addNotification } = useNotifications();
    const [messages, setMessages] = useState<Message[]>([]);
    const [fixedErrorMessage, setFixedErrorMessage] = useState<string | null>(null);
    const [sessionId, setSessionId] = useState<string>('');
    const [isStreaming, setIsStreaming] = useState(false);
    const STREAM_WINDOW_SIZE = 60;
    const [currentOperation, setCurrentOperation] = useState<{ activity: string; file?: string } | null>(null);
    const [activeOperations, setActiveOperations] = useState<Map<string, { activity: string; file?: string }>>(new Map());
    const [gitStatusUpdates, setGitStatusUpdates] = useState<GitStatus[]>([]);
    const [fileWatchPaths, setFileWatchPaths] = useState<string[]>([]);

    const [permissionRequest, setPermissionRequest] = useState<PermissionRequest | null>(null);
    const [thoughts, setThoughts] = useState<Thought[]>([]);
    const [tokenUsage, setTokenUsage] = useState<{input_tokens: number; output_tokens: number; total_tokens: number} | null>(null);
    const [sessionTokenUsage, setSessionTokenUsage] = useState<{input_tokens: number; output_tokens: number; total_tokens: number}>({input_tokens: 0, output_tokens: 0, total_tokens: 0});
    const [contextStats, setContextStats] = useState<ContextStats | null>(null);
    const [serverReadOnlyMode, setServerReadOnlyMode] = useState<boolean | null>(null);
    // Tracks the last usage value sent in the current agent run, to compute per-event deltas.
    // Prevents multi-step tool calls from double-counting cumulative backend values.
    const lastRunUsageRef = useRef<{input_tokens: number; output_tokens: number; total_tokens: number}>({input_tokens: 0, output_tokens: 0, total_tokens: 0});
    const [isGenerating, setIsGenerating] = useState(false);
    const [agentMode, setAgentMode] = useState<string>('aura-normal'); // Default to normal
    
    // Phase 6: Real-time Only Tool Executions (Not persisted in messages)
    const [realtimeToolExecutions, setRealtimeToolExecutions] = useState<Map<string, ToolExecution[]>>(new Map());
    const [planProgressSnapshot, setPlanProgressSnapshot] = useState<PlanProgressSnapshot | null>(null);

    const isStreamingRef = useRef(false);
    const realtimeToolExecutionsRef = useRef(realtimeToolExecutions);
    useEffect(() => { realtimeToolExecutionsRef.current = realtimeToolExecutions; }, [realtimeToolExecutions]);
    // FIX: Sync mirror of messages state for synchronous reads in event handlers
    // where setMessages callback hasn't executed yet.
    const messagesRef = useRef<Message[]>([]);
    // Keep messagesRef in sync after each render
    useEffect(() => { messagesRef.current = messages; }, [messages]);
    const sessionIdRef = useRef('');
    useEffect(() => { sessionIdRef.current = sessionId; }, [sessionId]);
    const loadSessionSequenceRef = useRef(0);
    
    // Memoized getter for tool executions to prevent unnecessary re-renders
    const getToolExecutions = useCallback((messageId: string) => {
        return realtimeToolExecutions.get(messageId);
    }, [realtimeToolExecutions]);

    // Keep track of the current assistant message ID being streamed
    const currentAssistantMessageId = useRef<string | null>(null);
    const splitBoundaryActiveRef = useRef(false);
    // Stable routing maps for tool events
    const toolCallToMessageIdRef = useRef<Map<string, string>>(new Map());
    const executionToMessageIdRef = useRef<Map<string, string>>(new Map());
    const codeStreamCompletionTimersRef = useRef<Map<string, number>>(new Map());
    const serverMessageIdToLocalMessageIdRef = useRef<Map<string, string>>(new Map());
    const pendingServerMessageIdRef = useRef<string | null>(null);
    const pendingChatRequestRef = useRef<{
        resolve: (result: ChatSendResult) => void;
        options: Required<SendMessageOptions>;
        model?: string;
        startedAt: number;
        firstTokenAt?: number;
        outputChars: number;
    } | null>(null);
    const stopRequestedRef = useRef(false);
    const ensureToolHostMessage = useCallback((): string => {
        const currentMsgs = messagesRef.current;
        const lastMsg = currentMsgs[currentMsgs.length - 1];

        if (
            lastMsg
            && lastMsg.role === 'assistant'
            && lastMsg.isStreaming
            && (!splitBoundaryActiveRef.current || currentAssistantMessageId.current === lastMsg.id)
        ) {
            currentAssistantMessageId.current = lastMsg.id;
            return lastMsg.id;
        }

        const placeholderId = createLocalEntityId('tool-placeholder');
        currentAssistantMessageId.current = placeholderId;
        setMessages(prev => [...prev, {
            id: placeholderId,
            role: 'assistant' as const,
            content: '',
            timestamp: Date.now(),
            responseStartedAt: pendingChatRequestRef.current?.startedAt,
            isStreaming: true,
            isToolPlaceholder: true
        }]);
        splitBoundaryActiveRef.current = false;
        return placeholderId;
    }, []);

    const bindServerMessageIdToLocal = useCallback((serverMessageId?: string | null, localMessageId?: string | null) => {
        if (!serverMessageId || !localMessageId) {
            return;
        }
        serverMessageIdToLocalMessageIdRef.current.set(serverMessageId, localMessageId);
    }, []);

    const resolveLocalMessageId = useCallback((messageId?: string | null): string | undefined => {
        if (!messageId) {
            return undefined;
        }
        return serverMessageIdToLocalMessageIdRef.current.get(messageId)
            || (messagesRef.current.some((message) => message.id === messageId) ? messageId : undefined);
    }, []);

    const bindPendingServerMessageId = useCallback((localMessageId?: string | null) => {
        if (!localMessageId || !pendingServerMessageIdRef.current) {
            return;
        }
        bindServerMessageIdToLocal(pendingServerMessageIdRef.current, localMessageId);
        pendingServerMessageIdRef.current = null;
    }, [bindServerMessageIdToLocal]);

    const appendAssistantNotice = useCallback((content: string) => {
        const normalizedContent = content.trim();
        if (!normalizedContent) {
            return;
        }

        currentAssistantMessageId.current = null;

        setMessages((prev) => {
            const lastMsg = prev[prev.length - 1];
            if (lastMsg && lastMsg.role === 'assistant' && lastMsg.isStreaming) {
                const settledLastMsg = {
                    ...lastMsg,
                    isStreaming: false,
                    isToolPlaceholder: false,
                };

                if (!lastMsg.content.trim()) {
                    return [
                        ...prev.slice(0, -1),
                        {
                            ...settledLastMsg,
                            content: normalizedContent,
                            timestamp: Date.now(),
                        },
                    ];
                }

                return [
                    ...prev.slice(0, -1),
                    settledLastMsg,
                    {
                        id: createLocalEntityId('assistant-notice'),
                        role: 'assistant' as const,
                        content: normalizedContent,
                        timestamp: Date.now(),
                    },
                ];
            }

            return [
                ...prev,
                {
                    id: createLocalEntityId('assistant-notice'),
                    role: 'assistant' as const,
                    content: normalizedContent,
                    timestamp: Date.now(),
                },
            ];
        });
    }, []);

    const settlePendingChatRequest = useCallback((result: ChatSendResult) => {
        const pendingRequest = pendingChatRequestRef.current;
        if (!pendingRequest) {
            return;
        }

        pendingChatRequestRef.current = null;
        pendingRequest.resolve({
            ...result,
            model: pendingRequest.model,
            ttftMs: pendingRequest.firstTokenAt ? Math.max(0, pendingRequest.firstTokenAt - pendingRequest.startedAt) : undefined,
            totalMs: Math.max(0, Date.now() - pendingRequest.startedAt),
            outputChars: pendingRequest.outputChars,
        });
    }, []);

    const removeLatestAssistantTurnForRetry = useCallback(() => {
        currentAssistantMessageId.current = null;
        pendingTokens.current = '';
        setFixedErrorMessage(null);
        toolCallToMessageIdRef.current.clear();
        executionToMessageIdRef.current.clear();
        serverMessageIdToLocalMessageIdRef.current.clear();
        pendingServerMessageIdRef.current = null;

        cancelScheduledTokenFlush();

        let removedMessageIds: string[] = [];
        setMessages((prev) => {
            const lastUserIndex = [...prev].map((message) => message.role).lastIndexOf('user');
            if (lastUserIndex === -1 || lastUserIndex === prev.length - 1) {
                return prev;
            }

            const trailingMessages = prev.slice(lastUserIndex + 1);
            removedMessageIds = trailingMessages.map((message) => message.id);
            return prev.slice(0, lastUserIndex + 1);
        });

        if (removedMessageIds.length > 0) {
            setRealtimeToolExecutions((prev) => {
                const next = new Map(prev);
                removedMessageIds.forEach((messageId) => next.delete(messageId));
                return next;
            });
        }
    }, []);

    const buildChatErrorMessage = useCallback((event: ServerEvent): string => {
        const code = getKnownChatErrorCode(event);
        const rawText = getErrorEventText(event);

        if (code === 1400) {
            return '错误 1400：请求失败。';
        }

        if (code === 1500) {
            return '错误 1500：服务暂时不可用，请稍后重试。';
        }

        if (code === 1403) {
            const paymentUrl = new URL('https://www.auramate.cn/payment');
            const authToken = localStorage.getItem('auth_token');
            if (authToken && !authToken.startsWith('local_')) {
                paymentUrl.pathname = '/login';
                paymentUrl.searchParams.set('token', authToken);
                paymentUrl.searchParams.set('redirect', '/payment');
            }

            return [
                '错误 1403：资源使用超限。',
                '',
                '请点击下方链接开通 Pro 会员或续费：',
                '',
                `[开通或续费](${paymentUrl.toString()})`,
            ].join('\n');
        }

        if (!rawText) {
            return t('error.unknown');
        }

        return `错误：${rawText}`;
    }, [t]);

    const showFixedChatError = useCallback((content: string) => {
        const normalizedContent = content.trim();
        if (!normalizedContent) {
            return;
        }

        setFixedErrorMessage(normalizedContent);
    }, []);
    
    // Token streaming refs
    const pendingTokens = useRef<string>("");
    const rafId = useRef<number | null>(null);
    const tokenFlushTimer = useRef<number | null>(null);
    const nextTokenFlushAtRef = useRef(0);
    const MAX_PENDING_TOKEN_CHARS = import.meta.env.PROD ? 240 : 160;
    const TOKEN_FLUSH_CADENCE_MS = 16;

    const pendingThoughts = useRef<Thought[]>([]);
    const thoughtFlushTimeout = useRef<number | null>(null);
    const THOUGHT_FLUSH_INTERVAL = import.meta.env.PROD ? 180 : 120;

    // Batch progress updates to avoid high-frequency message re-renders.
    const pendingProgress = useRef<{ percentage: number; details: string } | null>(null);
    const progressFlushTimer = useRef<number | null>(null);
    const PROGRESS_FLUSH_INTERVAL = import.meta.env.PROD ? 180 : 100;

    // Batch realtime tool execution updates to reduce high-frequency re-renders.
    const toolExecutionUpdaters = useRef<Array<(map: Map<string, ToolExecution[]>) => void>>([]);
    const toolExecutionFlushTimer = useRef<number | null>(null);
    const TOOL_EXEC_FLUSH_INTERVAL = import.meta.env.PROD ? 180 : 100;
    const CODE_STREAM_COMPLETION_QUIET_MS = 350;

    const flushToolExecutionUpdates = useCallback(() => {
        if (toolExecutionUpdaters.current.length === 0) {
            if (toolExecutionFlushTimer.current) {
                clearTimeout(toolExecutionFlushTimer.current);
                toolExecutionFlushTimer.current = null;
            }
            return;
        }

        const updaters = toolExecutionUpdaters.current;
        toolExecutionUpdaters.current = [];
        if (toolExecutionFlushTimer.current) {
            clearTimeout(toolExecutionFlushTimer.current);
            toolExecutionFlushTimer.current = null;
        }

        setRealtimeToolExecutions(prev => {
            const newMap = new Map(prev);
            updaters.forEach(update => update(newMap));
            return normalizeRealtimeToolExecutions(newMap);
        });
    }, []);

    const enqueueToolExecutionUpdate = useCallback((updater: (map: Map<string, ToolExecution[]>) => void) => {
        toolExecutionUpdaters.current.push(updater);
        if (!toolExecutionFlushTimer.current) {
            toolExecutionFlushTimer.current = window.setTimeout(flushToolExecutionUpdates, TOOL_EXEC_FLUSH_INTERVAL);
        }
    }, [flushToolExecutionUpdates]);

    const clearPendingCodeStreamCompletion = useCallback((completionKey?: string) => {
        if (!completionKey) return;
        const timer = codeStreamCompletionTimersRef.current.get(completionKey);
        if (timer) {
            clearTimeout(timer);
            codeStreamCompletionTimersRef.current.delete(completionKey);
        }
    }, []);

    const clearAllPendingCodeStreamCompletions = useCallback(() => {
        for (const timer of codeStreamCompletionTimersRef.current.values()) {
            clearTimeout(timer);
        }
        codeStreamCompletionTimersRef.current.clear();
    }, []);

    const scheduleCodeStreamCompletion = useCallback((completionKey?: string) => {
        if (!completionKey) return;
        clearPendingCodeStreamCompletion(completionKey);
        const timer = window.setTimeout(() => {
            codeStreamCompletionTimersRef.current.delete(completionKey);
            enqueueToolExecutionUpdate((newMap) => {
                for (const [msgId, execs] of newMap.entries()) {
                    const idx = execs.findIndex((exec) => getToolExecutionCompletionKey(exec) === completionKey);
                    if (idx === -1) continue;

                    const current = execs[idx];
                    if (current.status !== 'running' || !current.isFinalizing) {
                        continue;
                    }

                    const nextExecs = [...execs];
                    nextExecs[idx] = {
                        ...current,
                        status: 'completed',
                        isFinalizing: false,
                    };
                    newMap.set(msgId, nextExecs);
                    break;
                }
            });
            flushToolExecutionUpdates();
        }, CODE_STREAM_COMPLETION_QUIET_MS);
        codeStreamCompletionTimersRef.current.set(completionKey, timer);
    }, [clearPendingCodeStreamCompletion, enqueueToolExecutionUpdate, flushToolExecutionUpdates]);

    const settleAllRunningToolExecutions = useCallback((status: 'completed' | 'failed') => {
        enqueueToolExecutionUpdate((newMap) => {
            for (const [msgId, execs] of newMap.entries()) {
                let changed = false;
                const nextExecs = execs.map((exec) => {
                    if (exec.status === 'running') {
                        changed = true;
                        if (status === 'completed' && exec.isCodeStream) {
                            const nextExec = {
                                ...exec,
                                isFinalizing: true,
                            };
                            scheduleCodeStreamCompletion(getToolExecutionCompletionKey(nextExec));
                            return nextExec;
                        }

                        clearPendingCodeStreamCompletion(getToolExecutionCompletionKey(exec));
                        const fallbackOutput = exec.output && exec.output.trim().length > 0
                            ? exec.output
                            : status === 'failed'
                                ? '工具执行已结束（错误或中断），未返回结果。'
                                : '工具执行已结束，未返回结果。';
                        return {
                            ...exec,
                            status,
                            isFinalizing: false,
                            output: fallbackOutput,
                        };
                    }
                    return exec;
                });
                if (changed) {
                    newMap.set(msgId, nextExecs);
                }
            }
        });
    }, [clearPendingCodeStreamCompletion, enqueueToolExecutionUpdate, scheduleCodeStreamCompletion]);

    const settleToolExecutionPrecisely = useCallback((params: {
        executionId?: string;
        toolCallId?: string;
        messageId?: string;
        status: 'completed' | 'failed';
        output?: string;
        metadata?: any;
    }) => {
        const { executionId, toolCallId, messageId, status, output, metadata } = params;

        enqueueToolExecutionUpdate((newMap) => {
            const mappedMsgId = (executionId && executionToMessageIdRef.current.get(executionId))
                || (toolCallId && toolCallToMessageIdRef.current.get(toolCallId))
                || messageId
                || null;

            const settleMatchedExecution = (msgId: string, execs: ToolExecution[], idx: number) => {
                const nextExecs = [...execs];
                const current = nextExecs[idx];
                const completionKey = getToolExecutionCompletionKey({
                    id: toolCallId || current.id,
                    executionId: executionId || current.executionId,
                });
                const fallbackOutput = (output && output.trim().length > 0)
                    ? output
                    : (current.output && current.output.trim().length > 0)
                        ? current.output
                        : status === 'failed'
                            ? '工具执行失败或被中断，未返回详细结果。'
                            : '工具执行完成，未返回详细结果。';

                if (status === 'completed' && current.isCodeStream) {
                    nextExecs[idx] = {
                        ...current,
                        id: toolCallId || current.id,
                        executionId: executionId || current.executionId,
                        status: 'running',
                        isFinalizing: true,
                        output: current.output,
                        metadata: metadata ?? current.metadata,
                    };
                    newMap.set(msgId, nextExecs);
                    scheduleCodeStreamCompletion(completionKey);
                    return;
                }

                clearPendingCodeStreamCompletion(completionKey);
                nextExecs[idx] = {
                    ...current,
                    id: toolCallId || current.id,
                    executionId: executionId || current.executionId,
                    status,
                    isFinalizing: false,
                    output: current.isCodeStream ? current.output : fallbackOutput,
                    metadata: metadata ?? current.metadata,
                };
                newMap.set(msgId, nextExecs);
            };

            let matched = false;
            const candidateEntries = mappedMsgId
                ? [[mappedMsgId, newMap.get(mappedMsgId) || []] as const]
                : Array.from(newMap.entries());

            for (const [msgId, execs] of candidateEntries) {
                const idx = execs.findIndex((exec) => {
                    if (executionId && exec.executionId === executionId) return true;
                    if (toolCallId && exec.id === toolCallId) return true;
                    return false;
                });

                if (idx !== -1) {
                    settleMatchedExecution(msgId, execs, idx);
                    matched = true;
                    break;
                }
            }

            if (!matched && mappedMsgId) {
                for (const [msgId, execs] of newMap.entries()) {
                    if (msgId === mappedMsgId) continue;

                    const idx = execs.findIndex((exec) => {
                        if (executionId && exec.executionId === executionId) return true;
                        if (toolCallId && exec.id === toolCallId) return true;
                        return false;
                    });

                    if (idx !== -1) {
                        settleMatchedExecution(msgId, execs, idx);
                        matched = true;
                        break;
                    }
                }
            }

            // Fallback: no exact match found, settle latest running under mapped/current message.
            if (!matched) {
                const fallbackMsgId = findLatestRunningToolExecutionMessageId(newMap, mappedMsgId);
                if (!fallbackMsgId) {
                    return;
                }

                const execs = newMap.get(fallbackMsgId) || [];
                const idx = execs.map(e => e.status).lastIndexOf('running');
                if (idx === -1) return;

                const nextExecs = [...execs];
                const current = nextExecs[idx];
                const completionKey = getToolExecutionCompletionKey({
                    id: toolCallId || current.id,
                    executionId: executionId || current.executionId,
                });
                const fallbackOutput = (output && output.trim().length > 0)
                    ? output
                    : (current.output && current.output.trim().length > 0)
                        ? current.output
                        : status === 'failed'
                            ? '工具执行失败或被中断，未返回详细结果。'
                            : '工具执行完成，未返回详细结果。';

                if (status === 'completed' && current.isCodeStream) {
                    nextExecs[idx] = {
                        ...current,
                        id: toolCallId || current.id,
                        executionId: executionId || current.executionId,
                        status: 'running',
                        isFinalizing: true,
                        output: current.output,
                        metadata: metadata ?? current.metadata,
                    };
                    newMap.set(fallbackMsgId, nextExecs);
                    scheduleCodeStreamCompletion(completionKey);
                    return;
                }

                clearPendingCodeStreamCompletion(completionKey);
                nextExecs[idx] = {
                    ...current,
                    id: toolCallId || current.id,
                    executionId: executionId || current.executionId,
                    status,
                    isFinalizing: false,
                    output: current.isCodeStream ? current.output : fallbackOutput,
                    metadata: metadata ?? current.metadata,
                };
                newMap.set(fallbackMsgId, nextExecs);
            }
        });
    }, [clearPendingCodeStreamCompletion, enqueueToolExecutionUpdate, scheduleCodeStreamCompletion]);

    const flushProgress = useCallback(() => {
        if (!pendingProgress.current) {
            if (progressFlushTimer.current) {
                clearTimeout(progressFlushTimer.current);
                progressFlushTimer.current = null;
            }
            return;
        }

        const progressData = pendingProgress.current;
        pendingProgress.current = null;
        if (progressFlushTimer.current) {
            clearTimeout(progressFlushTimer.current);
            progressFlushTimer.current = null;
        }

        startTransition(() => {
            setMessages(prev => {
                const targetIndex = currentAssistantMessageId.current
                    ? prev.findIndex((msg) => msg.id === currentAssistantMessageId.current)
                    : -1;
                const fallbackLast = splitBoundaryActiveRef.current ? undefined : prev[prev.length - 1];
                const targetMsg = targetIndex >= 0 ? prev[targetIndex] : fallbackLast;

                if (targetMsg && targetMsg.role === 'assistant') {
                    const nextTimeline = targetMsg.eventTimeline ? [...targetMsg.eventTimeline] : [];
                    const nextContentMark = targetMsg.content.length;
                    const progressIndex = nextTimeline.findIndex((event) => event.type === 'progress');

                    if (progressIndex >= 0) {
                        nextTimeline[progressIndex] = {
                            type: 'progress',
                            contentMark: nextContentMark,
                        };
                    } else {
                        nextTimeline.push({ type: 'progress', contentMark: nextContentMark });
                    }

                    return [
                        ...prev.slice(0, targetIndex >= 0 ? targetIndex : prev.length - 1),
                        {
                            ...targetMsg,
                            eventTimeline: nextTimeline,
                            progress: {
                                percentage: progressData.percentage,
                                details: progressData.details
                            }
                        },
                        ...prev.slice(targetIndex >= 0 ? targetIndex + 1 : prev.length)
                    ];
                }

                const newMsgId = createLocalEntityId('assistant-progress');
                currentAssistantMessageId.current = newMsgId;
                splitBoundaryActiveRef.current = false;
                return [
                    ...prev,
                    {
                        id: newMsgId,
                        role: 'assistant' as const,
                        content: '',
                        timestamp: Date.now(),
                        responseStartedAt: pendingChatRequestRef.current?.startedAt,
                        isStreaming: true,
                        eventTimeline: [{ type: 'progress', contentMark: 0 }],
                        progress: {
                            percentage: progressData.percentage,
                            details: progressData.details,
                        },
                    }
                ];
            });
        });
    }, []);

    const enqueueProgressUpdate = useCallback((progressData: { percentage: number; details: string }) => {
        pendingProgress.current = progressData;
        if (!progressFlushTimer.current) {
            progressFlushTimer.current = window.setTimeout(flushProgress, PROGRESS_FLUSH_INTERVAL);
        }
    }, [flushProgress]);

    const flushThoughts = useCallback(() => {
        if (pendingThoughts.current.length === 0) {
            if (thoughtFlushTimeout.current) {
                clearTimeout(thoughtFlushTimeout.current);
                thoughtFlushTimeout.current = null;
            }
            return;
        }

        // Chronological order: [Oldest ... Newest]
        const buffered = pendingThoughts.current;
        pendingThoughts.current = [];
        if (thoughtFlushTimeout.current) {
            clearTimeout(thoughtFlushTimeout.current);
            thoughtFlushTimeout.current = null;
        }

        setThoughts(prev => {
            // Merge logic: Check if the first item in buffered can be merged with the last item in prev
            let newThoughts = [...prev];
            const incoming = [...buffered];

            if (newThoughts.length > 0 && incoming.length > 0) {
                const lastThought = newThoughts[newThoughts.length - 1];
                const firstIncoming = incoming[0];

                // Merge if same type and source, and time difference is small (< 2s)
                if (lastThought.type === firstIncoming.type && 
                    lastThought.source === firstIncoming.source &&
                    (firstIncoming.timestamp - lastThought.timestamp < 2000)) {
                    
                    // Merge content
                    newThoughts[newThoughts.length - 1] = {
                        ...lastThought,
                        content: lastThought.content + firstIncoming.content,
                        // Update timestamp to latest? Or keep original start time? Keep start time.
                    };
                    // Remove first item from incoming since it's merged
                    incoming.shift();
                }
            }

            // If incoming still has items, append them (merging internally if needed)
            if (incoming.length > 0) {
                // Also merge internal consecutive items in incoming
                const mergedIncoming: Thought[] = [];
                incoming.forEach((item, index) => {
                    if (index === 0) {
                        mergedIncoming.push(item);
                    } else {
                        const last = mergedIncoming[mergedIncoming.length - 1];
                        if (last.type === item.type && 
                            last.source === item.source &&
                            (item.timestamp - last.timestamp < 2000)) {
                            mergedIncoming[mergedIncoming.length - 1] = {
                                ...last,
                                content: last.content + item.content
                            };
                        } else {
                            mergedIncoming.push(item);
                        }
                    }
                });
                newThoughts = [...newThoughts, ...mergedIncoming];
            }
            
            // Keep last 100 items (Chronological: [Oldest ... Newest])
            if (newThoughts.length > 100) {
                return newThoughts.slice(-100);
            }
            return newThoughts;
        });
    }, []);

    const cancelScheduledTokenFlush = useCallback(() => {
        if (rafId.current !== null) {
            cancelAnimationFrame(rafId.current);
            rafId.current = null;
        }
        if (tokenFlushTimer.current !== null) {
            clearTimeout(tokenFlushTimer.current);
            tokenFlushTimer.current = null;
        }
    }, []);

    // Track thinking block counts per message to build event timeline
    const messageThinkingCountRef = useRef<Map<string, number>>(new Map());

    const flushTokens = useCallback((force: boolean = false) => {
        if (!pendingTokens.current) {
            cancelScheduledTokenFlush();
            return;
        }

        const tokensToFlush = pendingTokens.current;
        pendingTokens.current = "";
        cancelScheduledTokenFlush();
        nextTokenFlushAtRef.current = force ? 0 : performance.now() + TOKEN_FLUSH_CADENCE_MS;

        const targetId = currentAssistantMessageId.current;

        const commitMessages = () => {
            setMessages(prev => {
                const targetIndex = targetId ? prev.findIndex(msg => msg.id === targetId) : -1;
                const fallbackLast = splitBoundaryActiveRef.current ? undefined : prev[prev.length - 1];
                const targetMsg = targetIndex >= 0 ? prev[targetIndex] : fallbackLast;

                if (targetMsg && targetMsg.role === 'assistant') {
                    if (currentAssistantMessageId.current !== targetMsg.id) {
                        currentAssistantMessageId.current = targetMsg.id;
                    }
                    bindPendingServerMessageId(targetMsg.id);
                    splitBoundaryActiveRef.current = false;

                    const nextContent = stripInjectedPromptSections(targetMsg.content + tokensToFlush, false);
                    
                    // Count thinking blocks in the new content (match both closed and unclosed thinking tags)
                    const prevThinkingCount = messageThinkingCountRef.current.get(targetMsg.id) || 0;
                    const newThinkingCount = countRenderableThinkingBlocks(nextContent);
                    
                    // Build event timeline: add thinking events for new blocks
                    let eventTimeline = targetMsg.eventTimeline ? [...targetMsg.eventTimeline] : [];
                    for (let i = prevThinkingCount; i < newThinkingCount; i++) {
                        eventTimeline.push({ type: 'thinking', index: i });
                    }
                    messageThinkingCountRef.current.set(targetMsg.id, newThinkingCount);

                    const nextMsg = {
                        ...targetMsg,
                        content: nextContent,
                        responseStartedAt: pendingChatRequestRef.current?.startedAt ?? targetMsg.responseStartedAt,
                        isStreaming: true,
                        isToolPlaceholder: targetMsg.isToolPlaceholder && nextContent.trim().length === 0,
                        eventTimeline
                    };

                    if (targetIndex >= 0) {
                        return [...prev.slice(0, targetIndex), nextMsg, ...prev.slice(targetIndex + 1)];
                    }

                    return [...prev.slice(0, -1), nextMsg];
                }

                const newMsg: Message = {
                    id: createLocalEntityId('assistant'),
                    role: 'assistant',
                    content: stripInjectedPromptSections(tokensToFlush, false),
                    timestamp: Date.now(),
                    responseStartedAt: pendingChatRequestRef.current?.startedAt,
                    isStreaming: true
                };
                
                // Count thinking blocks in initial content (match both closed and unclosed thinking tags)
                const thinkingCount = countRenderableThinkingBlocks(newMsg.content);
                messageThinkingCountRef.current.set(newMsg.id, thinkingCount);
                if (thinkingCount > 0) {
                    newMsg.eventTimeline = Array.from({ length: thinkingCount }, (_, i) => ({ type: 'thinking' as const, index: i }));
                }
                
                currentAssistantMessageId.current = newMsg.id;
                bindPendingServerMessageId(newMsg.id);
                splitBoundaryActiveRef.current = false;
                return [...prev, newMsg];
            });
        };

        commitMessages();
    }, [TOKEN_FLUSH_CADENCE_MS, bindPendingServerMessageId, cancelScheduledTokenFlush]);

    const appendToolTimelineEvent = useCallback((messageId: string, executionId: string) => {
        if (!messageId || !executionId) {
            return;
        }

        setMessages((prev) => {
            const msgIndex = prev.findIndex((msg) => msg.id === messageId);
            if (msgIndex === -1) {
                return prev;
            }

            const msg = prev[msgIndex];
            if (msg.role !== 'assistant') {
                return prev;
            }

            const eventTimeline = msg.eventTimeline ? [...msg.eventTimeline] : [];
            const existingIndex = eventTimeline.findIndex((event) => event.type === 'tool' && event.executionId === executionId);
            const contentMark = msg.content.length;

            if (existingIndex !== -1) {
                const existing = eventTimeline[existingIndex];
                if (existing.type === 'tool' && existing.contentMark === contentMark) {
                    return prev;
                }

                eventTimeline[existingIndex] = {
                    type: 'tool',
                    executionId,
                    contentMark: existing.type === 'tool' && typeof existing.contentMark === 'number'
                        ? existing.contentMark
                        : contentMark,
                };
            } else {
                eventTimeline.push({ type: 'tool', executionId, contentMark });
            }

            return [
                ...prev.slice(0, msgIndex),
                { ...msg, eventTimeline },
                ...prev.slice(msgIndex + 1),
            ];
        });
    }, []);

    const scheduleTokenFlush = useCallback(() => {
        if (rafId.current !== null || tokenFlushTimer.current !== null) {
            return;
        }

        const now = performance.now();
        if (nextTokenFlushAtRef.current <= now) {
            nextTokenFlushAtRef.current = now + TOKEN_FLUSH_CADENCE_MS;
        }

        const runAt = nextTokenFlushAtRef.current;
        const waitMs = Math.max(0, runAt - now);

        const tick = (frameNow: number) => {
            if (frameNow >= runAt) {
                flushTokens(false);
                return;
            }
            rafId.current = requestAnimationFrame(tick);
        };

        rafId.current = requestAnimationFrame(tick);
        tokenFlushTimer.current = window.setTimeout(() => {
            flushTokens(false);
        }, waitMs + TOKEN_FLUSH_CADENCE_MS);
    }, [TOKEN_FLUSH_CADENCE_MS, flushTokens]);

    const stripInjectedPromptSections = useCallback((input: string, trimEdges = true): string => {
        return stripInjectedChatControlContent(input, trimEdges);
    }, []);

    const stripHistoryControlLines = useCallback((input: string, trimEdges = true): string => {
        return stripInjectedPromptSections(input, trimEdges);
    }, [stripInjectedPromptSections]);

    const hasHistoryRetryMarker = useCallback((input: string): boolean => {
        return /(SYSTEM WARNING:|SYSTEM MONITOR:|THIS IS A FAILURE|RETRY IMMEDIATELY|Your previous turn ended without a usable final answer|output the actual RESPONSE or TOOL CALL|you did NOT emit any tool call|you MUST use a tool)/i.test(input);
    }, []);

    const isHistoryRecoveryFallback = useCallback((input: string): boolean => {
        const normalized = stripHistoryControlLines(input, false).trim();
        if (!normalized) {
            return false;
        }

        return /^我连续多次收到没有有效内容的回复/.test(normalized)
            || /^I(?:'ve| have) received multiple replies without valid content/i.test(normalized)
            || /^I received multiple replies without valid content/i.test(normalized);
    }, [stripHistoryControlLines]);

    const extractHistoryParts = useCallback((partsRaw: string): { textFragments: string[]; thinkingFragments: string[]; attachmentMetas: any[] } => {
        if (!partsRaw) return { textFragments: [], thinkingFragments: [], attachmentMetas: [] };

        try {
            const parsed = JSON.parse(partsRaw);
            const parts = Array.isArray(parsed) ? parsed : [parsed];
            const textFragments: string[] = [];
            const thinkingFragments: string[] = [];
            const attachmentMetas: any[] = [];

            parts.forEach((part: any) => {
                if (!part) return;
                if (typeof part === 'string') {
                    textFragments.push(part);
                    return;
                }
                if (part.type === 'text' && part.data && typeof part.data.text === 'string') {
                    textFragments.push(part.data.text);
                    return;
                }
                if ((part.type === 'reasoning' || part.type === 'thinking') && part.data && typeof part.data.thinking === 'string') {
                    thinkingFragments.push(part.data.thinking);
                    return;
                }
                if (part.type === 'attachment_meta' && part.data) {
                    attachmentMetas.push(part.data);
                }
            });

            return { textFragments, thinkingFragments, attachmentMetas };
        } catch {
            return { textFragments: [], thinkingFragments: [], attachmentMetas: [] };
        }
    }, []);

    const buildHistoryContent = useCallback((role: Message['role'], textFragments: string[], thinkingFragments: string[]): string => {
        if (role === 'user') {
            const normalizedUserText = textFragments
                .join('\n')
                .replaceAll(INTERNAL_HIDDEN_USER_PROMPT_MARKER, '');
            return stripHistoryControlLines(normalizedUserText).trim();
        }

        if (role !== 'assistant') {
            return '';
        }

        const thinking = stripHistoryControlLines(thinkingFragments.join('')).trim();
        const text = stripHistoryControlLines(textFragments.join('')).trim();
        const normalizedThinking = isMeaningfulRenderableChatText(thinking) ? thinking : '';
        const normalizedText = isMeaningfulRenderableChatText(text) ? text : '';

        if (normalizedThinking && normalizedText) {
            return `<thinking>${normalizedThinking}</thinking>\n\n${normalizedText}`;
        }
        if (normalizedThinking) {
            return `<thinking>${normalizedThinking}</thinking>`;
        }
        return normalizedText;
    }, [stripHistoryControlLines]);

    const normalizeHistoryTimestamp = useCallback((rawTimestamp?: number | null): number => {
        if (typeof rawTimestamp !== 'number' || !Number.isFinite(rawTimestamp) || rawTimestamp <= 0) {
            return Date.now();
        }
        return rawTimestamp > 1_000_000_000_000 ? rawTimestamp : rawTimestamp * 1000;
    }, []);

    const mapSessionRowsToMessages = useCallback((rows: SessionMessageRecord[]): Message[] => {
        type AggregatedHistoryMessage = {
            id: string;
            role: Message['role'];
            textFragments: string[];
            thinkingFragments: string[];
            attachmentMetas: any[];
            timestamp: number;
            hasPayload: boolean;
        };

        const aggregated: AggregatedHistoryMessage[] = [];
        let currentGroup: AggregatedHistoryMessage | null = null;
        let suppressAssistantRecovery = false;

        const flushCurrentGroup = () => {
            if (currentGroup && currentGroup.hasPayload) {
                aggregated.push(currentGroup);
            }
            currentGroup = null;
        };

        for (const row of rows) {
            const { textFragments, thinkingFragments, attachmentMetas } = extractHistoryParts(row.parts);
            const rawText = textFragments.join('');

            if (row.role === 'user' && isHiddenSyntheticUserPrompt(rawText)) {
                continue;
            }

            if (row.role !== 'user' && row.role !== 'assistant') {
                if (
                    currentGroup?.role === 'assistant'
                    && currentGroup.hasPayload
                    && rawText
                    && hasHistoryRetryMarker(rawText)
                ) {
                    flushCurrentGroup();
                    suppressAssistantRecovery = true;
                }
                continue;
            }

            const role: Message['role'] = row.role;
            const timestamp = normalizeHistoryTimestamp(row.created_at);
            const hasPayload = textFragments.length > 0 || thinkingFragments.length > 0 || attachmentMetas.length > 0;

            if (role !== 'assistant') {
                suppressAssistantRecovery = false;
            }

            if (role === 'assistant' && suppressAssistantRecovery) {
                continue;
            }

            if (role === 'assistant' && rawText && hasHistoryRetryMarker(rawText)) {
                if (currentGroup?.role === 'assistant' && currentGroup.hasPayload) {
                    flushCurrentGroup();
                    suppressAssistantRecovery = true;
                }
                continue;
            }

            if (
                role === 'assistant'
                && currentGroup?.role === 'assistant'
                && currentGroup.hasPayload
                && rawText
                && isHistoryRecoveryFallback(rawText)
            ) {
                continue;
            }

            if (!currentGroup || currentGroup.role !== role) {
                flushCurrentGroup();
                currentGroup = {
                    id: row.id,
                    role,
                    textFragments: [],
                    thinkingFragments: [],
                    attachmentMetas: [],
                    timestamp,
                    hasPayload: false
                };
            } else {
                currentGroup.timestamp = timestamp;
            }

            if (!hasPayload || !currentGroup) {
                continue;
            }

            currentGroup.textFragments.push(...textFragments);
            currentGroup.thinkingFragments.push(...thinkingFragments);
            currentGroup.attachmentMetas.push(...attachmentMetas);
            currentGroup.hasPayload = true;
        }

        flushCurrentGroup();

        return aggregated.reduce<Message[]>((acc, group) => {
            const content = buildHistoryContent(group.role, group.textFragments, group.thinkingFragments);
            if (!content.trim() && group.attachmentMetas.length === 0) {
                return acc;
            }

            acc.push({
                id: group.id,
                role: group.role,
                content,
                attachmentMetas: group.attachmentMetas,
                timestamp: group.timestamp
            });
            return acc;
        }, []);
    }, [buildHistoryContent, extractHistoryParts, hasHistoryRetryMarker, isHistoryRecoveryFallback, normalizeHistoryTimestamp]);

    const hydrateSessionMessages = useCallback(async (id: string, shouldApply?: () => boolean) => {
        const [rows, activePlanSnapshot] = await Promise.all([
            sessionService.getMessages(id, 10000),
            sessionService.getPlanSnapshot(id, 10000).catch(() => null),
        ]);
        const mapped = mapSessionRowsToMessages(rows);
        const restoredPlanSnapshotFromHistory = buildPlanSnapshotFromExecutions(
            rows.flatMap((row) => parseHistoryPlanExecutions(row.parts)),
        );
        const restoredPlanSnapshot = activePlanSnapshot
            ? applyPlanProgressExecution(null, {
                id: createLocalEntityId('plan-restore'),
                name: 'manage_plan',
                input: '',
                output: '',
                status: 'completed',
                metadata: activePlanSnapshot,
            })
            : restoredPlanSnapshotFromHistory;
        if (shouldApply && !shouldApply()) {
            return mapped;
        }
        setMessages(mapped);
        setPlanProgressSnapshot(restoredPlanSnapshot);
        setSessionId(id);
        sessionIdRef.current = id;
        return mapped;
    }, [mapSessionRowsToMessages]);

    useEffect(() => {
        wsClient.connect();

        const handleEvent = (event: ServerEvent) => {
            if (event.session_id) {
                setSessionId(prev => {
                    if (!prev) return event.session_id!;
                    return prev;
                });
            }

            // Handle token streaming
            if (event.type === 'token') {
                setIsGenerating(true);
                isStreamingRef.current = true;
                if (event.message_id) {
                    pendingServerMessageIdRef.current = event.message_id;
                }
                if (event.content !== undefined && pendingChatRequestRef.current && pendingChatRequestRef.current.firstTokenAt === undefined) {
                    pendingChatRequestRef.current.firstTokenAt = Date.now();
                }
                const tokenContent = event.content || '';
                if (pendingChatRequestRef.current && tokenContent) {
                    pendingChatRequestRef.current.outputChars += tokenContent.length;
                }
                pendingTokens.current += tokenContent;
                
                // Force flush if buffer gets too large to prevent huge jumps
                if (pendingTokens.current.length > MAX_PENDING_TOKEN_CHARS) {
                            cancelScheduledTokenFlush();
                     flushTokens(true);
                     return;
                }

                // Schedule flush if not already scheduled
                scheduleTokenFlush();
                return;
            }

            // Flush any pending tokens before handling other events
            if (pendingTokens.current) {
                cancelScheduledTokenFlush();
                flushTokens(true);
            }

            switch (event.type) {
                case 'progress':
                    if (event.data && event.session_id) {
                        const progressData = event.data as { percentage: number; details: string };
                        enqueueProgressUpdate(progressData);
                    }
                    break;

                case 'plan_snapshot':
                    if (event.session_id && sessionIdRef.current && event.session_id !== sessionIdRef.current) {
                        break;
                    }
                    if (event.data) {
                        setPlanProgressSnapshot((previous) => applyPlanProgressExecution(previous, {
                            id: event.tool_execution_id || createLocalEntityId('plan-snapshot'),
                            executionId: event.tool_execution_id,
                            name: 'manage_plan',
                            input: '',
                            output: '',
                            status: 'completed',
                            metadata: event.data,
                        }));
                    }
                    break;

                case 'word_editor_action':
                    if (event.data && typeof event.data === 'object') {
                        publishWordEditorAction(event.data as WordEditorActionPayload);
                    }
                    break;

                case 'split':
                    // Backend splits persisted assistant rows after thinking, but the UI should
                    // keep streaming subsequent正文 into the same live message so the main answer
                    // does not appear truncated across multiple bubbles.
                    splitBoundaryActiveRef.current = false;
                    break;

                case 'tool_call':
                    try {
                        if (event.content) {
                             let toolCalls: any[] = [];
                             try {
                                 const parsed = JSON.parse(event.content);
                                 if (Array.isArray(parsed)) toolCalls = parsed;
                                 else toolCalls = [parsed];
                             } catch {
                                 console.warn("Failed to parse tool call JSON", event.content);
                             }

                             if (toolCalls.length > 0) {
                                 const resolvedEventMessageId = resolveLocalMessageId(event.message_id);
                                 const mappedToolCallMsgId = toolCalls.reduce<string | undefined>((found, tc) => {
                                     if (found || !tc?.id) return found;
                                     return toolCallToMessageIdRef.current.get(tc.id);
                                 }, undefined);
                                 const mappedExecutionMsgId = event.tool_execution_id
                                     ? executionToMessageIdRef.current.get(event.tool_execution_id)
                                     : undefined;

                                 // Prefer known routing over the current assistant message to avoid duplicate cards
                                 // when the backend re-emits the same tool call in the final response.
                                 const fallbackTargetMsgId = splitBoundaryActiveRef.current
                                     ? undefined
                                     : currentAssistantMessageId.current || resolvedEventMessageId || undefined;
                                 let targetMsgId: string | undefined = mappedToolCallMsgId || mappedExecutionMsgId || fallbackTargetMsgId;

                                 // FIX: If targetMsgId is null, force-flush buffered tokens to create the message.
                                 // This handles the race where token is buffered (RAF pending) but tool_call arrives first.
                                 if (!targetMsgId && pendingTokens.current) {
                                                 cancelScheduledTokenFlush();
                                     flushTokens(true);
                                     targetMsgId = currentAssistantMessageId.current || undefined;
                                 }

                                 // FIX: Still null (no tokens arrived yet, e.g. after split or model calls tool without text).
                                 // Create a placeholder assistant message so the tool card has a home.
                                 // We use messagesRef for synchronous read to determine if a streaming
                                 // assistant message already exists, avoiding the async setMessages timing issue.
                                 if (!targetMsgId) {
                                     targetMsgId = ensureToolHostMessage();
                                 }

                                 if (targetMsgId) {
                                     bindServerMessageIdToLocal(event.message_id, targetMsgId);
                                     enqueueToolExecutionUpdate((newMap) => {
                                         const nextExecs = [...(newMap.get(targetMsgId!) || [])];

                                         toolCalls.forEach((tc: any) => {
                                             let idx = nextExecs.findIndex((e) => {
                                                 if (tc.id && e.id === tc.id) return true;
                                                 if (event.tool_execution_id && e.executionId === event.tool_execution_id) return true;
                                                 return false;
                                             });

                                             if (idx === -1) {
                                                 const toolInput = extractToolCallInput(tc);
                                                 nextExecs.push({
                                                     id: tc.id || createLocalEntityId('tool-call'),
                                                     executionId: event.tool_execution_id,
                                                     name: tc.name,
                                                     input: toolInput,
                                                     output: '',
                                                     status: 'running',
                                                     isCodeStream: isCodeStreamTool(tc.name)
                                                 });
                                             } else {
                                                 const toolInput = extractToolCallInput(tc);
                                                 nextExecs[idx] = {
                                                     ...nextExecs[idx],
                                                     executionId: event.tool_execution_id || nextExecs[idx].executionId,
                                                     name: tc.name || nextExecs[idx].name,
                                                     input: toolInput || nextExecs[idx].input,
                                                     status: 'running',
                                                     isCodeStream: isCodeStreamTool(tc.name || nextExecs[idx].name) || nextExecs[idx].isCodeStream
                                                 };
                                             }

                                             if (tc.id) {
                                                 toolCallToMessageIdRef.current.set(tc.id, targetMsgId!);
                                             }
                                             if (event.tool_execution_id) {
                                                 executionToMessageIdRef.current.set(event.tool_execution_id, targetMsgId!);
                                             }
                                         });

                                         newMap.set(targetMsgId!, nextExecs);
                                     });

                                     flushToolExecutionUpdates();

                                    toolCalls.forEach((tc: any) => {
                                        const toolTimelineKey = event.tool_execution_id || tc?.id;
                                        if (targetMsgId && toolTimelineKey) {
                                            appendToolTimelineEvent(targetMsgId, toolTimelineKey);
                                        }
                                        if (tc?.name && isPlanProgressTool({ name: tc.name }) && !(event.session_id && sessionIdRef.current && event.session_id !== sessionIdRef.current)) {
                                            setPlanProgressSnapshot((previous) => applyPlanProgressExecution(previous, {
                                                id: tc.id || createLocalEntityId('plan-tool-call'),
                                                executionId: event.tool_execution_id,
                                                name: tc.name,
                                                input: extractToolCallInput(tc),
                                                output: '',
                                                status: 'running',
                                            }));
                                        }
                                    });
                                 }
                             }
                        }
                    } catch (e) {
                        console.error("Failed to parse tool call for status", e);
                    }
                    break;

                case 'tool_output':
                    if (event.content) {
                        const resolvedEventMessageId = resolveLocalMessageId(event.message_id);
                        const mappedMsgId = (event.tool_execution_id && executionToMessageIdRef.current.get(event.tool_execution_id))
                            || (splitBoundaryActiveRef.current ? undefined : resolvedEventMessageId || currentAssistantMessageId.current);
                        if (mappedMsgId) {
                            bindServerMessageIdToLocal(event.message_id, mappedMsgId);
                            enqueueToolExecutionUpdate((newMap) => {
                                const nextExecs = [...(newMap.get(mappedMsgId) || [])];
                                const activeIdx = event.tool_execution_id
                                    ? nextExecs.findIndex(e => e.executionId === event.tool_execution_id)
                                    : nextExecs.map(e => e.status).lastIndexOf('running');
                                if (activeIdx !== -1) {
                                    if (nextExecs[activeIdx].status !== 'running') {
                                        return;
                                    }
                                    nextExecs[activeIdx] = {
                                        ...nextExecs[activeIdx],
                                        output: nextExecs[activeIdx].output + event.content
                                    };
                                    if (nextExecs[activeIdx].isFinalizing && nextExecs[activeIdx].isCodeStream) {
                                        scheduleCodeStreamCompletion(getToolExecutionCompletionKey(nextExecs[activeIdx]));
                                    }
                                    newMap.set(mappedMsgId, nextExecs);
                                }
                            });

                            if (event.tool_execution_id) {
                                appendToolTimelineEvent(mappedMsgId, event.tool_execution_id);
                            }
                        }
                    }
                    break;

                case 'code_stream':
                    if (event.content !== undefined) {
                        const tool_call_id = event.data?.tool_call_id;
                        const tool_index = event.data?.tool_index ?? event.data?.index;
                        const tool_name = event.data?.tool_name ?? event.data?.name;
                        const tool_input = typeof event.data?.tool_input === 'string' ? event.data.tool_input : '';
                        const resolvedEventMessageId = resolveLocalMessageId(event.message_id);
                        let mappedMsgId = (tool_call_id && toolCallToMessageIdRef.current.get(tool_call_id))
                            || (event.tool_execution_id && executionToMessageIdRef.current.get(event.tool_execution_id))
                            || (splitBoundaryActiveRef.current ? undefined : resolvedEventMessageId || currentAssistantMessageId.current);
                        const initiallyMappedMsgId = mappedMsgId;

                        if (!mappedMsgId && pendingTokens.current) {
                            cancelScheduledTokenFlush();
                            flushTokens(true);
                            mappedMsgId = currentAssistantMessageId.current || undefined;
                        }

                        if (!mappedMsgId) {
                            mappedMsgId = ensureToolHostMessage();
                        }

                        if (mappedMsgId) {
                            const shouldFlushCodeStreamFrame = !initiallyMappedMsgId || !hasRealtimeToolExecution(realtimeToolExecutionsRef.current, mappedMsgId, {
                                executionId: event.tool_execution_id,
                                toolCallId: tool_call_id,
                                toolIndex: typeof tool_index === 'number' ? tool_index : undefined,
                                toolName: typeof tool_name === 'string' ? tool_name : undefined,
                            });

                            bindServerMessageIdToLocal(event.message_id, mappedMsgId);
                            enqueueToolExecutionUpdate((newMap) => {
                                const nextExecs = [...(newMap.get(mappedMsgId) || [])];

                                let idx = -1;
                                if (event.tool_execution_id) {
                                    idx = nextExecs.findIndex(e => e.executionId === event.tool_execution_id);
                                }
                                if (tool_call_id) {
                                    if (idx === -1) {
                                        idx = nextExecs.findIndex(e => e.id === tool_call_id);
                                    }
                                }
                                
                                if (idx === -1 && tool_index !== undefined) {
                                    if (nextExecs[tool_index] && (tool_name === undefined || nextExecs[tool_index].name === tool_name)) {
                                        idx = tool_index;
                                    }
                                }

                                if (idx === -1) {
                                    idx = nextExecs.map(e => e.status).lastIndexOf('running');
                                }

                                if (idx === -1 && tool_name) {
                                    const stableId = tool_call_id || createLocalEntityId(tool_index !== undefined ? `tool-index-${tool_index}` : 'tool-stream');
                                    const existingIdx = nextExecs.findIndex(e => e.id === stableId);
                                    if (existingIdx !== -1) {
                                        idx = existingIdx;
                                    } else {
                                        nextExecs.push({
                                            id: stableId,
                                            executionId: event.tool_execution_id,
                                            name: tool_name,
                                            input: '',
                                            output: '',
                                            status: 'running',
                                            isCodeStream: isCodeStreamTool(tool_name)
                                        });
                                        idx = nextExecs.length - 1;
                                    }
                                }
                                
                                if (idx !== -1) {
                                    if (nextExecs[idx].status !== 'running') {
                                        return;
                                    }
                                    const isCodeTool = isCodeStreamTool(nextExecs[idx].name);
                                    const mergedInput = tool_input || ((nextExecs[idx].input || '') + (event.content || ''));
                                    nextExecs[idx] = {
                                        ...nextExecs[idx],
                                        id: tool_call_id || nextExecs[idx].id,
                                        executionId: event.tool_execution_id || nextExecs[idx].executionId,
                                        input: mergedInput,
                                        output: isCodeTool ? (nextExecs[idx].output + (event.content || '')) : nextExecs[idx].output,
                                        isCodeStream: isCodeTool || nextExecs[idx].isCodeStream
                                    };
                                    if (nextExecs[idx].isFinalizing && nextExecs[idx].isCodeStream) {
                                        scheduleCodeStreamCompletion(getToolExecutionCompletionKey(nextExecs[idx]));
                                    }
                                    if (tool_call_id) {
                                        toolCallToMessageIdRef.current.set(tool_call_id, mappedMsgId);
                                    }
                                    if (event.tool_execution_id) {
                                        executionToMessageIdRef.current.set(event.tool_execution_id, mappedMsgId);
                                    }
                                    newMap.set(mappedMsgId, nextExecs);
                                }
                            });

                            if (shouldFlushCodeStreamFrame) {
                                flushToolExecutionUpdates();
                            }
                        }
                    }
                    break;

                case 'tool_result':
                    if (event.content || event.tool_result) {
                        let resultId = "";
                        let resultOutput = "";
                        let isError = false;
                        let metadata: any = null;

                        if (event.tool_result) {
                            resultId = event.tool_result.tool_call_id;
                            resultOutput = event.tool_result.content;
                            isError = event.tool_result.is_error;
                            metadata = event.tool_result.metadata;
                        } else if (event.content) {
                            try {
                                const res = JSON.parse(event.content);
                                resultId = res.tool_call_id;
                                resultOutput = res.content;
                                isError = res.is_error;
                                metadata = res.metadata;
                            } catch {
                                resultOutput = event.content;
                            }
                        }

                        const resolvedEventMessageId = resolveLocalMessageId(event.message_id);
                        const toolTimelineMsgId = (event.tool_execution_id && executionToMessageIdRef.current.get(event.tool_execution_id))
                            || (resultId && toolCallToMessageIdRef.current.get(resultId))
                            || resolvedEventMessageId
                            || undefined;
                        const toolTimelineKey = event.tool_execution_id || resultId || undefined;
                        if (toolTimelineMsgId && toolTimelineKey) {
                            appendToolTimelineEvent(toolTimelineMsgId, toolTimelineKey);
                        }

                        settleToolExecutionPrecisely({
                            executionId: event.tool_execution_id,
                            toolCallId: resultId || undefined,
                            messageId: resolvedEventMessageId,
                            status: isError ? 'failed' : 'completed',
                            output: resultOutput,
                            metadata,
                        });

                        flushToolExecutionUpdates();

                        // Cleanup mapping after terminal result to prevent stale routing
                        if (event.tool_execution_id) {
                            executionToMessageIdRef.current.delete(event.tool_execution_id);
                        }
                        if (resultId) {
                            toolCallToMessageIdRef.current.delete(resultId);
                        }
                    }
                    break;

                case 'notification':
                    {
                        let title: unknown;
                        let content: unknown;

                        if (event.data && typeof event.data === 'object') {
                            const payload = event.data as Record<string, unknown>;
                            title = payload.title;
                            content = payload.content ?? payload.message;
                        } else if (event.content) {
                            try {
                                const parsed = JSON.parse(event.content) as Record<string, unknown>;
                                title = parsed.title;
                                content = parsed.content ?? parsed.message;
                            } catch {
                                content = event.content;
                            }
                        }

                        const safeTitle = typeof title === 'string' && title.trim() ? title : '通知';
                        const safeContent = typeof content === 'string'
                            ? content
                            : content == null
                                ? ''
                                : JSON.stringify(content);
                        const normalizedContent = safeContent.trim();

                        // Ignore empty notification payloads to avoid blank popups.
                        if (!normalizedContent) {
                            break;
                        }

                        addNotification(safeTitle, normalizedContent);
                    }
                    break;

                case 'usage':
                    if (event.usage) {
                        setTokenUsage(event.usage);
                        // Compute delta vs last event in this run to avoid double-counting cumulative values.
                        const last = lastRunUsageRef.current;
                        const delta = {
                            input_tokens:  event.usage.input_tokens  - last.input_tokens,
                            output_tokens: event.usage.output_tokens - last.output_tokens,
                            total_tokens:  event.usage.total_tokens  - last.total_tokens,
                        };
                        lastRunUsageRef.current = event.usage;
                        setSessionTokenUsage(prev => ({
                            input_tokens:  prev.input_tokens  + delta.input_tokens,
                            output_tokens: prev.output_tokens + delta.output_tokens,
                            total_tokens:  prev.total_tokens  + delta.total_tokens,
                        }));
                    }
                    break;

                case 'permission_request':
                    if (event.data) {
                        setPermissionRequest(event.data as PermissionRequest);
                    }
                    break;

                case 'context_stats':
                    if (event.data) {
                        if (event.session_id && sessionIdRef.current && event.session_id !== sessionIdRef.current) {
                            break;
                        }
                        const nextContextStats = normalizeContextStats(event.data);
                        if (nextContextStats) {
                            setContextStats(nextContextStats);
                        }
                    }
                    break;

                case 'thought':
                    if (event.data) {
                        const thoughtData = event.data as unknown as ThoughtEventData;
                        const newThought: Thought = {
                            id: createLocalEntityId('thought'),
                            timestamp: Date.now(),
                            type: thoughtData.type,
                            content: thoughtData.content,
                            source: thoughtData.source
                        };

                        pendingThoughts.current.push(newThought);
                        if (!thoughtFlushTimeout.current) {
                            thoughtFlushTimeout.current = window.setTimeout(flushThoughts, THOUGHT_FLUSH_INTERVAL);
                        }
                    }
                    break;
                    
                case 'git_status_update': // Updated to match ws.ts EventType
                    try {
                        const payload = event.data ?? (event.content ? JSON.parse(event.content) : null);
                        if (Array.isArray(payload)) {
                            setGitStatusUpdates(payload);
                            setFileWatchPaths(payload.map(item => item.path));
                        } else if (payload && typeof payload === 'object') {
                            const typedPayload = payload as GitStatusUpdatePayload;
                            const updates = Array.isArray(typedPayload.updates) ? typedPayload.updates : [];
                            const paths = Array.isArray(typedPayload.paths) ? typedPayload.paths : updates.map(item => item.path);
                            setGitStatusUpdates(updates);
                            setFileWatchPaths(paths);
                        } else {
                            setGitStatusUpdates([]);
                            setFileWatchPaths([]);
                        }
                    } catch (e) {
                        console.error("Failed to parse git status", e);
                    }
                    break;

                case 'done':
                    setIsGenerating(false);
                    isStreamingRef.current = false;
                    if (pendingTokens.current) {
                        cancelScheduledTokenFlush();
                        flushTokens(true);
                    }
                    settleAllRunningToolExecutions('completed');
                    window.setTimeout(() => {
                        toolCallToMessageIdRef.current.clear();
                        executionToMessageIdRef.current.clear();
                    }, 800);
                    flushToolExecutionUpdates();
                    flushProgress();

                    const lastAssistantMessage = messagesRef.current[messagesRef.current.length - 1];
                    const shouldDropToolPlaceholder = Boolean(
                        lastAssistantMessage
                        && lastAssistantMessage.role === 'assistant'
                        && lastAssistantMessage.isToolPlaceholder
                        && !lastAssistantMessage.content.trim(),
                    );
                    const wasCanceled = stopRequestedRef.current;

                    if (shouldDropToolPlaceholder && lastAssistantMessage) {
                        const placeholderId = lastAssistantMessage.id;
                        setMessages((prev) => prev.filter((msg) => msg.id !== placeholderId));
                        setRealtimeToolExecutions((prev) => {
                            const next = new Map(prev);
                            next.delete(placeholderId);
                            return normalizeRealtimeToolExecutions(next);
                        });
                        currentAssistantMessageId.current = null;
                    } else {
                    const responseStartedAt = pendingChatRequestRef.current?.startedAt;
                    setMessages(prev => {
                        const lastMsg = prev[prev.length - 1];
                        if (lastMsg && lastMsg.role === 'assistant' && lastMsg.isStreaming) {
                            const isCurrentResponse = responseStartedAt !== undefined
                                && lastMsg.responseStartedAt === responseStartedAt;
                            const responseTimeMs = !wasCanceled && isCurrentResponse
                                ? Math.max(0, Date.now() - responseStartedAt)
                                : undefined;
                            return [
                                ...prev.slice(0, -1),
                                {
                                    ...lastMsg,
                                    isStreaming: false,
                                    ...(responseTimeMs !== undefined ? { responseTimeMs } : {}),
                                }
                            ];
                        }
                        return prev;
                    });
                    }

                    splitBoundaryActiveRef.current = false;
                    settlePendingChatRequest({ status: wasCanceled ? 'canceled' : 'success' });
                    if (!wasCanceled) {
                        notifyTaskCompleteIfAway();
                    }
                    stopRequestedRef.current = false;
                    break;

                case 'error':
                    setIsGenerating(false);
                    isStreamingRef.current = false;
                    if (pendingTokens.current) {
                        cancelScheduledTokenFlush();
                        flushTokens(true);
                    }
                    // Prefer exact settling when server provides execution identity.
                    if (event.tool_execution_id) {
                        settleToolExecutionPrecisely({
                            executionId: event.tool_execution_id,
                            status: 'failed',
                            output: event.content || '工具执行失败或被中断。',
                        });
                    } else {
                        settleAllRunningToolExecutions('failed');
                    }
                    flushToolExecutionUpdates();
                    flushProgress();

                    if (stopRequestedRef.current || isCancellationEvent(event)) {
                        setFixedErrorMessage(null);
                        settlePendingChatRequest({
                            status: 'canceled',
                            errorMessage: getErrorEventText(event) || '任务已取消',
                            errorEvent: event,
                        });
                        stopRequestedRef.current = false;
                        break;
                    }

                    {
                        const chatErrorMessage = buildChatErrorMessage(event);
                        const errorCode = getKnownChatErrorCode(event);
                        const suppressErrorUi = pendingChatRequestRef.current?.options.suppressErrorUi ?? false;
                        if (!suppressErrorUi) {
                            showFixedChatError(chatErrorMessage);
                            appendAssistantNotice(chatErrorMessage);
                        }
                        settlePendingChatRequest({
                            status: 'error',
                            errorCode,
                            errorMessage: chatErrorMessage,
                            errorEvent: event,
                        });
                    }
                    break;

                case 'config_updated':
                    if (event.data) {
                        const config = event.data as { agent_mode?: string };
                        if (config.agent_mode) {
                            setAgentMode(config.agent_mode);
                        }
                    }
                    break;

                case 'read_only_updated':
                    if (event.data && typeof event.data === 'object') {
                        const data = event.data as { read_only_mode?: unknown };
                        if (typeof data.read_only_mode === 'boolean') {
                            setServerReadOnlyMode(data.read_only_mode);
                        }
                    }
                    if (event.session_id) {
                        setSessionId(prev => prev || event.session_id || '');
                    }
                    break;
            }
        };

        // Use subscribe method from wsClient
        const unsubscribe = wsClient.subscribe(handleEvent);

        return () => {
            unsubscribe();
            cancelScheduledTokenFlush();
            clearAllPendingCodeStreamCompletions();
            if (toolExecutionFlushTimer.current) {
                clearTimeout(toolExecutionFlushTimer.current);
                toolExecutionFlushTimer.current = null;
            }
            if (progressFlushTimer.current) {
                clearTimeout(progressFlushTimer.current);
                progressFlushTimer.current = null;
            }
        };
    }, [t, sessionId, flushTokens, flushToolExecutionUpdates, enqueueToolExecutionUpdate, settleAllRunningToolExecutions, settleToolExecutionPrecisely, flushProgress, enqueueProgressUpdate, addNotification, hydrateSessionMessages, ensureToolHostMessage, appendAssistantNotice, appendToolTimelineEvent, bindServerMessageIdToLocal, buildChatErrorMessage, resolveLocalMessageId, showFixedChatError, settlePendingChatRequest, clearAllPendingCodeStreamCompletions, scheduleCodeStreamCompletion]);

    const sendMessage = useCallback(async (
        content: string,
        attachments: File[] = [],
        mode: string = 'normal',
        model?: string,
        options: SendMessageOptions = {},
    ): Promise<ChatSendResult> => {
        const normalizedOptions: Required<SendMessageOptions> = {
            appendUserMessage: options.appendUserMessage ?? true,
            enableSubagents: options.enableSubagents ?? true,
            suppressErrorUi: options.suppressErrorUi ?? false,
            replaceLatestAssistantTurn: options.replaceLatestAssistantTurn ?? false,
        };

        if (pendingChatRequestRef.current) {
            pendingChatRequestRef.current.resolve({
                status: 'error',
                errorMessage: 'CHAT_REQUEST_REPLACED',
            });
            pendingChatRequestRef.current = null;
        }

        loadSessionSequenceRef.current += 1;

        if (normalizedOptions.replaceLatestAssistantTurn) {
            removeLatestAssistantTurnForRetry();
        }

        // Clear previous assistant message ID so we don't append to old message
        currentAssistantMessageId.current = null;
        splitBoundaryActiveRef.current = false;
        toolCallToMessageIdRef.current.clear();
        executionToMessageIdRef.current.clear();
        serverMessageIdToLocalMessageIdRef.current.clear();
        pendingServerMessageIdRef.current = null;
        isStreamingRef.current = false;
        stopRequestedRef.current = false;
        setFixedErrorMessage(null);
        setContextStats(null);
        // Reset per-run usage tracker so each message's usage is computed from zero delta.
        lastRunUsageRef.current = {input_tokens: 0, output_tokens: 0, total_tokens: 0};
        
        // Sanitize content for encoding issues
        const sanitizedContent = sanitizeUTF8(content);
        const wordEditorState = getWordEditorStateSnapshot();
        const activeWordDocument = wordEditorState.document;
        const isWordEditorOpen = wordEditorState.isOpen;
        const activeGoWordSessionId = wordEditorState.goWordSessionId;
        const lastWordApplyError = wordEditorState.lastApplyError?.replace(/\s+/g, ' ').trim() ?? '';
        const lastWordApplyErrorAt = wordEditorState.lastApplyErrorAt;
        const activeWordPath = resolveWordDocumentPath(activeWordDocument);
        const shouldBindWordDocument = Boolean(
            isWordEditorOpen && activeWordDocument && activeWordDocument.source !== 'mock' && activeWordPath,
        );

        let outgoingContent = sanitizedContent;
        if (shouldBindWordDocument && activeWordDocument) {
            const wordContextBlock = [
                '[WORD_CONTEXT]',
                `active_document_name: ${activeWordDocument.name}`,
                `active_document_path: ${activeWordPath}`,
                activeGoWordSessionId ? `active_document_session_id: ${activeGoWordSessionId}` : 'active_document_session_id: unavailable',
                lastWordApplyError ? `last_apply_error: ${lastWordApplyError}` : 'last_apply_error: none',
                lastWordApplyErrorAt ? `last_apply_error_at: ${new Date(lastWordApplyErrorAt).toISOString()}` : 'last_apply_error_at: none',
                'editing_policy: interface_only',
                'read_policy: 先用 read 处理 docx 全文理解、结构通读和模糊搜索。大文档优先用 read 的 skeleton/search/full 路径拿整体上下文，避免第一次就把 auramate-word-reader 当全文阅读器导致结果过大被截断。',
                'reader_policy: 只有在 read 之后仍需要真实 paragraph_id/table_id、当前 go-word 会话定位、相对定位消歧、表格值单元格确认或样式证据时，再调用 auramate-word-reader。优先传 active_document_session_id 做聚焦读取；修改后如果需要确认结果，继续读取同一个 session_id。',
                'notes: 当前右侧 Word 面板已打开该文档。涉及该文档的编辑操作只能通过 go-word 接口完成；推荐顺序是 read -> 按需 auramate-word-reader -> auramate-word-copilot。',
                'hard_rules: 1) 只允许使用 read、auramate-word-reader、auramate-word-copilot 三类工具处理该文档；2) 修改前先用 read 获取全文或结构上下文；3) 若目标仍不够精确，或需要真实 locator / 样式证据，再调用 auramate-word-reader；4) 修改时必须使用 auramate-word-copilot / go-word 接口；5) 禁止使用 apply_patch、write_file、edit_file、create_file、shell 直接改写该文档；6) 禁止通过其他通用文件编辑方式变更该文档。',
                '[/WORD_CONTEXT]',
                '',
                sanitizedContent,
            ].join('\n');

            outgoingContent = sanitizeUTF8(wordContextBlock);
        }
        
        // Convert Files to wire format.
        // Prefer absolute file paths for normal attachments, but keep a binary fallback
        // for clipboard screenshots that do not exist on disk yet.
        const wireAttachments = await Promise.all(attachments.map(async (file, index) => {
            const fileName = file.name || `attachment_${Date.now()}_${index + 1}`;
            let filePath = fileName;
            let hasAbsolutePath = false;

            if ((file as any)._absolutePath) {
                filePath = (file as any)._absolutePath;
                hasAbsolutePath = true;
            } else if ('path' in file && (file as any).path) {
                filePath = (file as any).path;
                hasAbsolutePath = true;
            }

            let content: number[] = [];
            if (!hasAbsolutePath) {
                try {
                    const arrayBuffer = await file.arrayBuffer();
                    content = Array.from(new Uint8Array(arrayBuffer));
                    console.log('[Attachment] In-memory fallback enabled for:', fileName, '| Bytes:', content.length);
                } catch (error) {
                    console.warn('[Attachment] Failed to read in-memory attachment:', fileName, error);
                }
            }

            console.log('[Attachment] File:', fileName, '| Path:', filePath, '| HasAbsolutePath:', hasAbsolutePath);

            return {
                FilePath: filePath,
                FileName: fileName,
                MimeType: file.type || 'application/octet-stream',
                Content: content,
            };
        }));

        if (shouldBindWordDocument && activeWordDocument) {
            const existsInAttachments = wireAttachments.some((attachment) => {
                return attachment.FilePath === activeWordPath || attachment.FileName === activeWordDocument.name;
            });

            if (!existsInAttachments) {
                wireAttachments.push({
                    FilePath: activeWordPath,
                    FileName: activeWordDocument.name,
                    MimeType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
                    Content: [],
                });
            }
        }
        
        console.log('[Attachment] Sending attachments:', wireAttachments);
        
        if (normalizedOptions.appendUserMessage) {
            const userMsg: Message = {
                id: createLocalEntityId('user'),
                role: 'user',
                content: sanitizedContent,
                timestamp: Date.now(),
                attachments: attachments
                    .filter((a) => a.type.startsWith('image/'))
                    .map((a) => URL.createObjectURL(a)),
                attachmentMetas: attachments.map(a => ({
                    name: a.name,
                    type: a.type,
                    url: a.type.startsWith('image/') ? URL.createObjectURL(a) : undefined
                }))
            };
            setMessages(prev => [...prev, userMsg]);
        }
        setIsGenerating(true);

        const msg: ClientMessage = {
            type: 'chat',
            content: outgoingContent,
            session_id: sessionId,
            attachments: wireAttachments,
            enable_subagents: normalizedOptions.enableSubagents,
            mode,
            model,
        };
        return new Promise<ChatSendResult>((resolve) => {
            pendingChatRequestRef.current = {
                resolve,
                options: normalizedOptions,
                model,
                startedAt: Date.now(),
                outputChars: 0,
            };

            try {
                wsClient.send(msg);
            } catch (error) {
                const fallbackMessage = error instanceof Error ? error.message : '消息发送失败';
                if (!normalizedOptions.suppressErrorUi) {
                    showFixedChatError(fallbackMessage);
                    appendAssistantNotice(fallbackMessage);
                }
                settlePendingChatRequest({
                    status: 'error',
                    errorMessage: fallbackMessage,
                });
            }
        });
    }, [appendAssistantNotice, removeLatestAssistantTurnForRetry, sessionId, settlePendingChatRequest, showFixedChatError]);

    const respondPermission = useCallback((action: 'grant' | 'deny') => {
        if (!permissionRequest) return;
        wsClient.send({
            type: 'permission_response',
            permission_id: permissionRequest.id,
            action,
            session_id: sessionId
        });
        setPermissionRequest(null);
    }, [sessionId, permissionRequest]);

    const interruptGeneration = useCallback(() => {
        if (sessionId) {
            stopRequestedRef.current = true;
            setFixedErrorMessage(null);
            wsClient.send({
                type: 'stop',
                session_id: sessionId
            });
            setIsGenerating(false);
            settlePendingChatRequest({ status: 'canceled', errorMessage: '任务已取消' });
        }
    }, [sessionId, settlePendingChatRequest]);

    const stop = interruptGeneration;

    const cancelTool = useCallback((executionId: string) => {
        if (sessionId && executionId) {
            wsClient.send({
                type: 'cancel_tool',
                session_id: sessionId,
                tool_execution_id: executionId
            });
        }
    }, [sessionId]);

    const setSessionReadOnlyMode = useCallback((enabled: boolean) => {
        wsClient.send({
            type: 'set_read_only',
            session_id: sessionId || '',
            read_only_mode: enabled,
        });
        return true;
    }, [sessionId]);
    
    const resetSession = useCallback(() => {
        setMessages([]);
        setFixedErrorMessage(null);
        stopRequestedRef.current = false;
        clearAllPendingCodeStreamCompletions();
        const emptyToolExecutions = new Map<string, ToolExecution[]>();
        setRealtimeToolExecutions(emptyToolExecutions);
        setPlanProgressSnapshot(null);
        setSessionTokenUsage({input_tokens: 0, output_tokens: 0, total_tokens: 0});
        setContextStats(null);
        lastRunUsageRef.current = {input_tokens: 0, output_tokens: 0, total_tokens: 0};
        loadSessionSequenceRef.current += 1;
        setSessionId('');
        sessionIdRef.current = '';
        currentAssistantMessageId.current = null;
        splitBoundaryActiveRef.current = false;
        toolCallToMessageIdRef.current.clear();
        executionToMessageIdRef.current.clear();
        serverMessageIdToLocalMessageIdRef.current.clear();
        pendingServerMessageIdRef.current = null;
    }, [clearAllPendingCodeStreamCompletions]);

    const loadSession = useCallback(async (id: string, options?: LoadSessionOptions) => {
        const requestSequence = loadSessionSequenceRef.current + 1;
        loadSessionSequenceRef.current = requestSequence;
        const isCurrentRequest = () => loadSessionSequenceRef.current === requestSequence;

        try {
            setIsGenerating(false);
            setFixedErrorMessage(null);
            clearAllPendingCodeStreamCompletions();
            currentAssistantMessageId.current = null;
            splitBoundaryActiveRef.current = false;
            toolCallToMessageIdRef.current.clear();
            executionToMessageIdRef.current.clear();
            serverMessageIdToLocalMessageIdRef.current.clear();
            pendingServerMessageIdRef.current = null;
            setContextStats(null);
            pendingTokens.current = '';
            const emptyToolExecutions = new Map<string, ToolExecution[]>();
            setRealtimeToolExecutions(emptyToolExecutions);
            setPlanProgressSnapshot(null);
            setCurrentOperation(null);
            setActiveOperations(new Map());
            await hydrateSessionMessages(id, isCurrentRequest);
            if (!isCurrentRequest()) {
                return;
            }
            try {
                const stats = await sessionService.getContextStats(id, options, 10000);
                if (isCurrentRequest()) {
                    setContextStats(normalizeContextStats(stats));
                }
            } catch (statsError) {
                if (isCurrentRequest()) {
                    console.warn('Failed to load session context stats:', statsError);
                }
            }
        } catch (error) {
            if (!isCurrentRequest()) {
                return;
            }
            console.error('Failed to load session messages:', error);
            toast.error('加载历史会话失败');
            throw error;
        }
    }, [clearAllPendingCodeStreamCompletions, hydrateSessionMessages]);

    const addLocalMessage = useCallback((role: string, content: string) => {
        const msg: Message = {
            id: createLocalEntityId(role || 'message'),
            role: role as any,
            content,
            timestamp: Date.now()
        };
        setMessages(prev => [...prev, msg]);
    }, []);

    const windowedMessages = useMemo(() => {
        if (messages.length <= STREAM_WINDOW_SIZE) return messages;
        return messages.slice(-STREAM_WINDOW_SIZE);
    }, [messages]);

    return {
        sessionId,
        messages: windowedMessages,
        fixedErrorMessage,
        sendMessage,
        permissionRequest,
        respondPermission,
        thoughts,
        tokenUsage,
        sessionTokenUsage,
        contextStats,
        serverReadOnlyMode,
        planProgressSnapshot,
        isGenerating,
        realtimeToolExecutions,
        stop,
        interruptGeneration,
        cancelTool,
        setSessionReadOnlyMode,
        resetSession,
        loadSession,
        addLocalMessage,
        currentOperation,
        activeOperations,
        gitStatusUpdates,
        fileWatchPaths,
        agentMode
    };
}

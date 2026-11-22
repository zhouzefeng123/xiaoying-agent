// Model IDs are opaque, account-owned identifiers returned by the server.
export type ChatModelSelection = string
export type ResolvedChatModel = string

export interface ChatModelOption {
    id: ChatModelSelection
    label: string
    description?: string
}

export interface ChatModelObservation {
    status: 'success' | 'error' | 'canceled'
    ttftMs?: number
    totalMs?: number
    outputChars?: number
}

export interface AutoModelRetryDecision {
    errorCode?: number | null
    ttftMs?: number
}

export const DEFAULT_CHAT_MODEL: ResolvedChatModel = ''
export const DEFAULT_CHAT_MODEL_SELECTION: ChatModelSelection = ''
export const CHAT_MODEL_OPTIONS: ChatModelOption[] = []

export function resolveChatModelSelection(_selection: ChatModelSelection, _content: string, _attachments: File[] = []): ResolvedChatModel {
    return _selection.startsWith('user-model-') ? _selection : ''
}

export function resolveChatModelAttemptSequence(_selection: ChatModelSelection, _content: string, _attachments: File[] = []): ResolvedChatModel[] {
    const model = resolveChatModelSelection(_selection, _content, _attachments)
    return model ? [model] : []
}

export function shouldRetryAutoModelAttempt(_decision: AutoModelRetryDecision): boolean {
    return false
}

export function recordResolvedChatModelObservation(_model: ResolvedChatModel, _observation: ChatModelObservation): void {
    // User-selected routes never fall back to another account or provider.
}

export function getResolvedChatModelLabel(_model: ResolvedChatModel): string {
    return _model
}

export function getChatModelOption(_selection: ChatModelSelection): ChatModelOption {
    return { id: _selection, label: _selection }
}
export type ChatModelSelection = 'auto' | 'custom-main-model' | 'deepseek-v4-flash' | 'deepseek-v4-pro' | 'kimi-k2.6' | 'glm-5.1' | 'minimax-m2.7'

export type ResolvedChatModel = Exclude<ChatModelSelection, 'auto'>

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

const AUTO_CHAT_MODEL_ATTEMPTS = ['deepseek-v4-flash', 'deepseek-v4-pro', 'kimi-k2.6', 'glm-5.1', 'minimax-m2.7'] as const satisfies readonly ResolvedChatModel[]
const AUTO_MODEL_STATS_STORAGE_KEY = 'auramate.chat.auto_model_stats'
const AUTO_MODEL_STALE_AFTER_MS = 15 * 60 * 1000
const AUTO_MODEL_FAILURE_PENALTY_MS = 1500
const AUTO_MODEL_EWMA_ALPHA = 0.35
const AUTO_MODEL_TYPICAL_OUTPUT_CHARS = 1200
const AUTO_MODEL_TTFT_SCORE_WEIGHT = 0.25

type AutoTrackedChatModel = typeof AUTO_CHAT_MODEL_ATTEMPTS[number]

interface AutoModelStats {
    sampleCount: number
    failureCount: number
    lastObservedAt: number
    ttftEwmaMs?: number
    totalEwmaMs?: number
    outputMsPerCharEwma?: number
}

type AutoModelStatsMap = Partial<Record<AutoTrackedChatModel, AutoModelStats>>

const CHAT_MODEL_LABELS: Record<ResolvedChatModel, string> = {
    'custom-main-model': '自定义模型',
    'deepseek-v4-flash': 'deepseek-v4-flash',
    'deepseek-v4-pro': 'deepseek-v4-pro',
    'kimi-k2.6': 'kimi-k2.6',
    'glm-5.1': 'glm-5.1',
    'minimax-m2.7': 'minimax-m2.7',
}

export const DEFAULT_CHAT_MODEL: ResolvedChatModel = 'deepseek-v4-flash'
export const DEFAULT_CHAT_MODEL_SELECTION: ChatModelSelection = 'auto'

export const CHAT_MODEL_OPTIONS: ChatModelOption[] = [
    { id: 'auto', label: '自动' },
    { id: 'deepseek-v4-flash', label: 'deepseek-v4-flash' },
    { id: 'deepseek-v4-pro', label: 'deepseek-v4-pro' },
    { id: 'kimi-k2.6', label: 'kimi-k2.6' },
    { id: 'glm-5.1', label: 'glm-5.1' },
    { id: 'minimax-m2.7', label: 'minimax-m2.7' },
    { id: 'custom-main-model', label: '自定义模型', description: '使用设置中的主模型配置' },
]

function isAutoTrackedChatModel(model: ResolvedChatModel): model is AutoTrackedChatModel {
    return AUTO_CHAT_MODEL_ATTEMPTS.includes(model as AutoTrackedChatModel)
}

function normalizePositiveNumber(value: unknown): number | undefined {
    return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : undefined
}

function normalizeNonNegativeInteger(value: unknown): number {
    return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? Math.floor(value) : 0
}

function normalizeAutoModelStats(raw: unknown): AutoModelStatsMap {
    if (!raw || typeof raw !== 'object') {
        return {}
    }

    const source = raw as Record<string, unknown>
    const normalized: AutoModelStatsMap = {}
    for (const model of AUTO_CHAT_MODEL_ATTEMPTS) {
        const entry = source[model]
        if (!entry || typeof entry !== 'object') {
            continue
        }

        const record = entry as Record<string, unknown>
        normalized[model] = {
            sampleCount: normalizeNonNegativeInteger(record.sampleCount),
            failureCount: normalizeNonNegativeInteger(record.failureCount),
            lastObservedAt: normalizeNonNegativeInteger(record.lastObservedAt),
            ttftEwmaMs: normalizePositiveNumber(record.ttftEwmaMs),
            totalEwmaMs: normalizePositiveNumber(record.totalEwmaMs),
            outputMsPerCharEwma: normalizePositiveNumber(record.outputMsPerCharEwma),
        }
    }

    return normalized
}

function readAutoModelStats(): AutoModelStatsMap {
    if (typeof window === 'undefined') {
        return {}
    }

    try {
        const raw = window.localStorage.getItem(AUTO_MODEL_STATS_STORAGE_KEY)
        if (!raw) {
            return {}
        }
        return normalizeAutoModelStats(JSON.parse(raw))
    } catch {
        return {}
    }
}

function writeAutoModelStats(stats: AutoModelStatsMap): void {
    if (typeof window === 'undefined') {
        return
    }

    try {
        window.localStorage.setItem(AUTO_MODEL_STATS_STORAGE_KEY, JSON.stringify(stats))
    } catch {
        // Ignore storage failures and fall back to deterministic ordering.
    }
}

function isStatsFresh(stats: AutoModelStats | undefined, now: number): boolean {
    if (!stats || stats.lastObservedAt <= 0) {
        return false
    }
    return now-stats.lastObservedAt <= AUTO_MODEL_STALE_AFTER_MS
}

function effectiveFailureCount(stats: AutoModelStats | undefined, now: number): number {
    if (!stats || !isStatsFresh(stats, now)) {
        return 0
    }
    return stats.failureCount
}

function hasUsableLatencyStats(stats: AutoModelStats | undefined, now: number): boolean {
    if (!stats || !isStatsFresh(stats, now) || stats.sampleCount <= 0) {
        return false
    }
    return Number.isFinite(effectiveLatencyScore(stats, now))
}

function effectiveLatencyScore(stats: AutoModelStats | undefined, now: number): number {
    if (!stats || !isStatsFresh(stats, now)) {
        return Number.POSITIVE_INFINITY
    }

    const outputScore = stats.outputMsPerCharEwma === undefined
        ? undefined
        : stats.outputMsPerCharEwma * AUTO_MODEL_TYPICAL_OUTPUT_CHARS
    const ttftScore = stats.ttftEwmaMs === undefined ? 0 : stats.ttftEwmaMs * AUTO_MODEL_TTFT_SCORE_WEIGHT
    const baseline = outputScore ?? stats.totalEwmaMs ?? stats.ttftEwmaMs ?? Number.POSITIVE_INFINITY
    return baseline + ttftScore + stats.failureCount * AUTO_MODEL_FAILURE_PENALTY_MS
}

function applyEWMA(previousValue: number | undefined, nextValue: number): number {
    if (!Number.isFinite(nextValue) || nextValue <= 0) {
        return previousValue ?? nextValue
    }
    if (previousValue === undefined || !Number.isFinite(previousValue) || previousValue <= 0) {
        return nextValue
    }
    return previousValue * (1 - AUTO_MODEL_EWMA_ALPHA) + nextValue * AUTO_MODEL_EWMA_ALPHA
}

function compareAutoModels(a: AutoTrackedChatModel, b: AutoTrackedChatModel, stats: AutoModelStatsMap, now: number): number {
    const aStats = stats[a]
    const bStats = stats[b]
    const aHasLatency = hasUsableLatencyStats(aStats, now)
    const bHasLatency = hasUsableLatencyStats(bStats, now)
    if (aHasLatency && bHasLatency) {
        const aScore = effectiveLatencyScore(aStats, now)
        const bScore = effectiveLatencyScore(bStats, now)
        if (aScore !== bScore) {
            return aScore - bScore
        }
    } else if (aHasLatency !== bHasLatency) {
        return aHasLatency ? -1 : 1
    }

    const aFailures = effectiveFailureCount(aStats, now)
    const bFailures = effectiveFailureCount(bStats, now)
    if (aFailures !== bFailures) {
        return aFailures - bFailures
    }

    const defaultIndexGap = AUTO_CHAT_MODEL_ATTEMPTS.indexOf(a) - AUTO_CHAT_MODEL_ATTEMPTS.indexOf(b)
    if (defaultIndexGap !== 0) {
        return defaultIndexGap
    }

    return 0
}

function getDynamicAutoChatModelAttempts(): ResolvedChatModel[] {
    const now = Date.now()
    const stats = readAutoModelStats()
    const measuredModelCount = AUTO_CHAT_MODEL_ATTEMPTS.filter((model) => hasUsableLatencyStats(stats[model], now)).length
    if (measuredModelCount < 2) {
        return [...AUTO_CHAT_MODEL_ATTEMPTS]
    }
    return [...AUTO_CHAT_MODEL_ATTEMPTS].sort((a, b) => compareAutoModels(a, b, stats, now))
}

function dedupeResolvedChatModels(models: ResolvedChatModel[]): ResolvedChatModel[] {
    return models.filter((model, index) => models.indexOf(model) === index)
}

export function resolveChatModelSelection(selection: ChatModelSelection, content: string, attachments: File[] = []): ResolvedChatModel {
    return resolveChatModelAttemptSequence(selection, content, attachments)[0]
}

export function resolveChatModelAttemptSequence(selection: ChatModelSelection, content: string, attachments: File[] = []): ResolvedChatModel[] {
    if (selection !== 'auto') {
        return [selection]
    }

    void content
    void attachments
    return dedupeResolvedChatModels(getDynamicAutoChatModelAttempts())
}

export function shouldRetryAutoModelAttempt(decision: AutoModelRetryDecision): boolean {
    // Resource / service bootstrap failures may still surface as 1403 before the
    // first token arrives. In auto mode we should keep advancing the candidate
    // chain in that early-failure window instead of hard-stopping on the first model.
    if (decision.errorCode !== 1403) {
        return true
    }

    return !(typeof decision.ttftMs === 'number' && decision.ttftMs > 0)
}

export function recordResolvedChatModelObservation(model: ResolvedChatModel, observation: ChatModelObservation): void {
    if (!isAutoTrackedChatModel(model) || observation.status === 'canceled') {
        return
    }

    const now = Date.now()
    const stats = readAutoModelStats()
    const existing = stats[model]
    const resetForStaleness = !isStatsFresh(existing, now)
    const next: AutoModelStats = {
        sampleCount: resetForStaleness ? 0 : existing?.sampleCount ?? 0,
        failureCount: resetForStaleness ? 0 : existing?.failureCount ?? 0,
        lastObservedAt: now,
        ttftEwmaMs: resetForStaleness ? undefined : existing?.ttftEwmaMs,
        totalEwmaMs: resetForStaleness ? undefined : existing?.totalEwmaMs,
        outputMsPerCharEwma: resetForStaleness ? undefined : existing?.outputMsPerCharEwma,
    }

    if (observation.status === 'success') {
        next.sampleCount += 1
        next.failureCount = 0
        if (typeof observation.ttftMs === 'number' && observation.ttftMs > 0) {
            next.ttftEwmaMs = applyEWMA(next.ttftEwmaMs, observation.ttftMs)
        }
        if (typeof observation.totalMs === 'number' && observation.totalMs > 0) {
            next.totalEwmaMs = applyEWMA(next.totalEwmaMs, observation.totalMs)
        }
        if (
            typeof observation.outputChars === 'number'
            && observation.outputChars > 0
            && typeof observation.totalMs === 'number'
            && observation.totalMs > 0
        ) {
            const outputMs = typeof observation.ttftMs === 'number' && observation.ttftMs > 0
                ? Math.max(1, observation.totalMs - observation.ttftMs)
                : observation.totalMs
            next.outputMsPerCharEwma = applyEWMA(next.outputMsPerCharEwma, outputMs / observation.outputChars)
        }
    } else if (observation.status === 'error') {
        next.failureCount = Math.min(next.failureCount + 1, 4)
        if (typeof observation.totalMs === 'number' && observation.totalMs > 0) {
            next.totalEwmaMs = applyEWMA(next.totalEwmaMs, observation.totalMs)
        }
    }

    stats[model] = next
    writeAutoModelStats(stats)
}

export function getResolvedChatModelLabel(model: ResolvedChatModel): string {
    return CHAT_MODEL_LABELS[model]
}

export function getChatModelOption(selection: ChatModelSelection): ChatModelOption {
    return CHAT_MODEL_OPTIONS.find((option) => option.id === selection) ?? CHAT_MODEL_OPTIONS[0]
}

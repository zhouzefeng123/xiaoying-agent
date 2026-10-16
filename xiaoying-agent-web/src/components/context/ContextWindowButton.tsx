import { useEffect, useMemo, useRef, useState } from 'react';
import { cn } from '../../lib/utils';
import type { ContextStats } from '../../lib/ws';

type ContextWindowButtonProps = {
    contextStats: ContextStats | null;
};

type ContextSection = {
    key?: string;
    label: string;
    tokens: number;
};

type AssemblyRow = {
    label: string;
    value: string;
};

const TEXT = {
    title: '\u4e0a\u4e0b\u6587\u7a97\u53e3',
    currentContext: '\u5f53\u524d\u4e0a\u4e0b\u6587',
    responseReserve: '\u4fdd\u7559\u7528\u4e8e\u54cd\u5e94',
    available: '\u53ef\u7ee7\u7eed\u4f7f\u7528',
    capacityLimits: '\u5bb9\u91cf\u9608\u503c',
    softLimit: '\u8f6f\u4e0a\u9650',
    hardLimit: '\u786c\u4e0a\u9650',
    system: '\u7cfb\u7edf',
    userContext: '\u7528\u6237\u4e0a\u4e0b\u6587',
    assembly: '\u88c5\u914d\u89c2\u6d4b',
    historyWindow: '\u5386\u53f2\u7a97\u53e3',
    warmContext: 'Warm Context',
    ragCache: 'RAG Cache',
    memoryLayers: '\u5185\u5b58\u5c42',
    promptLayers: 'Prompt Layers',
    fallback: '\u56de\u9000\u7b56\u7565',
    action: '\u67e5\u770b\u4e0a\u4e0b\u6587\u7a97\u53e3',
    systemInstructions: '\u7cfb\u7edf\u6307\u4ee4',
    toolDefinitions: '\u5de5\u5177\u5b9a\u4e49',
    messages: '\u6d88\u606f',
    toolResults: '\u5de5\u5177\u7ed3\u679c',
};

const SECTION_LABELS: Record<string, string> = {
    system_instructions: TEXT.systemInstructions,
    tool_definitions: TEXT.toolDefinitions,
    messages: TEXT.messages,
    tool_results: TEXT.toolResults,
};

const DEFAULT_SECTIONS: ContextSection[] = [
    { key: 'system_instructions', label: SECTION_LABELS.system_instructions, tokens: 0 },
    { key: 'tool_definitions', label: SECTION_LABELS.tool_definitions, tokens: 0 },
    { key: 'messages', label: SECTION_LABELS.messages, tokens: 0 },
    { key: 'tool_results', label: SECTION_LABELS.tool_results, tokens: 0 },
];

function clamp(value: number, min: number, max: number): number {
    return Math.max(min, Math.min(max, value));
}

function formatTokenCount(value: number): string {
    if (value >= 1_000_000) {
        return `${(value / 1_000_000).toFixed(1)}M`;
    }
    if (value >= 1000) {
        return `${(value / 1000).toFixed(1)}K`;
    }
    return Math.round(value).toLocaleString();
}

function formatPercent(value: number, total: number): string {
    if (total <= 0) {
        return '0%';
    }
    const percent = (value / total) * 100;
    return percent >= 10 ? `${Math.round(percent)}%` : `${percent.toFixed(1)}%`;
}

function normalizeSections(stats: ContextStats | null): ContextSection[] {
    const incoming = new Map<string, ContextSection>();
    for (const section of stats?.sections ?? []) {
        if (!section?.key) {
            continue;
        }
        incoming.set(section.key, {
            key: section.key,
            label: SECTION_LABELS[section.key] ?? section.label,
            tokens: Math.max(0, Number(section.tokens) || 0),
        });
    }

    return DEFAULT_SECTIONS.map((section) => incoming.get(section.key ?? '') ?? section);
}

function readNumericStat(value: number | undefined, fallback: number): number {
    return Number.isFinite(Number(value)) ? Number(value) : fallback;
}

function formatDelta(current: number, limit: number): string {
    if (limit <= 0) {
        return '0 token';
    }
    if (current >= limit) {
        return `\u5df2\u8fbe\u6216\u8d85\u51fa ${formatTokenCount(current - limit)} token`;
    }
    return `\u8ddd\u5f53\u524d ${formatTokenCount(limit - current)} token`;
}

function formatCacheState(value: string | undefined): string {
    switch (value) {
        case 'hit':
            return '\u547d\u4e2d';
        case 'cold_start_refresh':
            return '\u51b7\u542f\u52a8\u5237\u65b0';
        case 'expired_refresh':
            return '\u8fc7\u671f\u5237\u65b0';
        case 'disabled':
            return '\u672a\u542f\u7528';
        default:
            return value ?? '\u672a\u77e5';
    }
}

export function formatWarmContextSource(stats: ContextStats['assembly']): string | null {
    if (!stats) {
        return null;
    }
    const source = stats.warm_context_source;
    const status = stats.warm_context_status;
    if (!source) {
        switch (status) {
            case 'disabled_no_user_input':
                return '\u672a\u89e6\u53d1\uff1a\u5f53\u524d\u6ca1\u6709\u53ef\u68c0\u7d22\u7684\u7528\u6237\u8f93\u5165';
            case 'disabled_short_input':
                return '\u672a\u89e6\u53d1\uff1a\u5f53\u524d\u8f93\u5165\u8fc7\u77ed\uff0c\u5df2\u8df3\u8fc7\u5386\u53f2\u53ec\u56de';
            case 'disabled_message_store_unavailable':
                return '\u672a\u89e6\u53d1\uff1a\u6d88\u606f\u68c0\u7d22\u4e0d\u53ef\u7528';
            case 'session_scope_miss':
                return stats.implicit_global_fallback_skipped
                    ? '\u672a\u547d\u4e2d\uff1a\u5f53\u524d/\u7236/\u7956\u5148\u4f1a\u8bdd\u65e0\u76f8\u5173\u5386\u53f2\uff0c\u4e14\u5df2\u8df3\u8fc7\u9690\u5f0f\u5168\u5c40\u56de\u9000'
                    : '\u672a\u547d\u4e2d\uff1a\u5f53\u524d/\u7236/\u7956\u5148\u4f1a\u8bdd\u65e0\u76f8\u5173\u5386\u53f2';
            case 'global_miss':
                return '\u672a\u547d\u4e2d\uff1a\u5168\u5c40\u5386\u53f2\u65e0\u76f8\u5173\u7ed3\u679c';
            case 'search_error':
                return '\u68c0\u7d22\u5931\u8d25\uff1a Warm Context \u672a\u80fd\u7eb3\u5165\u672c\u8f6e';
            default:
                break;
        }
        return stats.implicit_global_fallback_skipped
            ? '\u672a\u547d\u4e2d\uff0c\u4e14\u5df2\u8df3\u8fc7\u9690\u5f0f\u5168\u5c40\u56de\u9000'
            : '\u672a\u6ce8\u5165';
    }

    const sourceLabel = (() => {
        switch (source) {
            case 'current_session':
                return '\u5f53\u524d\u4f1a\u8bdd';
            case 'parent_session':
                return '\u7236\u4f1a\u8bdd';
            case 'ancestor_session':
                return '\u7956\u5148\u4f1a\u8bdd';
            case 'global':
                return '\u5168\u5c40';
            default:
                return source;
        }
    })();

    return stats.warm_context_session_id && source !== 'current_session'
        ? `${sourceLabel} · ${stats.warm_context_session_id}`
        : sourceLabel;
}

function formatHistoryWindowSource(stats: ContextStats['assembly']): string | null {
    if (!stats?.history_window_source) {
        return null;
    }

    const sourceLabel = stats.history_window_source === 'window'
        ? '\u7a97\u53e3\u5316\u52a0\u8f7d'
        : stats.history_window_source === 'full_history_fallback'
            ? '\u5168\u91cf\u5386\u53f2\u56de\u9000'
            : stats.history_window_source;
    const reasonLabel = (() => {
        switch (stats.history_window_fallback_reason) {
            case 'session_lookup_failed':
                return 'session \u67e5\u8be2\u5931\u8d25';
            case 'list_window_failed':
                return 'ListWindow \u5931\u8d25';
            default:
                return stats.history_window_fallback_reason;
        }
    })();
    const messageCount = Number(stats.history_window_messages ?? 0);

    return [
        sourceLabel,
        reasonLabel,
        messageCount > 0 ? `${messageCount} \u6761` : undefined,
    ].filter(Boolean).join(' · ');
}

function buildAssemblyRows(stats: ContextStats | null): AssemblyRow[] {
    const assembly = stats?.assembly;
    if (!assembly) {
        return [];
    }

    const rows: AssemblyRow[] = [];
    const historyWindow = formatHistoryWindowSource(assembly);
    if (historyWindow) {
        rows.push({ label: TEXT.historyWindow, value: historyWindow });
    }

    const warmContext = formatWarmContextSource(assembly);
    if (warmContext) {
        const messageCount = Number(assembly.warm_context_messages ?? 0);
        rows.push({
            label: TEXT.warmContext,
            value: messageCount > 0 ? `${warmContext} · ${messageCount} \u6761` : warmContext,
        });
    }

    rows.push({
        label: TEXT.ragCache,
        value: `L1/L2 ${formatCacheState(assembly.critical_cache)} / L3/L4 ${formatCacheState(assembly.dynamic_cache)}`,
    });

    const memoryParts: string[] = [];
    if (assembly.memory_capsule || Number(assembly.capsule_memory_count ?? 0) > 0) {
        memoryParts.push(`Capsule ${Number(assembly.capsule_memory_count ?? 0)}`);
    }
    if (assembly.turn_context || Number(assembly.turn_memory_count ?? 0) > 0) {
        memoryParts.push(`Turn ${Number(assembly.turn_memory_count ?? 0)}`);
    }
    if (memoryParts.length > 0) {
        rows.push({ label: TEXT.memoryLayers, value: memoryParts.join(' / ') });
    }

    const promptLayers: string[] = [];
    if (assembly.working_state) {
        promptLayers.push('Working State');
    }
    if (assembly.mutation_journal) {
        promptLayers.push('Mutation Journal');
    }
    if (assembly.dynamic_suffix) {
        promptLayers.push('Dynamic Suffix');
    }
    if (promptLayers.length > 0) {
        rows.push({ label: TEXT.promptLayers, value: promptLayers.join(' + ') });
    }

    if (assembly.implicit_global_fallback_skipped) {
        rows.push({
            label: TEXT.fallback,
            value: '\u5df2\u8df3\u8fc7 session-bound \u8bf7\u6c42\u7684\u9690\u5f0f\u5168\u5c40\u56de\u9000',
        });
    }

    return rows;
}

export function ContextWindowButton({ contextStats }: ContextWindowButtonProps) {
    const [isOpen, setIsOpen] = useState(false);
    const containerRef = useRef<HTMLDivElement>(null);

    const context = useMemo(() => {
        const limit = Math.max(0, Number(contextStats?.context_window_tokens ?? contextStats?.max_tokens ?? 0) || 0);
        const usedTokens = Math.max(0, Number(contextStats?.current_tokens ?? 0) || 0);
        const softLimitTokens = limit > 0
            ? clamp(readNumericStat(contextStats?.soft_limit_tokens, limit * 0.5), 0, limit)
            : 0;
        const hardLimitTokens = limit > 0
            ? clamp(readNumericStat(contextStats?.hard_limit_tokens, limit * 0.75), 0, limit)
            : 0;
        const reservedTokens = limit > 0
            ? clamp(readNumericStat(contextStats?.response_reserve_tokens, limit * 0.08), 0, Math.max(0, limit - usedTokens))
            : 0;
        const availableTokens = limit > 0
            ? clamp(readNumericStat(contextStats?.remaining_tokens, limit - usedTokens - reservedTokens), 0, limit)
            : Math.max(0, Number(contextStats?.remaining_tokens ?? 0) || 0);
        const usedPercent = limit > 0 ? (usedTokens / limit) * 100 : 0;
        const reservedPercent = limit > 0 ? (reservedTokens / limit) * 100 : 0;

        return {
            limit,
            usedTokens,
            availableTokens,
            reservedTokens,
            softLimitTokens,
            hardLimitTokens,
            usedPercent,
            reservedPercent,
            sections: normalizeSections(contextStats),
        };
    }, [contextStats]);

    const roundedPercent = Math.round(clamp(context.usedPercent, 0, 100));
    const reserveLeft = clamp(context.usedPercent, 0, 100);
    const reserveWidth = clamp(context.reservedPercent, 0, 100 - reserveLeft);
    const sectionTotal = Math.max(context.usedTokens, 0);
    const systemSections = context.sections.filter((section) => section.key === 'system_instructions' || section.key === 'tool_definitions');
    const userSections = context.sections.filter((section) => section.key !== 'system_instructions' && section.key !== 'tool_definitions');
    const assemblyRows = useMemo(() => buildAssemblyRows(contextStats), [contextStats]);
    const tokenText = context.limit > 0
        ? `${formatTokenCount(context.usedTokens)}/${formatTokenCount(context.limit)} token`
        : `${formatTokenCount(context.usedTokens)} token`;
    const reserveText = context.limit > 0
        ? `${formatTokenCount(context.reservedTokens)} token (${formatPercent(context.reservedTokens, context.limit)})`
        : `${formatTokenCount(context.reservedTokens)} token`;
    const availableText = context.limit > 0
        ? `${formatTokenCount(context.availableTokens)} token (${formatPercent(context.availableTokens, context.limit)})`
        : `${formatTokenCount(context.availableTokens)} token`;
    const softLimitText = context.limit > 0
        ? `${formatTokenCount(context.softLimitTokens)} token (${formatPercent(context.softLimitTokens, context.limit)})`
        : `${formatTokenCount(context.softLimitTokens)} token`;
    const hardLimitText = context.limit > 0
        ? `${formatTokenCount(context.hardLimitTokens)} token (${formatPercent(context.hardLimitTokens, context.limit)})`
        : `${formatTokenCount(context.hardLimitTokens)} token`;

    useEffect(() => {
        if (!isOpen) {
            return;
        }

        function handlePointerDown(event: MouseEvent) {
            if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
                setIsOpen(false);
            }
        }

        document.addEventListener('mousedown', handlePointerDown);
        return () => document.removeEventListener('mousedown', handlePointerDown);
    }, [isOpen]);

    return (
        <div className="relative shrink-0" ref={containerRef}>
            {isOpen && (
                <div className="absolute bottom-full right-0 z-[120] mb-2 w-72 max-w-[calc(100vw-2rem)] overflow-hidden rounded-lg border border-zinc-200 bg-white text-zinc-700 shadow-lg dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-300">
                    <div className="flex items-center justify-between border-b border-zinc-100 bg-zinc-50/70 px-3 py-2 dark:border-zinc-800 dark:bg-zinc-900/60">
                        <h2 className="text-xs font-medium text-zinc-900 dark:text-zinc-100">{TEXT.title}</h2>
                        <span className="text-xs tabular-nums text-zinc-400">{roundedPercent}%</span>
                    </div>

                    <div className="p-3">
                        <div className="mb-3 space-y-1.5">
                            <MetricRow label={TEXT.currentContext} value={tokenText} />
                            <MetricRow label={TEXT.responseReserve} value={reserveText} />
                            <MetricRow label={TEXT.available} value={availableText} />
                        </div>

                        <div className="relative mb-2.5 h-1.5 overflow-hidden rounded-full bg-zinc-100 dark:bg-zinc-800">
                            <div
                                className="absolute inset-y-0 left-0 rounded-full bg-sky-500/70"
                                style={{ width: `${clamp(context.usedPercent, 0, 100)}%` }}
                            />
                            <div
                                className="absolute inset-y-0 rounded-full opacity-80"
                                style={{
                                    left: `${reserveLeft}%`,
                                    width: `${reserveWidth}%`,
                                    backgroundImage: 'repeating-linear-gradient(135deg, rgba(14, 165, 233, 0.78) 0 3px, transparent 3px 7px)',
                                }}
                            />
                        </div>

                        <div className="mb-3 flex items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400">
                            <div className="flex items-center gap-2">
                                <span
                                    className="h-2.5 w-4 rounded-[2px]"
                                    style={{
                                        backgroundImage: 'repeating-linear-gradient(135deg, rgba(14, 165, 233, 0.78) 0 3px, transparent 3px 7px)',
                                    }}
                                />
                                <span>{TEXT.responseReserve}</span>
                            </div>
                        </div>

                        <section className="mb-3">
                            <h3 className="mb-1.5 text-xs font-medium text-zinc-400">{TEXT.capacityLimits}</h3>
                            <div className="grid grid-cols-2 gap-2">
                                <LimitCard label={TEXT.softLimit} value={softLimitText} detail={formatDelta(context.usedTokens, context.softLimitTokens)} />
                                <LimitCard label={TEXT.hardLimit} value={hardLimitText} detail={formatDelta(context.usedTokens, context.hardLimitTokens)} />
                            </div>
                        </section>

                        <div className="space-y-3">
                            <section>
                                <h3 className="mb-1.5 text-xs font-medium text-zinc-400">{TEXT.system}</h3>
                                {systemSections.map((section) => (
                                    <ContextRow key={section.key ?? section.label} section={section} totalTokens={sectionTotal} />
                                ))}
                            </section>

                            <section>
                                <h3 className="mb-1.5 text-xs font-medium text-zinc-400">{TEXT.userContext}</h3>
                                {userSections.map((section) => (
                                    <ContextRow key={section.key ?? section.label} section={section} totalTokens={sectionTotal} />
                                ))}
                            </section>

                            {assemblyRows.length > 0 && (
                                <section>
                                    <h3 className="mb-1.5 text-xs font-medium text-zinc-400">{TEXT.assembly}</h3>
                                    {assemblyRows.map((row) => (
                                        <MetricRow key={row.label} label={row.label} value={row.value} />
                                    ))}
                                </section>
                            )}
                        </div>
                    </div>
                </div>
            )}

            <button
                type="button"
                onClick={() => setIsOpen((value) => !value)}
                className={cn(
                    'grid h-6 w-6 place-items-center rounded-full text-zinc-400 transition-colors hover:bg-zinc-100 hover:text-zinc-600 dark:hover:bg-zinc-800 dark:hover:text-zinc-300',
                    isOpen && 'bg-zinc-100 text-zinc-700 dark:bg-zinc-800 dark:text-zinc-100',
                )}
                title={TEXT.action}
                aria-label={TEXT.action}
            >
                <span
                    className="grid h-[18px] w-[18px] place-items-center rounded-full p-[2px] [--context-empty-ring:rgb(228_228_231)] dark:[--context-empty-ring:rgb(63_63_70)]"
                    style={{
                        background: `conic-gradient(rgb(14 165 233) ${clamp(context.usedPercent, 0, 100)}%, var(--context-empty-ring) ${clamp(context.usedPercent, 0, 100)}% 100%)`,
                    }}
                >
                    <span className="h-full w-full rounded-full bg-white dark:bg-zinc-950" />
                </span>
            </button>
        </div>
    );
}

function MetricRow({ label, value }: { label: string; value: string }) {
    return (
        <div className="flex items-baseline justify-between gap-3 text-xs">
            <span className="shrink-0 text-zinc-400">{label}</span>
            <span className="font-medium tabular-nums text-zinc-900 dark:text-zinc-100">{value}</span>
        </div>
    );
}

function LimitCard({ label, value, detail }: { label: string; value: string; detail: string }) {
    return (
        <div className="rounded-md border border-zinc-200/80 bg-zinc-50/70 px-2.5 py-2 dark:border-zinc-800 dark:bg-zinc-900/60">
            <div className="mb-1 text-[11px] font-medium text-zinc-500 dark:text-zinc-400">{label}</div>
            <div className="text-xs font-medium tabular-nums text-zinc-900 dark:text-zinc-100">{value}</div>
            <div className="mt-1 text-[11px] text-zinc-500 dark:text-zinc-400">{detail}</div>
        </div>
    );
}

function ContextRow({ section, totalTokens }: { section: ContextSection; totalTokens: number }) {
    return (
        <div className="mb-1 flex items-center justify-between gap-3 text-xs">
            <span className="min-w-0 truncate text-zinc-600 dark:text-zinc-300">{section.label}</span>
            <span className="shrink-0 tabular-nums text-zinc-500">{`${formatTokenCount(section.tokens)} (${formatPercent(section.tokens, totalTokens)})`}</span>
        </div>
    );
}

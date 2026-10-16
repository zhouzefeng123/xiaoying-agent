import React, { useState, useEffect, useRef, useMemo, useCallback, useLayoutEffect } from 'react';
import ReactMarkdown from 'react-markdown';
import rehypeRaw from 'rehype-raw';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import vscDarkPlus from 'react-syntax-highlighter/dist/esm/styles/prism/vsc-dark-plus';
import remarkGfm from 'remark-gfm';
import { handleExternalLinkClick, isExternalLinkHref } from '../../lib/externalLinks';
import { isMeaningfulRenderableChatText, stripInjectedChatControlContent } from '../../lib/chatContent';
import {
    derivePlanProgressItems,
    isPlanProgressTool as isSharedPlanProgressTool,
    planProgressPercent,
    type PlanProgressItem,
    type PlanProgressSnapshot,
    type PlanProgressStatus,
} from '../../lib/planProgress';
import { cn } from '../../lib/utils';
import { Bot, CheckCircle2, ChevronRight, ChevronDown, Circle, Loader2, Pin, Square, RefreshCw, File as FileIcon, FolderOpen, FileText, TerminalSquare, Play, Search, Globe, Wrench, Download, GitBranch, Braces, XCircle, type LucideIcon } from 'lucide-react';
import { ImagePreview } from './ImagePreview';
import { Logo } from '../common/Logo';

import { throttle } from 'lodash';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const syntaxTheme: any = vscDarkPlus;
const USE_LIGHTWEIGHT_CHAT_CARD_MOTION = true;
const CHAT_CARD_EXIT_DURATION_MS = 320;
const CHAT_CARD_EXIT_EASING = 'cubic-bezier(0.22, 1, 0.36, 1)';

const getThinkingMarkdownThrottle = (contentLength: number): number => {
    if (import.meta.env.PROD) {
        if (contentLength < 2000) return 50;
        if (contentLength < 5000) return 66;
        if (contentLength < 10000) return 100;
        return 140;
    }

    if (contentLength < 2000) return 33;
    if (contentLength < 5000) return 50;
    if (contentLength < 10000) return 100;
    return 160;
};

const renderPlainDeletionText = (props: React.HTMLAttributes<HTMLElement>) => {
    const { children, ...rest } = props;
    return <span {...rest}>{children}</span>;
};

// Throttled Markdown Renderer Component
const ThrottledMarkdown = React.memo(({ content }: { content: string }) => {
    const [renderedContent, setRenderedContent] = useState(content);
    
    // Dynamic throttle calculation based on content length
    const throttleTime = useMemo(() => getThinkingMarkdownThrottle(content.length), [content.length]);

    // Create a throttled updater
    const updateContent = useMemo(
        () => throttle((newContent: string) => {
            setRenderedContent(newContent);
        }, throttleTime, { leading: true, trailing: true }),
        [throttleTime]
    );

    useEffect(() => {
        updateContent(content);
    }, [content, updateContent]);

    useEffect(() => {
        return () => {
            updateContent.cancel();
        };
    }, [updateContent]);

    return (
        <ReactMarkdown
            remarkPlugins={[remarkGfm]}
            rehypePlugins={[rehypeRaw]}
            components={{
                code({ inline, className, children, ...props }: { inline?: boolean; className?: string; children?: React.ReactNode } & React.HTMLAttributes<HTMLElement>) {
                    const match = /language-(\w+)/.exec(className || '');
                        return !inline && match ? (
                            <div className="rounded-md overflow-hidden my-2 border border-zinc-200/50 dark:border-zinc-700/50">
                                <div className="bg-[#1e1e1e] px-2 py-0.5 text-[10px] text-zinc-500 border-b border-zinc-200/50 dark:border-zinc-700/50">
                                    {match[1]}
                                </div>
                            <SyntaxHighlighter
                                style={syntaxTheme}
                                    language={match[1]}
                                    PreTag="div"
                                    {...props}
                                    customStyle={{ margin: 0, borderRadius: 0, fontSize: '11px', lineHeight: '1.5', background: '#1e1e1e' }}
                                >
                                    {String(children).replace(/\n$/, '')}
                                </SyntaxHighlighter>
                            </div>
                        ) : (
                        <code className="px-1 py-0.5 rounded font-mono text-[10px] bg-zinc-100 dark:bg-zinc-800 text-zinc-600 dark:text-zinc-300 border border-zinc-200/50 dark:border-zinc-700/50" {...props}
                        >
                            {children}
                        </code>
                    );
                },
                p: (props: React.HTMLAttributes<HTMLParagraphElement>) => <p className="mb-2 last:mb-0" {...props} />,
                ul: (props: React.HTMLAttributes<HTMLUListElement>) => <ul className="list-disc pl-4 mb-2" {...props} />,
                ol: (props: React.HTMLAttributes<HTMLOListElement>) => <ol className="list-decimal pl-4 mb-2" {...props} />,
                del: renderPlainDeletionText,
                s: renderPlainDeletionText,
                a: ({ href, children, ...props }: { href?: string; children?: React.ReactNode } & React.AnchorHTMLAttributes<HTMLAnchorElement>) => (
                    <a
                        {...props}
                        href={href}
                        className="font-medium underline decoration-2 underline-offset-2 cursor-pointer text-blue-600 dark:text-blue-400"
                        target={isExternalLinkHref(href) ? '_blank' : props.target}
                        rel={isExternalLinkHref(href) ? 'noopener noreferrer' : props.rel}
                        onClick={(event) => {
                            props.onClick?.(event);
                            if (!event.defaultPrevented) {
                                handleExternalLinkClick(event, href);
                            }
                        }}
                    >
                        {children}
                    </a>
                ),
            }}
        >
            {renderedContent}
        </ReactMarkdown>
    );
});

const createMessageMarkdownComponents = (isStreaming: boolean) => ({
    code({ inline, className, children, ...props }: { inline?: boolean; className?: string; children?: React.ReactNode } & React.HTMLAttributes<HTMLElement>) {
        const match = /language-(\w+)/.exec(className || '');
        if (!inline && match) {
            const codeContent = String(children).replace(/\n$/, '');
            if (isStreaming) {
                return (
                    <div className="my-4 overflow-hidden rounded-md border border-zinc-200/50 dark:border-zinc-700/50">
                        <div className="bg-[#1e1e1e] px-3 py-1.5 text-xs text-zinc-400 border-b border-zinc-700/50 flex justify-between items-center">
                            <span>{match[1]}</span>
                        </div>
                        <pre className="m-0 p-4 text-[11px] leading-[1.5] font-mono bg-[#1e1e1e] text-zinc-200 overflow-x-auto">
                            {codeContent}
                        </pre>
                    </div>
                );
            }

            return (
                <div className="my-4 overflow-hidden rounded-md border border-zinc-200/50 dark:border-zinc-700/50">
                    <div className="bg-[#1e1e1e] px-3 py-1.5 text-xs text-zinc-400 border-b border-zinc-700/50 flex justify-between items-center">
                        <span>{match[1]}</span>
                    </div>
                    <SyntaxHighlighter
                        style={syntaxTheme}
                        language={match[1]}
                        PreTag="div"
                        customStyle={{ margin: 0, borderRadius: 0, fontSize: '11px', lineHeight: '1.5', background: '#1e1e1e' }}
                    >
                        {codeContent}
                    </SyntaxHighlighter>
                </div>
            );
        }

        return (
            <code className={cn(
                "px-1.5 py-0.5 rounded font-mono text-sm break-all whitespace-pre-wrap",
                "bg-zinc-200/50 dark:bg-zinc-700/50 text-zinc-800 dark:text-zinc-200 border border-zinc-200/50 dark:border-zinc-700/50"
            )} {...props}>
                {children}
            </code>
        );
    },
    p: (props: React.HTMLAttributes<HTMLParagraphElement>) => <p className="mb-1.5 last:mb-0 leading-normal" {...props} />,
    ul: (props: React.HTMLAttributes<HTMLUListElement>) => <ul className="list-disc pl-5 mb-1.5 space-y-1" {...props} />,
    ol: (props: React.HTMLAttributes<HTMLOListElement>) => <ol className="list-decimal pl-5 mb-1.5 space-y-1" {...props} />,
    li: (props: React.HTMLAttributes<HTMLLIElement>) => <li className="pl-1" {...props} />,
    del: renderPlainDeletionText,
    s: renderPlainDeletionText,
    h1: (props: React.HTMLAttributes<HTMLHeadingElement>) => <h1 className="text-lg font-bold mb-2.5 mt-4 border-b pb-1 border-zinc-200/50 dark:border-zinc-700/50" {...props} />,
    h2: (props: React.HTMLAttributes<HTMLHeadingElement>) => <h2 className="text-base font-bold mb-2 mt-3" {...props} />,
    h3: (props: React.HTMLAttributes<HTMLHeadingElement>) => <h3 className="text-sm font-bold mb-1.5 mt-2.5" {...props} />,
    h4: (props: React.HTMLAttributes<HTMLHeadingElement>) => <h4 className="text-xs font-bold mb-1.5 mt-2.5" {...props} />,
    table: (props: React.HTMLAttributes<HTMLTableElement>) => <div className="overflow-x-auto my-4"><table className="min-w-full divide-y divide-zinc-200/50 dark:divide-zinc-700/50 border border-zinc-200/50 dark:border-zinc-700/50 rounded-lg" {...props} /></div>,
    th: (props: React.HTMLAttributes<HTMLTableCellElement>) => <th className="px-3 py-2 bg-zinc-50/50 dark:bg-zinc-800/50 text-left text-xs font-medium text-zinc-500 uppercase tracking-wider border-b border-zinc-200/50 dark:border-zinc-700/50" {...props} />,
    td: (props: React.HTMLAttributes<HTMLTableCellElement>) => <td className="px-3 py-2 whitespace-normal border-b border-zinc-200/50 dark:border-zinc-700/50 text-sm" {...props} />,
    a: ({ href, children, ...props }: { href?: string; children?: React.ReactNode } & React.AnchorHTMLAttributes<HTMLAnchorElement>) => {
        const isImage = href?.match(/\.(jpeg|jpg|gif|png|webp)(\?.*)?$/i);
        const isExternal = isExternalLinkHref(href);

        if (isImage && href) {
            let finalSrc = href;
            if (href.startsWith('workspace/')) {
                finalSrc = '/' + href;
            }

            return (
                <span className="block my-2">
                    <a
                        className="font-medium underline decoration-2 underline-offset-2 hover:opacity-80 mb-1 inline-block text-blue-600 dark:text-blue-400 cursor-pointer"
                        href={href}
                        target={isExternal ? '_blank' : props.target}
                        rel={isExternal ? 'noopener noreferrer' : props.rel}
                        onClick={(event) => {
                            props.onClick?.(event);
                            if (!event.defaultPrevented) {
                                handleExternalLinkClick(event, href);
                            }
                        }}
                        {...props}
                    >
                        {children}
                    </a>
                    <ImagePreview src={finalSrc} alt={typeof children === 'string' ? children : 'Image'} />
                </span>
            );
        }

        return (
            <a
                className="font-medium underline decoration-2 underline-offset-2 hover:opacity-80 text-blue-600 dark:text-blue-400 cursor-pointer"
                href={href}
                target={isExternal ? '_blank' : props.target}
                rel={isExternal ? 'noopener noreferrer' : props.rel}
                onClick={(event) => {
                    props.onClick?.(event);
                    if (!event.defaultPrevented) {
                        handleExternalLinkClick(event, href);
                    }
                }}
                {...props}
            >
                {children}
            </a>
        );
    },
    img: ({ src, alt }: { src?: string; alt?: string }) => {
        if (!src) return null;
        let finalSrc = src;
        if (src.startsWith('workspace/')) {
            finalSrc = '/' + src;
        }
        return <ImagePreview src={finalSrc} alt={alt} />;
    },
    blockquote: (props: React.BlockquoteHTMLAttributes<HTMLQuoteElement>) => <blockquote className="border-l-4 border-zinc-300 dark:border-zinc-600 pl-4 italic my-2" {...props} />,
    details: (props: React.DetailsHTMLAttributes<HTMLDetailsElement>) => (
        <details className="mb-2 rounded-lg border border-zinc-200 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-900/50 overflow-hidden" {...props} />
    ),
    summary: (props: React.HTMLAttributes<HTMLElement>) => (
        <summary className="px-3 py-2 cursor-pointer hover:bg-zinc-100 dark:hover:bg-zinc-800 font-medium text-xs text-zinc-500 uppercase tracking-wider select-none outline-none flex items-center gap-2" {...props} />
    ),
});

const MessageMarkdown = React.memo(({ content, isStreaming }: { content: string; isStreaming: boolean }) => {
    const [renderedContent, setRenderedContent] = useState(content);
    const latestContentRef = useRef(content);
    const frameIdRef = useRef<number | null>(null);
    const fallbackTimerRef = useRef<number | null>(null);

    const cancelScheduledRender = useCallback(() => {
        if (frameIdRef.current) {
            cancelAnimationFrame(frameIdRef.current);
            frameIdRef.current = null;
        }
        if (fallbackTimerRef.current) {
            clearTimeout(fallbackTimerRef.current);
            fallbackTimerRef.current = null;
        }
    }, []);

    const flushRenderedContent = useCallback(() => {
        cancelScheduledRender();
        setRenderedContent(latestContentRef.current);
    }, [cancelScheduledRender]);

    const scheduleRenderedContent = useCallback(() => {
        if (frameIdRef.current || fallbackTimerRef.current) {
            return;
        }

        frameIdRef.current = requestAnimationFrame(() => {
            flushRenderedContent();
        });

        fallbackTimerRef.current = window.setTimeout(() => {
            flushRenderedContent();
        }, import.meta.env.PROD ? 48 : 32);
    }, [flushRenderedContent]);

    useEffect(() => {
        if (!isStreaming) {
            latestContentRef.current = content;
            cancelScheduledRender();
            setRenderedContent(content);
            return;
        }

        latestContentRef.current = content;
        scheduleRenderedContent();
    }, [cancelScheduledRender, content, isStreaming, scheduleRenderedContent]);

    useEffect(() => {
        return () => {
            cancelScheduledRender();
        };
    }, [cancelScheduledRender]);

    const markdownComponents = useMemo(() => createMessageMarkdownComponents(isStreaming), [isStreaming]);

    return (
        <ReactMarkdown
            remarkPlugins={[remarkGfm]}
            rehypePlugins={[rehypeRaw]}
            components={markdownComponents}
        >
            {isStreaming ? renderedContent : content}
        </ReactMarkdown>
    );
});

// Markdown cache to avoid re-parsing the same content
const markdownCache = new Map<string, string>();
const CACHE_MAX_SIZE = 100;

function getCachedMarkdown(content: string): string | null {
    return markdownCache.get(content) || null;
}

function setCachedMarkdown(content: string, parsed: string): void {
    if (markdownCache.size >= CACHE_MAX_SIZE) {
        // Remove oldest entry
        const firstKey = markdownCache.keys().next().value;
        if (firstKey) {
            markdownCache.delete(firstKey);
        }
    }
    markdownCache.set(content, parsed);
}

export interface ToolExecution {
    id: string;
    executionId?: string; // Phase 5: Unique ID for manual interruption
    name: string;
    input: string;
    output: string;
    status: 'running' | 'completed' | 'failed';
    isCodeStream?: boolean;
    isFinalizing?: boolean;
    metadata?: any;
}

type ToolCardVisual = {
    icon: LucideIcon;
    iconClassName: string;
    iconContainerClassName: string;
};

export interface AttachmentMeta {
    name: string;
    url?: string;
    type?: string;
}

// File type recognition for attachment cards
const getFileTypeInfo = (meta: AttachmentMeta) => {
    const fileName = meta.name.toLowerCase();
    const ext = fileName.split('.').pop() || '';
    const hasDot = meta.name.includes('.');
    
    // Check if it's a directory/folder (no dot in name AND type is empty, or specific directory types)
    const isDirectory = (!hasDot && !meta.type) || meta.type === 'inode/directory' || meta.type === 'application/x-directory';
    if (isDirectory) {
        return { icon: FolderOpen, color: 'text-yellow-600', bgColor: 'bg-yellow-100 dark:bg-yellow-900/30', label: '文件夹' };
    }
    
    const docTypes = ['pdf', 'doc', 'docx', 'txt', 'rtf', 'odt', 'wps'];
    const spreadsheetTypes = ['xls', 'xlsx', 'csv', 'ods', 'et'];
    const presentationTypes = ['ppt', 'pptx', 'odp', 'dps'];
    const codeTypes = ['js', 'ts', 'jsx', 'tsx', 'py', 'java', 'go', 'cpp', 'c', 'h', 'html', 'css', 'json', 'xml', 'md', 'vue', 'svelte', 'rb', 'php', 'rs', 'swift', 'kt', 'sh', 'bat', 'ps1'];
    const archiveTypes = ['zip', 'rar', '7z', 'tar', 'gz', 'bz2', 'xz'];
    const imageTypes = ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'svg', 'webp', 'ico', 'tiff'];
    const videoTypes = ['mp4', 'avi', 'mkv', 'mov', 'wmv', 'flv', 'webm'];
    const audioTypes = ['mp3', 'wav', 'flac', 'aac', 'ogg', 'wma', 'm4a'];
    
    if (docTypes.includes(ext)) {
        return { icon: FileText, color: 'text-blue-600', bgColor: 'bg-blue-100 dark:bg-blue-900/30', label: ext.toUpperCase() };
    }
    if (spreadsheetTypes.includes(ext)) {
        return { icon: FileText, color: 'text-green-600', bgColor: 'bg-green-100 dark:bg-green-900/30', label: ext.toUpperCase() };
    }
    if (presentationTypes.includes(ext)) {
        return { icon: FileText, color: 'text-orange-600', bgColor: 'bg-orange-100 dark:bg-orange-900/30', label: ext.toUpperCase() };
    }
    if (codeTypes.includes(ext)) {
        return { icon: TerminalSquare, color: 'text-purple-600', bgColor: 'bg-purple-100 dark:bg-purple-900/30', label: ext.toUpperCase() };
    }
    if (archiveTypes.includes(ext)) {
        return { icon: FileIcon, color: 'text-yellow-600', bgColor: 'bg-yellow-100 dark:bg-yellow-900/30', label: ext.toUpperCase() };
    }
    if (imageTypes.includes(ext)) {
        return { icon: FileText, color: 'text-pink-600', bgColor: 'bg-pink-100 dark:bg-pink-900/30', label: ext.toUpperCase() };
    }
    if (videoTypes.includes(ext)) {
        return { icon: FileText, color: 'text-red-600', bgColor: 'bg-red-100 dark:bg-red-900/30', label: ext.toUpperCase() };
    }
    if (audioTypes.includes(ext)) {
        return { icon: FileText, color: 'text-indigo-600', bgColor: 'bg-indigo-100 dark:bg-indigo-900/30', label: ext.toUpperCase() };
    }
    
    // Default - handle octet-stream and unknown types
    const mimeTypeLabel = meta.type?.split('/').pop()?.toUpperCase();
    if (!mimeTypeLabel || mimeTypeLabel === 'OCTET-STREAM') {
        // Unknown binary file, try to use extension or show "文件"
        return { icon: FileIcon, color: 'text-zinc-500', bgColor: 'bg-zinc-100 dark:bg-zinc-800/50', label: ext ? ext.toUpperCase() : '文件' };
    }
    return { icon: FileIcon, color: 'text-zinc-500', bgColor: 'bg-zinc-100 dark:bg-zinc-800/50', label: mimeTypeLabel };
};

const AttachmentFileCard: React.FC<{ meta: AttachmentMeta }> = ({ meta }) => {
    const fileInfo = getFileTypeInfo(meta);
    const IconComponent = fileInfo.icon;
    
    return (
        <div className="flex max-w-xs cursor-default items-center gap-2 rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2 dark:border-zinc-700 dark:bg-zinc-800/50">
            <div className={cn("w-8 h-8 rounded-md flex items-center justify-center flex-shrink-0", fileInfo.bgColor)}>
                <IconComponent className={cn("w-4 h-4", fileInfo.color)} />
            </div>
            <div className="flex flex-col min-w-0">
                <span className="text-xs font-medium text-zinc-700 dark:text-zinc-200 truncate" title={meta.name}>
                    {meta.name}
                </span>
                <span className="text-[10px] text-zinc-500 uppercase tracking-wider">
                    {fileInfo.label}
                </span>
            </div>
        </div>
    );
};

export type TimelineEvent =
    | { type: 'thinking'; index: number }
    | { type: 'tool'; executionId: string; contentMark?: number }
    | { type: 'progress'; contentMark?: number };

type ContentFragment =
    | { type: 'thinking'; content: string }
    | { type: 'main'; content: string; hasResumeAction: boolean; resumeLabel: string };

export interface Message {
    id: string;
    role: 'user' | 'assistant' | 'system' | 'tool';
    content: string;
    timestamp?: number;
    responseStartedAt?: number;
    responseTimeMs?: number;
    attachments?: string[];
    attachmentMetas?: AttachmentMeta[];
    isStreaming?: boolean;
    isToolPlaceholder?: boolean;
    progress?: {
        percentage: number;
        details: string;
    };
    toolExecutions?: ToolExecution[];
    eventTimeline?: TimelineEvent[];
}

const formatResponseTime = (durationMs?: number): string => {
    if (typeof durationMs !== 'number' || !Number.isFinite(durationMs)) {
        return '';
    }

    if (durationMs < 1000) {
        return '<1s';
    }

    const seconds = durationMs / 1000;
    if (seconds < 60) return `${Math.round(seconds)}s`;

    const minutes = Math.floor(seconds / 60);
    const remainingSeconds = Math.round(seconds % 60);
    return remainingSeconds > 0 ? `${minutes}m ${remainingSeconds}s` : `${minutes}m`;
};

export interface MessageBubbleProps {
    message: Message;
    isThinking?: boolean;
    onCancelTool?: (executionId: string) => void;
    realtimeToolExecutions?: ToolExecution[];
    onResume?: () => void;
}

const injectedPromptMarkers = [
    '[TURN CONTEXT]',
    '[MEMORY CAPSULE]',
    '[WORKING STATE]',
    '[CONTEXT INFO]',
    '[SYSTEM INFO]',
    '[System Summary]:',
    'User has attached the following files:',
    '请使用 read 等工具的 file_path 参数读取这些文件进行处理。',
    'Environment:',
    '<env>'
];

const injectedPromptControlLinePattern = /(SYSTEM WARNING:|SYSTEM MONITOR:|SYSTEM:|STOP REPEATING|Action Required:|THIS IS A FAILURE|RETRY IMMEDIATELY|check_command_status|you have completed your thinking process|output the actual RESPONSE or TOOL CALL|you did NOT emit any tool call|you MUST use a tool|If you are waiting for something|If you are stuck, ask the user|OS:|Shell:|CWD:|ProjectRoot:|User:|Time:|Timezone:|Loc:|Installed Tools:|\[TURN CONTEXT\]|\[MEMORY CAPSULE\]|\[WORKING STATE\]|\[CONTEXT INFO\]|\[SYSTEM INFO\]|\[System Summary\]:|User has attached the following files:|请使用 read 等工具的 file_path 参数读取这些文件进行处理。|<env>|Environment:)/i;

const streamingOpenTokens = ['<thinking>', '<think>', '&lt;thinking&gt;', '&lt;think&gt;'];
const streamingCloseTokens = ['</thinking>', '</think>', '&lt;/thinking&gt;', '&lt;/think&gt;'];

type ParsedMessageContent = {
    thinkingContents: string[];
    mainContent: string;
    hasResumeAction: boolean;
    resumeLabel: string;
};

type StreamingParseState = {
    rawContent: string;
    carry: string;
    inThinking: boolean;
    thinkingBlocks: string[];
    currentThinkingBlock: string;
    committedMain: string;
    pendingMainLine: string;
    skipQuotedBlock: boolean;
    stopMainOutput: boolean;
    justClosedEmptyThinking: boolean;
};

function createStreamingParseState(): StreamingParseState {
    return {
        rawContent: '',
        carry: '',
        inThinking: false,
        thinkingBlocks: [],
        currentThinkingBlock: '',
        committedMain: '',
        pendingMainLine: '',
        skipQuotedBlock: false,
        stopMainOutput: false,
        justClosedEmptyThinking: false,
    };
}

function finalizeStreamingThinkingBlock(state: StreamingParseState): void {
    const thinking = stripInjectedChatControlContent(state.currentThinkingBlock);
    if (isMeaningfulRenderableChatText(thinking)) {
        state.thinkingBlocks.push(thinking);
        state.justClosedEmptyThinking = false;
    } else {
        state.justClosedEmptyThinking = true;
    }

    state.currentThinkingBlock = '';
}

function hasStreamingInlineMainPrefix(state: StreamingParseState): boolean {
    if (/\S$/.test(state.pendingMainLine)) {
        return true;
    }

    return /\S$/.test(state.committedMain);
}

function findEarliestTokenIndex(input: string, tokens: string[]): { index: number; token: string } | null {
    let bestIndex = -1;
    let bestToken = '';

    for (const token of tokens) {
        const index = input.indexOf(token);
        if (index === -1) continue;
        if (bestIndex === -1 || index < bestIndex) {
            bestIndex = index;
            bestToken = token;
        }
    }

    return bestIndex === -1 ? null : { index: bestIndex, token: bestToken };
}

function longestStreamingTokenPrefixSuffix(input: string, tokens: string[]): number {
    let best = 0;

    for (const token of tokens) {
        const maxCheck = Math.min(input.length, token.length-1);
        for (let length = maxCheck; length > best; length -= 1) {
            if (input.endsWith(token.slice(0, length))) {
                best = length;
                break;
            }
        }
    }

    return best;
}

function isStreamingStopLine(line: string): boolean {
    return injectedPromptMarkers.some((marker) => line.includes(marker));
}

function shouldSkipStreamingToolLine(trimmedLine: string): { skip: boolean; quotedBlock: boolean } {
    if (!trimmedLine) {
        return { skip: false, quotedBlock: false };
    }

    const normalized = trimmedLine.startsWith('>') ? trimmedLine.replace(/^>\s?/, '') : trimmedLine;
    const isToolMarker = normalized.startsWith('[Tool Call') || normalized.startsWith('[Tool Output') || normalized.startsWith('[Error]') || normalized.startsWith('Output:');

    if (!isToolMarker) {
        return { skip: false, quotedBlock: false };
    }

    return {
        skip: true,
        quotedBlock: trimmedLine.startsWith('>'),
    };
}

function appendStreamingMainChunk(state: StreamingParseState, chunk: string): void {
    if (!chunk || state.stopMainOutput) {
        return;
    }

    let normalizedChunk = chunk;
    if (state.justClosedEmptyThinking) {
        if (hasStreamingInlineMainPrefix(state)) {
            normalizedChunk = normalizedChunk.replace(/^(?:[\t \u00a0]*[\r\n]+[\s\u00a0]*)+/, '');
        }
        state.justClosedEmptyThinking = false;
    }

    const combined = state.pendingMainLine + normalizedChunk;
    const lines = combined.split('\n');
    state.pendingMainLine = lines.pop() ?? '';

    for (const line of lines) {
        appendStreamingMainLine(state, line, true);
        if (state.stopMainOutput) {
            state.pendingMainLine = '';
            return;
        }
    }
}

function appendStreamingMainLine(state: StreamingParseState, line: string, withNewline: boolean): void {
    if (state.stopMainOutput) {
        return;
    }

    const trimmed = line.trim();
    const isQuoted = trimmed.startsWith('>');

    if (state.skipQuotedBlock) {
        if (isQuoted) {
            return;
        }
        state.skipQuotedBlock = false;
    }

    if (isStreamingStopLine(line)) {
        state.stopMainOutput = true;
        return;
    }

    if (injectedPromptControlLinePattern.test(line)) {
        return;
    }

    const skipToolLine = shouldSkipStreamingToolLine(trimmed);
    if (skipToolLine.skip) {
        state.skipQuotedBlock = skipToolLine.quotedBlock;
        return;
    }

    state.committedMain += line;
    if (withNewline) {
        state.committedMain += '\n';
    }
}

function getStreamingPendingMain(state: StreamingParseState): string {
    const pending = state.pendingMainLine;
    if (!pending || state.stopMainOutput || state.skipQuotedBlock) {
        return '';
    }

    if (isStreamingStopLine(pending) || injectedPromptControlLinePattern.test(pending)) {
        return '';
    }

    const skipToolLine = shouldSkipStreamingToolLine(pending.trim());
    if (skipToolLine.skip) {
        return '';
    }

    return pending;
}

function countMarkdownTablePipes(line: string): number {
    return (line.match(/\|/g) || []).length;
}

function isMarkdownTableDelimiterLine(line: string): boolean {
    const trimmed = line.trim();
    return countMarkdownTablePipes(trimmed) >= 2 && trimmed.includes('-') && /^[|:\-\s]+$/.test(trimmed);
}

function isProbableMarkdownTableLine(line: string): boolean {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('```')) {
        return false;
    }

    if (isMarkdownTableDelimiterLine(trimmed)) {
        return true;
    }

    return countMarkdownTablePipes(trimmed) >= 2;
}

function normalizeMarkdownTableWhitespace(content: string): string {
    if (!content.includes('|')) {
        return content;
    }

    const lines = content.split('\n');
    const normalized: string[] = [];

    for (let index = 0; index < lines.length; index += 1) {
        const line = lines[index];
        if (line.trim()) {
            normalized.push(line);
            continue;
        }

        const previousLine = normalized.length > 0 ? normalized[normalized.length - 1] : '';
        if (!isProbableMarkdownTableLine(previousLine)) {
            normalized.push(line);
            continue;
        }

        let nextIndex = index + 1;
        while (nextIndex < lines.length && !lines[nextIndex].trim()) {
            nextIndex += 1;
        }

        if (nextIndex < lines.length && isProbableMarkdownTableLine(lines[nextIndex])) {
            continue;
        }

        normalized.push(line);
    }

    return normalized.join('\n');
}

function extractResumeActionMeta(content: string): { mainContent: string; hasResumeAction: boolean; resumeLabel: string } {
    let mainContent = content;
    let hasResumeAction = false;
    let resumeLabel = '继续';
    const resumeActionPattern = /<Action\s+type=["']resume_prompt["']\s+label=["'](.+?)["']\s*\/>/gi;
    const matchInfo = resumeActionPattern.exec(mainContent);
    if (matchInfo) {
        hasResumeAction = true;
        resumeLabel = matchInfo[1] || '继续';
        mainContent = mainContent.replace(resumeActionPattern, '').trim();
    }

    return { mainContent, hasResumeAction, resumeLabel };
}

function materializeStreamingParse(state: StreamingParseState): ParsedMessageContent {
    const rawMain = normalizeMarkdownTableWhitespace(`${state.committedMain}${getStreamingPendingMain(state)}`).replace(/\n{3,}/g, '\n\n');
    const pendingThinking = stripInjectedChatControlContent(state.currentThinkingBlock);
    const allThinkingBlocks = pendingThinking
        && isMeaningfulRenderableChatText(pendingThinking)
        ? [...state.thinkingBlocks, pendingThinking]
        : state.thinkingBlocks;
    const cleanedMain = rawMain.trimStart();
    const resumeMeta = extractResumeActionMeta(cleanedMain);

    return {
        thinkingContents: allThinkingBlocks,
        mainContent: resumeMeta.mainContent,
        hasResumeAction: resumeMeta.hasResumeAction,
        resumeLabel: resumeMeta.resumeLabel,
    };
}

function appendStreamingChunk(state: StreamingParseState, chunk: string): void {
    if (!chunk) {
        return;
    }

    state.rawContent += chunk;
    state.carry += chunk;

    for (;;) {
        if (!state.carry) {
            return;
        }

        if (state.inThinking) {
            const match = findEarliestTokenIndex(state.carry, streamingCloseTokens);
            if (match) {
                state.currentThinkingBlock += state.carry.slice(0, match.index);
                state.carry = state.carry.slice(match.index + match.token.length);
                finalizeStreamingThinkingBlock(state);
                state.inThinking = false;
                continue;
            }

            const partialLength = longestStreamingTokenPrefixSuffix(state.carry, streamingCloseTokens);
            const flushLength = state.carry.length - partialLength;
            if (flushLength > 0) {
                state.currentThinkingBlock += state.carry.slice(0, flushLength);
                state.carry = state.carry.slice(flushLength);
            }
            return;
        }

        const match = findEarliestTokenIndex(state.carry, streamingOpenTokens);
        if (match) {
            appendStreamingMainChunk(state, state.carry.slice(0, match.index));
            state.carry = state.carry.slice(match.index + match.token.length);
            state.inThinking = true;
            continue;
        }

        const partialLength = longestStreamingTokenPrefixSuffix(state.carry, streamingOpenTokens);
        const flushLength = state.carry.length - partialLength;
        if (flushLength > 0) {
            appendStreamingMainChunk(state, state.carry.slice(0, flushLength));
            state.carry = state.carry.slice(flushLength);
        }
        return;
    }
}

function buildStreamingParseFromScratch(content: string): ParsedMessageContent {
    const state = createStreamingParseState();
    appendStreamingChunk(state, content);
    return materializeStreamingParse(state);
}

function sanitizeMainContentBlock(content: string, isStreaming: boolean): string {
    let normalized = content;

    if (normalized.includes('>')) {
        const lines = normalized.split('\n');
        const filteredLines: string[] = [];
        let skipQuoted = false;
        const len = lines.length;
        for (let i = 0; i < len; i++) {
            const line = lines[i];
            const trimmed = line.trim();
            const isQuoted = trimmed.startsWith('>');

            if (skipQuoted) {
                if (isQuoted) continue;
                skipQuoted = false;
            }

            if (isQuoted) {
                if (trimmed.includes('[Tool Call') || trimmed.includes('[Tool Output') || trimmed.includes('[Error]') || trimmed.includes('Output:')) {
                    const clean = trimmed.replace(/^>\s?/, '');
                    if (clean.startsWith('[Tool Call') || clean.startsWith('[Tool Output') || clean.startsWith('[Error]') || clean.startsWith('Output:')) {
                        skipQuoted = true;
                        continue;
                    }
                }
            } else if (trimmed.startsWith('[Tool Call') || trimmed.startsWith('[Tool Output') || trimmed.startsWith('[Error]')) {
                continue;
            }
            filteredLines.push(line);
        }
        normalized = filteredLines.join('\n').replace(/\n{3,}/g, '\n\n');
    }

    return normalizeMarkdownTableWhitespace(stripInjectedPromptContent(isStreaming ? normalized.trimStart() : normalized.trim()));
}

function pushMainContentFragment(fragments: ContentFragment[], rawContent: string, isStreaming: boolean): void {
    const cleanedMain = sanitizeMainContentBlock(rawContent, isStreaming);
    const resumeMeta = extractResumeActionMeta(cleanedMain);

    if (!isMeaningfulRenderableChatText(resumeMeta.mainContent) && !resumeMeta.hasResumeAction) {
        return;
    }

    fragments.push({
        type: 'main',
        content: resumeMeta.mainContent,
        hasResumeAction: resumeMeta.hasResumeAction,
        resumeLabel: resumeMeta.resumeLabel,
    });
}

function findNextThinkingMatch(content: string, startIndex: number): { index: number; length: number; thinking: string } | null {
    const source = content.slice(startIndex);
    const patterns = [
        /(?:<|&lt;)(?:thinking|think|tinking)\b(?:>|&gt;)([\s\S]*?)(?:(?:<\/|&lt;\/)(?:thinking|think|tinking)(?:>|&gt;)|$)/i,
        /(?:<|&lt;)details\b(?:>|&gt;)\s*(?:<|&lt;)summary\b(?:>|&gt;)\s*Thinking Process\s*(?:<\/|&lt;\/)summary(?:>|&gt;)([\s\S]*?)(?:(?:<\/|&lt;\/)details(?:>|&gt;)|$)/i,
    ];

    let bestMatch: { index: number; length: number; thinking: string } | null = null;

    for (const pattern of patterns) {
        const match = pattern.exec(source);
        if (!match || match.index === undefined) {
            continue;
        }

        const candidate = {
            index: startIndex + match.index,
            length: match[0].length,
            thinking: stripInjectedChatControlContent(match[1]?.trim() ?? ''),
        };

        if (!bestMatch || candidate.index < bestMatch.index) {
            bestMatch = candidate;
        }
    }

    return bestMatch;
}

function splitContentFragments(contentInput: string, isStreaming: boolean): ContentFragment[] {
    if (!contentInput) {
        return [];
    }

    const fragments: ContentFragment[] = [];
    let cursor = 0;

    for (;;) {
        const nextThinking = findNextThinkingMatch(contentInput, cursor);
        if (!nextThinking) {
            pushMainContentFragment(fragments, contentInput.slice(cursor), isStreaming);
            break;
        }

        pushMainContentFragment(fragments, contentInput.slice(cursor, nextThinking.index), false);
        if (nextThinking.thinking) {
            fragments.push({ type: 'thinking', content: nextThinking.thinking });
        }
        cursor = nextThinking.index + nextThinking.length;
    }

    return fragments;
}

function parseMessageContent(contentInput: string, isStreaming: boolean): ParsedMessageContent {
    const fragments = splitContentFragments(contentInput, isStreaming);
    const thinkingContents = fragments
        .filter((fragment): fragment is Extract<ContentFragment, { type: 'thinking' }> => fragment.type === 'thinking')
        .map((fragment) => fragment.content);
    const mainFragments = fragments.filter((fragment): fragment is Extract<ContentFragment, { type: 'main' }> => fragment.type === 'main');
    let hasResumeAction = false;
    let resumeLabel = '继续';

    for (let index = mainFragments.length - 1; index >= 0; index -= 1) {
        if (mainFragments[index].hasResumeAction) {
            hasResumeAction = true;
            resumeLabel = mainFragments[index].resumeLabel;
            break;
        }
    }

    return {
        thinkingContents,
        mainContent: mainFragments.map((fragment) => fragment.content).filter(Boolean).join('\n\n'),
        hasResumeAction,
        resumeLabel,
    };
}

function stripInjectedPromptContent(input: string, trimEdges = true): string {
    return stripInjectedChatControlContent(input, trimEdges);
}

interface ChatCardProps {
    children: React.ReactNode;
    className?: string;
    cardClassName?: string;
    contentClassName?: string;
    header?: React.ReactNode;
    isCollapsible?: boolean;
    isOpen?: boolean;
    onToggle?: () => void;
    isVisible?: boolean;
    fullWidth?: boolean;
    onClick?: (e: React.MouseEvent<HTMLDivElement>) => void;
    animateVisibility?: boolean;
    allowOverflowVisible?: boolean;
}

export const ChatCard: React.FC<ChatCardProps> = ({
    children,
    className,
    cardClassName,
    contentClassName,
    header,
    isCollapsible = false,
    isOpen = true,
    onToggle,
    fullWidth = true,
    onClick,
    allowOverflowVisible = false,
}) => {
    const shouldRenderBody = !isCollapsible || isOpen;

    return (
        <div 
            style={{
                ...(fullWidth ? { width: '100%', minWidth: '100%', display: 'block' } : { display: 'inline-block', maxWidth: '100%' }),
            }}
            className={cn(
                fullWidth && "w-full min-w-full",
                className
            )}
            onClick={onClick}
        >
            <div
                className={cn(
                    "rounded-xl border",
                    !allowOverflowVisible && "overflow-hidden",
                    "bg-white dark:bg-zinc-950",
                    "border-zinc-200 dark:border-zinc-800",
                    "text-zinc-900 dark:text-zinc-100",
                    cardClassName
                )}
            >
                {header && (
                    <div 
                        onClick={isCollapsible ? onToggle : undefined}
                        className={cn(
                            "flex items-center justify-between px-4 py-2.5 border-b",
                            !USE_LIGHTWEIGHT_CHAT_CARD_MOTION && "transition-colors",
                            "bg-zinc-50 dark:bg-zinc-900",
                            "border-zinc-200 dark:border-zinc-800",
                            isCollapsible && "cursor-pointer hover:bg-black/5 dark:hover:bg-white/5"
                        )}
                    >
                        {header}
                    </div>
                )}
                
                {shouldRenderBody && (
                    <div className="w-full">
                        <div className={cn("relative w-full", contentClassName)}>
                            {children}
                        </div>
                    </div>
                )}
            </div>
        </div>
    );
};

const ThinkingSection: React.FC<{ content: string; isThinking: boolean }> = ({ content, isThinking }) => {
    // Local state for manual toggle
    const [isOpen, setIsOpen] = useState(isThinking);
    const scrollRef = useRef<HTMLDivElement>(null);
    const bottomRef = useRef<HTMLDivElement>(null);
    const [shouldAutoScroll, setShouldAutoScroll] = useState(true);
    const [isAtBottom, setIsAtBottom] = useState(true);

    // Synchronize with isThinking prop: open when thinking starts, close when thinking finishes
    useEffect(() => {
        setIsOpen(isThinking);
    }, [isThinking]);

    // Handle scroll events to determine if we should auto-scroll
    const handleScroll = () => {
        if (!scrollRef.current) return;
        const { scrollTop, scrollHeight, clientHeight } = scrollRef.current;
        // If user is within 20px of the bottom, enable auto-scroll
        const atBottom = scrollHeight - scrollTop - clientHeight < 20;
        setShouldAutoScroll(atBottom);
        setIsAtBottom(atBottom);
    };

    // Check if content is scrollable on mount/update
    useEffect(() => {
        if (scrollRef.current) {
            const { scrollHeight, clientHeight } = scrollRef.current;
            setIsAtBottom(scrollHeight <= clientHeight);
        }
    }, [content, isOpen]);

    // Auto-scroll logic: Run on every render/update if auto-scroll is enabled
    React.useLayoutEffect(() => {
        if (!isThinking || !shouldAutoScroll || !scrollRef.current) {
            return;
        }

        const container = scrollRef.current;
        container.scrollTop = container.scrollHeight;
    });

    return (
        <ChatCard
            isCollapsible={true}
            isOpen={isOpen}
            onToggle={() => setIsOpen(!isOpen)}
            header={
                <>
                    <div className="flex items-center gap-2">
                        {isThinking ? (
                            <div className={cn("w-2 h-2 rounded-full bg-blue-500", !USE_LIGHTWEIGHT_CHAT_CARD_MOTION && "animate-pulse")}></div>
                        ) : (
                            <div className="w-2 h-2 rounded-full bg-zinc-400 dark:bg-zinc-600"></div>
                        )}
                        <span className="text-xs font-bold text-zinc-600 dark:text-zinc-300 font-mono tracking-tight uppercase">
                            {isThinking ? '思考中...' : '思考过程'}
                        </span>
                    </div>
                    <div className="flex items-center gap-2">
                        {isOpen ? <ChevronDown className="w-4 h-4 text-zinc-400" /> : <ChevronRight className="w-4 h-4 text-zinc-400" />}
                    </div>
                </>
            }
        >
            <div className="relative">
                <div 
                    ref={scrollRef}
                    onScroll={handleScroll}
                    className="p-4 overflow-y-auto max-h-[27vh] scrollbar-thin scrollbar-thumb-zinc-200 dark:scrollbar-thumb-zinc-800 scrollbar-track-transparent"
                >
                    <div className="markdown-body text-xs leading-normal text-zinc-900 dark:text-zinc-100 break-words">
                        {isThinking ? (
                            <div className="whitespace-pre-line font-sans text-xs leading-normal text-zinc-900 dark:text-zinc-100">
                                {content}
                            </div>
                        ) : (
                        <ThrottledMarkdown content={content} />
                        )}
                        {/* Invisible anchor for thinking scroll */}
                        <div ref={bottomRef} className="h-px w-full" />
                    </div>
                </div>
                {!isAtBottom && isOpen && (
                    <div className="absolute bottom-0 left-0 right-0 h-12 bg-gradient-to-t from-white dark:from-zinc-950 to-transparent pointer-events-none" />
                )}
            </div>
        </ChatCard>
    );
};

// Helper to localize tool names
const getToolNameCN = (name: string): string => {
    const map: Record<string, string> = {
        'read_file': '读取文件',
        'write_file': '写入文件',
        'create_file': '创建文件',
        'replace_file': '替换文件',
        'edit_file': '编辑文件',
        'search_replace': '搜索替换',
        'run_command': '执行命令',
        'ls': '列出文件',
        'dir': '列出文件',
        'cat': '查看文件',
        'search_codebase': '搜索代码库',
        'glob': '文件名匹配',
        'grep': '正则搜索',
        'ask_user': '询问用户',
        'vscode_askQuestions': '询问用户',
        'get_workspace_state': '获取工作区状态',
        'bash': '命令行',
        'run_in_terminal': '运行终端命令',
        'send_to_terminal': '发送终端输入',
        'get_terminal_output': '获取终端输出',
        'kill_terminal': '终止终端',
        'terminal_last_command': '查看上一条终端命令',
        'terminal_selection': '查看终端选中内容',
        'patch': '应用补丁',
        'apply_patch': '应用补丁',
        'cleanup_workspace': '清理工作区',
        'daily_info': '日常信息',
        'visual-analysis': '图像分析',
        'manage_cognitive_state': '认知状态管理',
        'reload_environment': '重载环境变量',
        'manage_file_history': '文件历史管理',
        'send_notification': '发送通知',
        'repo_map': '代码库地图',
        'scan_desktop': '扫描桌面',
        'stop_command': '停止命令',
        'manage_tools': '工具管理',
        'fetch': '获取网页',
        'delete_file': '删除文件',
        'batch_write': '批量写入',
        'manage_plan': '计划管理',
        'manage_schedule': '日程管理',
        'schedule_task': '定时任务',
        'download_file': '下载文件',
        'check_command_status': '检查命令状态',
        'planner': '规划器',
        'delegate_task': '委派子任务',
        'batch_delegate_task': '批量委派子任务',
        'manage_worker': '管理子智能体',
        'wait_for_workers': '等待子智能体',
        'agent': '智能体',
        'get_diagnostics': '获取诊断信息',
        'get_errors': '检查错误',
        'grep_search': '文本搜索',
        'file_search': '文件搜索',
        'semantic_search': '语义搜索',
        'list_dir': '查看目录',
        'create_directory': '创建目录',
        'fetch_webpage': '抓取网页',
        'open_browser_page': '打开浏览器页面',
        'read_page': '读取页面',
        'click_element': '点击页面元素',
        'type_in_page': '输入页面内容',
        'hover_element': '悬停页面元素',
        'drag_element': '拖拽页面元素',
        'navigate_page': '页面跳转',
        'screenshot_page': '页面截图',
        'view_image': '查看图片',
        'run_playwright_code': '执行浏览器脚本',
        'handle_dialog': '处理页面弹窗',
        'github_repo': '搜索 GitHub 仓库',
        'runTests': '运行测试',
        'testFailure': '查看测试失败详情',
        'run_task': '运行任务',
        'create_and_run_task': '创建并运行任务',
        'manage_todo_list': '更新任务清单',
        'task_complete': '标记任务完成',
        'get_changed_files': '查看代码变更',
        'memory': '记忆管理',
        'resolve_memory_file_uri': '解析记忆文件',
        'runSubagent': '调用子智能体',
        'configure_python_environment': '配置 Python 环境',
        'install_python_packages': '安装 Python 包',
        'get_python_executable_details': '获取 Python 解释器信息',
        'get_python_environment_details': '获取 Python 环境信息',
        'create_new_workspace': '创建新工作区',
        'create_new_jupyter_notebook': '创建 Jupyter 笔记本',
        'edit_notebook_file': '编辑 Jupyter 笔记本',
        'run_notebook_cell': '运行笔记本单元',
        'copilot_getNotebookSummary': '查看笔记本摘要',
        'renderMermaidDiagram': '渲染 Mermaid 图表',
        'container-tools_get-config': '获取容器工具配置',
        'mcp_pylance_mcp_s_pylanceRunCodeSnippet': '运行 Python 代码片段',
        'mcp_pylance_mcp_s_pylanceDocString': '查看 Python 文档字符串',
        'mcp_pylance_mcp_s_pylanceFileSyntaxErrors': '检查 Python 语法错误',
        'mcp_pylance_mcp_s_pylanceSyntaxErrors': '检查代码语法',
        'mcp_pylance_mcp_s_pylanceWorkspaceRoots': '查看工作区根目录',
        'mcp_pylance_mcp_s_pylanceWorkspaceUserFiles': '查看工作区 Python 文件',
        'mcp_pylance_mcp_s_pylanceImports': '查看 Python 导入',
        'mcp_pylance_mcp_s_pylanceInstalledTopLevelModules': '查看已安装模块',
        'mcp_pylance_mcp_s_pylanceInvokeRefactoring': '执行 Python 重构',
        'mcp_pylance_mcp_s_pylancePythonEnvironments': '查看 Python 环境',
        'mcp_pylance_mcp_s_pylanceSettings': '查看 Pylance 设置',
        'mcp_pylance_mcp_s_pylanceUpdatePythonEnvironment': '切换 Python 环境',
        'mcp_pylance_mcp_s_pylanceDocuments': '查询 Pylance 文档'
    };
    return map[name] || name;
};

// Helper to format tool args for display
const extractPatchTarget = (input: string): string | null => {
    const match = input.match(/\*\*\*\s+(?:Update|Add|Delete)\s+File:\s+([^\r\n]+?)(?:\s+->\s+[^\r\n]+)?\s*(?:\r?\n|$)/);
    return match?.[1]?.trim() || null;
};

const extractBatchWriteTarget = (input: string): string | null => {
    try {
        const args = JSON.parse(input);
        if (args && typeof args === 'object' && args.files && typeof args.files === 'object') {
            const firstPath = Object.keys(args.files)[0];
            if (firstPath) return firstPath;
        }
    } catch {
        const match = input.match(/"files"\s*:\s*\{\s*"([^\"]+)"/);
        if (match?.[1]) return match[1];
    }

    return null;
};

const extractJsonishStringField = (input: string, fieldNames: string[]): string | null => {
    for (const fieldName of fieldNames) {
        const match = input.match(new RegExp(`"${fieldName}"\\s*:\\s*"([^\"]*)(?:"|$)`));
        if (!match?.[1]) continue;

        return match[1]
            .replace(/\\n/g, '\n')
            .replace(/\\t/g, '\t')
            .replace(/\\"/g, '"')
            .replace(/\\\\/g, '\\');
    }

    return null;
};

const formatToolArgs = (name: string, input: string): string => {
    const patchTarget = extractPatchTarget(input);
    if (patchTarget) return patchTarget;

    const batchTarget = extractBatchWriteTarget(input);
    if (batchTarget) return batchTarget;

    try {
        const args = JSON.parse(input);
        
        // File paths
        if (args.file_path) return args.file_path;
        if (args.filePath) return args.filePath;
        if (args.path) return args.path;
        if (args.dir) return args.dir;
        if (args.dirPath) return args.dirPath;
        if (args.filename) return args.filename;
        if (args.fileName) return args.fileName;
        if (args.old_path) return args.old_path;
        if (args.oldPath) return args.oldPath;
        if (args.new_path) return args.new_path;
        if (args.newPath) return args.newPath;
        
        // Command
        if (args.command) return args.command;
        
        // Search
        if (args.query) return `"${args.query}"`;
        if (args.pattern) return `"${args.pattern}"`;
        
        // Generic "content" preview (if short)
        if (args.content && args.content.length < 50) return args.content;
        
        // If nothing specific matched, try first string value
        const values = Object.values(args);
        for (const val of values) {
            if (typeof val === 'string' && val.length > 0 && val.length < 100) {
                return val;
            }
        }
    } catch {
        // Running tool calls often carry partial JSON chunks. Extract key fields from
        // incomplete payloads so the card can show the target file before completion.
        const fallbackPath = extractJsonishStringField(input, ['file_path', 'filePath', 'path', 'dir', 'dirPath', 'filename', 'fileName', 'old_path', 'oldPath', 'new_path', 'newPath']);
        if (fallbackPath) return fallbackPath;

        const fallbackPatchTarget = extractPatchTarget(input);
        if (fallbackPatchTarget) return fallbackPatchTarget;

        const fallbackBatchTarget = extractBatchWriteTarget(input);
        if (fallbackBatchTarget) return fallbackBatchTarget;

        const fallbackCommand = extractJsonishStringField(input, ['command']);
        if (fallbackCommand) return fallbackCommand;

        const fallbackQuery = extractJsonishStringField(input, ['query', 'pattern']);
        if (fallbackQuery) return `"${fallbackQuery}"`;

        // Not JSON, just return raw string trimmed
        return input.length > 50 ? input.substring(0, 50) + '...' : input;
    }
    return '';
};

const getToolTargetDisplay = (name: string, input: string): { label: string; detail?: string } | null => {
    const raw = formatToolArgs(name, input).trim();
    if (!raw) return null;

    const unquoted = raw.replace(/^"([\s\S]*)"$/, '$1').trim();
    if (!unquoted) return null;

    const looksLikePath = /[\\/]/.test(unquoted) || /\.[A-Za-z0-9_-]+$/.test(unquoted);
    if (!looksLikePath) {
        return {
            label: unquoted.length > 60 ? unquoted.slice(0, 57) + '...' : unquoted
        };
    }

    const trimmedPath = unquoted.replace(/[\\/]+$/, '');
    const filename = trimmedPath.split(/[\\/]/).pop() || trimmedPath;
    return {
        label: filename,
        detail: trimmedPath !== filename ? trimmedPath : undefined
    };
};

const parseToolMetadata = (metadata: unknown): Record<string, unknown> | null => {
    if (!metadata) return null;
    if (typeof metadata === 'string') {
        try {
            const parsed = JSON.parse(metadata);
            return parsed && typeof parsed === 'object' ? parsed as Record<string, unknown> : null;
        } catch {
            return null;
        }
    }
    return typeof metadata === 'object' ? metadata as Record<string, unknown> : null;
};

const truncateToolText = (value: string, max = 88): string => {
    const normalized = value.replace(/\s+/g, ' ').trim();
    if (!normalized) return '';
    if (normalized.length <= max) return normalized;
    return `${normalized.slice(0, Math.max(0, max - 3))}...`;
};

const extractToolOutputHeadline = (output: string): string | null => {
    if (!output) return null;

    const lines = output
        .split(/\r?\n/)
        .map((line) => line.trim())
        .filter(Boolean)
        .filter((line) => line !== 'Result:' && line !== 'Output:' && !line.startsWith('```'));

    if (lines.length === 0) return null;
    return truncateToolText(lines[0]);
};

const extractToolArgumentDetails = (name: string, input: string): Array<{ label: string; value: string }> => {
    const details: Array<{ label: string; value: string }> = [];
    const pushDetail = (label: string, value: unknown) => {
        if (typeof value !== 'string') return;
        const normalized = value.trim();
        if (!normalized) return;
        if (details.some((item) => item.value === normalized)) return;
        details.push({ label, value: truncateToolText(normalized, 72) });
    };

    const patchTarget = extractPatchTarget(input);
    if (patchTarget) {
        pushDetail('目标', patchTarget);
    }

    const batchTarget = extractBatchWriteTarget(input);
    if (batchTarget) {
        pushDetail('批量', batchTarget);
    }

    try {
        const args = JSON.parse(input);
        if (!args || typeof args !== 'object') {
            return details;
        }

        const obj = args as Record<string, unknown>;
        pushDetail('文件', typeof obj.file_path === 'string' ? obj.file_path : typeof obj.filePath === 'string' ? obj.filePath : typeof obj.path === 'string' ? obj.path : typeof obj.file === 'string' ? obj.file : '');
        pushDetail('目录', typeof obj.dir === 'string' ? obj.dir : typeof obj.dirPath === 'string' ? obj.dirPath : typeof obj.directory === 'string' ? obj.directory : '');
        pushDetail('命令', obj.command);
        pushDetail('查询', typeof obj.query === 'string' ? obj.query : typeof obj.pattern === 'string' ? obj.pattern : '');
        pushDetail('地址', typeof obj.url === 'string' ? obj.url : typeof obj.base_url === 'string' ? obj.base_url : '');
        pushDetail('动作', obj.action);
        pushDetail('标识', typeof obj.id === 'string' ? obj.id : typeof obj.command_id === 'string' ? obj.command_id : '');
    } catch {
        if (name === 'run_command' || name === 'bash') {
            const command = extractJsonishStringField(input, ['command']);
            pushDetail('命令', command || input);
        } else {
            const path = extractJsonishStringField(input, ['file_path', 'filePath', 'path', 'file', 'dir', 'dirPath', 'directory']);
            if (path) pushDetail('目标', path);
            const query = extractJsonishStringField(input, ['query', 'pattern']);
            if (query) pushDetail('查询', query);
            const url = extractJsonishStringField(input, ['url']);
            if (url) pushDetail('地址', url);
        }
    }

    return details.slice(0, 3);
};

const getToolCardVisual = (toolName: string): ToolCardVisual => {
    const normalized = toolName.toLowerCase();

    if (['read', 'read_file', 'grep', 'search_codebase', 'repo_map', 'lookup_symbol', 'get_diagnostics'].includes(normalized)) {
        return {
            icon: Search,
            iconClassName: 'text-sky-600 dark:text-sky-300',
            iconContainerClassName: 'bg-sky-100 dark:bg-sky-900/30 border-sky-200 dark:border-sky-800/60',
        };
    }

    if (['fetch', 'download_file', 'visual-analysis'].includes(normalized)) {
        return {
            icon: normalized === 'download_file' ? Download : Globe,
            iconClassName: 'text-emerald-600 dark:text-emerald-300',
            iconContainerClassName: 'bg-emerald-100 dark:bg-emerald-900/30 border-emerald-200 dark:border-emerald-800/60',
        };
    }

    if (['write', 'write_file', 'create_file', 'replace_file', 'edit', 'edit_file', 'patch', 'apply_patch', 'search_replace', 'batch_write', 'delete', 'delete_file'].includes(normalized)) {
        return {
            icon: Braces,
            iconClassName: 'text-violet-600 dark:text-violet-300',
            iconContainerClassName: 'bg-violet-100 dark:bg-violet-900/30 border-violet-200 dark:border-violet-800/60',
        };
    }

    if (['run_command', 'bash', 'check_command_status', 'stop_command', 'app_launcher'].includes(normalized)) {
        return {
            icon: TerminalSquare,
            iconClassName: 'text-amber-600 dark:text-amber-300',
            iconContainerClassName: 'bg-amber-100 dark:bg-amber-900/30 border-amber-200 dark:border-amber-800/60',
        };
    }

    if (['delegate_task', 'batch_delegate_task', 'manage_worker', 'wait_for_workers', 'agent'].includes(normalized)) {
        return {
            icon: GitBranch,
            iconClassName: 'text-fuchsia-600 dark:text-fuchsia-300',
            iconContainerClassName: 'bg-fuchsia-100 dark:bg-fuchsia-900/30 border-fuchsia-200 dark:border-fuchsia-800/60',
        };
    }

    return {
        icon: Wrench,
        iconClassName: 'text-zinc-600 dark:text-zinc-300',
        iconContainerClassName: 'bg-zinc-100 dark:bg-zinc-900/30 border-zinc-200 dark:border-zinc-800/60',
    };
};

const getToolStatusBadge = (execution: ToolExecution): { label: string; className: string } => {
    if (execution.status === 'running') {
        if (execution.isFinalizing) {
            return {
                label: '收尾中',
                className: 'bg-amber-100 text-amber-700 border-amber-200 dark:bg-amber-950/60 dark:text-amber-300 dark:border-amber-900/70',
            };
        }
        return {
            label: '进行中',
            className: 'bg-blue-100 text-blue-700 border-blue-200 dark:bg-blue-950/60 dark:text-blue-300 dark:border-blue-900/70',
        };
    }
    if (execution.status === 'failed') {
        return {
            label: '失败',
            className: 'bg-red-100 text-red-700 border-red-200 dark:bg-red-950/60 dark:text-red-300 dark:border-red-900/70',
        };
    }
    return {
        label: '完成',
        className: 'bg-green-100 text-green-700 border-green-200 dark:bg-green-950/60 dark:text-green-300 dark:border-green-900/70',
    };
};

const getToolCardSummary = (execution: ToolExecution): string | null => {
    const metadata = parseToolMetadata(execution.metadata);
    const outputHeadline = extractToolOutputHeadline(execution.output);

    if (execution.status === 'running') {
        if (execution.isFinalizing) return '正在同步最后的输出';
        if (execution.isCodeStream) return '正在持续接收代码输出';
        if (execution.name === 'run_command' || execution.name === 'bash') return '命令已启动，等待终端输出';
        return '工具调用已发出，等待返回结果';
    }

    if (execution.status === 'failed') {
        return outputHeadline || '工具执行失败，请展开查看详情';
    }

    if (metadata && typeof metadata.diff === 'string' && metadata.diff.trim()) {
        return '已生成变更对比，可展开查看差异';
    }

    if (['write', 'write_file', 'create_file', 'replace_file', 'edit', 'edit_file', 'patch', 'apply_patch', 'search_replace', 'batch_write', 'delete', 'delete_file'].includes(execution.name)) {
        return outputHeadline || '文件操作已完成';
    }

    if (['run_command', 'bash', 'check_command_status', 'stop_command', 'app_launcher'].includes(execution.name)) {
        return outputHeadline || '命令执行已结束';
    }

    if (['delegate_task', 'batch_delegate_task', 'manage_worker', 'wait_for_workers', 'agent'].includes(execution.name)) {
        return outputHeadline || '子任务调度已完成';
    }

    return outputHeadline || '工具执行已完成';
};

// Simple helper to clean JSON stream to show code content
// Transforms `{"content": "func main()...` -> `func main()...`
const cleanJsonCodeStream = (jsonStr: string): string => {
    if (!jsonStr) return '';

    // 1. Try to extract content from known fields (content, new_str, patch_text, etc.)
    const fields = ['content', 'code', 'text', 'new_str', 'patch_text'];
    for (const field of fields) {
        // Match "field": "VALUE...
        // We use a regex that matches the prefix up to the start of the value string
        const regex = new RegExp(`^[\\s\\S]*"${field}"\\s*:\\s*"`);
        if (regex.test(jsonStr)) {
             let clean = jsonStr.replace(regex, '');
             
             // 3. Unescape basic chars (JSON string encoding)
             clean = clean.replace(/\\n/g, '\n')
                  .replace(/\\t/g, '\t')
                  .replace(/\\"/g, '"')
                  .replace(/\\\\/g, '\\');
             
             // 2. Remove JSON suffix if it looks like the end
             if (clean.endsWith('"}')) {
                 clean = clean.slice(0, -2);
             } else if (clean.endsWith('"')) {
                 clean = clean.slice(0, -1);
             }
             
             return clean;
        }
    }
    
    // Fallback: If no field matched, return as is (maybe it's raw text)
    return jsonStr;
};

interface ToolExecutionViewProps {
    execution: ToolExecution;
    isParentActive?: boolean;
    onCancel?: (executionId: string) => void;
}

const ToolExecutionViewComponent: React.FC<ToolExecutionViewProps> = ({ execution, isParentActive = true, onCancel }) => {
    // FIX: Default to collapsed (false) even if running, to keep UI clean
    // User can expand if they want to see details.
    const [isOpen, setIsOpen] = useState(false);
    const scrollRef = useRef<HTMLDivElement>(null);
    const [isAtBottom, setIsAtBottom] = useState(true);
    const isRunning = execution.status === 'running';
    const isFailed = execution.status === 'failed';
    const codeStatusLabel = isRunning
        ? execution.isFinalizing ? '正在同步最后的输出...' : '正在生成代码...'
        : isFailed ? '代码生成失败' : '代码生成完成';
    const terminalStatusLabel = isRunning
        ? execution.isFinalizing ? '正在同步最后的输出...' : '正在执行...'
        : isFailed ? '执行失败' : '执行完成';
    const toolTargetDisplay = useMemo(() => getToolTargetDisplay(execution.name, execution.input), [execution.name, execution.input]);
    const toolVisual = useMemo(() => getToolCardVisual(execution.name), [execution.name]);
    const toolStatusBadge = useMemo(() => getToolStatusBadge(execution), [execution]);
    const toolSummary = useMemo(() => getToolCardSummary(execution), [execution]);
    const toolArgumentDetails = useMemo(() => extractToolArgumentDetails(execution.name, execution.input), [execution.name, execution.input]);
    const ToolIcon = toolVisual.icon;
    const shouldRenderToolArgumentDetails = toolArgumentDetails.length > 0 && (!USE_LIGHTWEIGHT_CHAT_CARD_MOTION || isOpen);

    // Handle scroll events
    const handleScroll = () => {
        if (!scrollRef.current) return;
        const { scrollTop, scrollHeight, clientHeight } = scrollRef.current;
        const atBottom = scrollHeight - scrollTop - clientHeight < 20;
        setIsAtBottom(atBottom);
    };

    // Check scroll state on content change or open
    useEffect(() => {
        if (!isOpen) return;
        if (scrollRef.current) {
            const { scrollTop, scrollHeight, clientHeight } = scrollRef.current;
            const atBottom = scrollHeight - scrollTop - clientHeight < 20;
            setIsAtBottom(atBottom);
        }
    }, [execution.output, execution.input, isOpen]);

    // Code Stream Fusion View (Also used for write_file even if completed, to show what was written)
    if (execution.isCodeStream) {
        // ... (Code processing logic remains the same)
        let codeContent = execution.output;
        if (execution.status === 'running') {
            let source = codeContent || execution.input;
            if (source) {
                codeContent = cleanJsonCodeStream(source);
                if ((!codeContent || codeContent.trim() === '{}') && source.length > 0) {
                     if (source.includes('"content"')) {
                         const parts = source.split('"content"');
                         if (parts.length > 1) {
                             let contentPart = parts[1];
                             contentPart = contentPart.replace(/^[\s:]+"/, '');
                             contentPart = contentPart.replace(/\\n/g, '\n').replace(/\\t/g, '\t').replace(/\\"/g, '"').replace(/\\\\/g, '\\');
                             codeContent = contentPart;
                         }
                     } else {
                         if (source.trim() === '{}') {
                             codeContent = '';
                         } else {
                             codeContent = source;
                         }
                     }
                }
            }
        } else if (!codeContent && execution.input) {
             try {
                 const inputObj = JSON.parse(execution.input);
                 codeContent = inputObj.content || inputObj.code || inputObj.text || inputObj.new_str || inputObj.patch_text || '';
             } catch {
                 codeContent = cleanJsonCodeStream(execution.input);
             }
        } else if (codeContent) {
             codeContent = cleanJsonCodeStream(codeContent);
        }

        // Detect language from filename in input if possible
        let language = 'go'; // default
        if (execution.input) {
            try {
                const inputObj = JSON.parse(execution.input);
                const filename = inputObj.path || inputObj.file_path || inputObj.filePath || inputObj.filename || inputObj.fileName || extractPatchTarget(execution.input) || extractBatchWriteTarget(execution.input) || '';
                if (filename.endsWith('.ts') || filename.endsWith('.tsx')) language = 'typescript';
                else if (filename.endsWith('.js') || filename.endsWith('.jsx')) language = 'javascript';
                else if (filename.endsWith('.py')) language = 'python';
                else if (filename.endsWith('.html')) language = 'html';
                else if (filename.endsWith('.css')) language = 'css';
                else if (filename.endsWith('.json')) language = 'json';
                else if (filename.endsWith('.md')) language = 'markdown';
                else if (filename.endsWith('.rs')) language = 'rust';
                else if (filename.endsWith('.java')) language = 'java';
                else if (filename.endsWith('.c') || filename.endsWith('.cpp')) language = 'cpp';
                else if (filename.endsWith('.sh')) language = 'bash';
            } catch {
                const filename = extractPatchTarget(execution.input) || extractBatchWriteTarget(execution.input) || extractJsonishStringField(execution.input, ['file_path', 'filePath', 'path', 'filename', 'fileName']) || '';
                if (filename.endsWith('.ts') || filename.endsWith('.tsx')) language = 'typescript';
                else if (filename.endsWith('.js') || filename.endsWith('.jsx')) language = 'javascript';
                else if (filename.endsWith('.py')) language = 'python';
                else if (filename.endsWith('.html')) language = 'html';
                else if (filename.endsWith('.css')) language = 'css';
                else if (filename.endsWith('.json')) language = 'json';
                else if (filename.endsWith('.md')) language = 'markdown';
                else if (filename.endsWith('.rs')) language = 'rust';
                else if (filename.endsWith('.java')) language = 'java';
                else if (filename.endsWith('.c') || filename.endsWith('.cpp')) language = 'cpp';
                else if (filename.endsWith('.sh')) language = 'bash';
            }
        }

        // Check for Diff in Metadata
        let diffContent = '';
        if (execution.status === 'completed' && execution.metadata) {
            try {
                // Metadata can be a JSON string or an object depending on transport
                const meta = typeof execution.metadata === 'string' ? JSON.parse(execution.metadata) : execution.metadata;
                if (meta && meta.diff) {
                    diffContent = meta.diff;
                }
            } catch (e) {
                console.warn("Failed to parse metadata for diff", e);
            }
        }

        return (
            <ChatCard
                cardClassName="[box-shadow:inset_0_0_0_1px_rgba(228,228,231,0.9)] dark:[box-shadow:inset_0_0_0_1px_rgba(39,39,42,0.95)]"
                isCollapsible={true}
                isOpen={isOpen}
                onToggle={() => setIsOpen(!isOpen)}
                header={
                    <>
                        <div className="flex items-start gap-3 min-w-0 flex-1">
                            <div className={cn('mt-0.5 flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg border', toolVisual.iconContainerClassName)}>
                                <ToolIcon className={cn('h-4 w-4', toolVisual.iconClassName)} />
                            </div>
                            <div className="min-w-0 flex-1">
                                <div className="flex items-center gap-2 min-w-0">
                                    <div className="text-xs font-semibold text-zinc-700 dark:text-zinc-200 font-mono tracking-tight truncate">
                                        {getToolNameCN(execution.name)}
                                    </div>
                                    <span className={cn('inline-flex flex-shrink-0 items-center rounded-full border px-2 py-0.5 text-[10px] font-semibold tracking-wide', toolStatusBadge.className)}>
                                        {toolStatusBadge.label}
                                    </span>
                                </div>
                                <div className="text-[11px] text-zinc-500 dark:text-zinc-400 truncate" title={toolSummary || codeStatusLabel}>
                                    {toolSummary || codeStatusLabel}
                                </div>
                                {toolTargetDisplay && (
                                    <div
                                        className="text-[11px] text-zinc-500 dark:text-zinc-400 truncate"
                                        title={toolTargetDisplay.detail || toolTargetDisplay.label}
                                    >
                                        {toolTargetDisplay.label}
                                    </div>
                                )}
                            </div>
                        </div>
                        <div className="flex items-center gap-2">
                            {execution.status === 'running' && execution.executionId && onCancel && (
                                <button 
                                    onClick={(e) => {
                                        e.stopPropagation();
                                        onCancel(execution.executionId!);
                                    }}
                                    className="group relative rounded-lg bg-red-600 p-1.5 text-white"
                                    title="停止执行"
                                >
                                    <Square className="w-3.5 h-3.5 fill-current" />
                                </button>
                            )}
                            {isOpen ? <ChevronDown className="w-4 h-4 text-zinc-500" /> : <ChevronRight className="w-4 h-4 text-zinc-500" />}
                        </div>
                    </>
                }
            >
                {shouldRenderToolArgumentDetails && (
                    <div className="flex flex-wrap gap-2 border-b border-zinc-200 bg-zinc-50/80 px-4 py-3 dark:border-zinc-800 dark:bg-zinc-900/60">
                        {toolArgumentDetails.map((detail) => (
                            <div key={`${detail.label}-${detail.value}`} className="inline-flex max-w-full items-center gap-1 rounded-full border border-zinc-200 bg-white px-2.5 py-1 text-[11px] text-zinc-600 dark:border-zinc-700 dark:bg-zinc-950 dark:text-zinc-300">
                                <span className="font-semibold text-zinc-500 dark:text-zinc-400">{detail.label}</span>
                                <span className="truncate">{detail.value}</span>
                            </div>
                        ))}
                    </div>
                )}
                {/* Ensure code view has max height and scrolls */}
                <div className="relative">
                    <div 
                        className="relative max-h-[40vh] overflow-y-auto overflow-x-auto bg-transparent text-zinc-900 dark:text-zinc-100 scrollbar-thin scrollbar-thumb-zinc-200 dark:scrollbar-thumb-zinc-800 scrollbar-track-transparent" 
                        ref={scrollRef}
                        onScroll={handleScroll}
                    >
                        <div style={{ minWidth: '100%', width: 'fit-content' }}>
                        {diffContent ? (
                             <div className="flex flex-col">
                                 <div className="bg-zinc-800/30 px-3 py-1 text-xs text-zinc-400 border-b border-zinc-700/30 font-mono">
                                     变更对比
                                 </div>
                                 <SyntaxHighlighter
                                     style={syntaxTheme}
                                     language="diff"
                                     PreTag="div"
                                     customStyle={{ margin: 0, padding: '16px', fontSize: '12px', lineHeight: '1.6', overflowX: 'visible', minHeight: '60px', background: 'transparent' }}
                                     wrapLines={false}
                                     wrapLongLines={false}
                                     showLineNumbers={true}
                                 >
                                     {diffContent}
                                 </SyntaxHighlighter>
                             </div>
                        ) : execution.status === 'running' ? (
                            <div className="bg-[#1e1e1e] px-4 py-3 text-zinc-200">
                                <pre className="whitespace-pre-wrap break-all font-mono text-xs leading-relaxed">{codeContent || ' '}</pre>
                            </div>
                        ) : (
                            <SyntaxHighlighter
                                style={syntaxTheme}
                                language={language}
                                PreTag="div"
                                customStyle={{ margin: 0, padding: '16px', fontSize: '12px', lineHeight: '1.6', overflowX: 'visible', minHeight: '60px', background: 'transparent' }}
                                wrapLines={false}
                                wrapLongLines={false}
                                showLineNumbers={true}
                            >
                                {codeContent || ' '}
                            </SyntaxHighlighter>
                        )}
                        </div>
                    </div>
                    {!isAtBottom && isOpen && (
                        <div className="absolute bottom-0 left-0 right-0 h-12 bg-gradient-to-t from-white/90 dark:from-zinc-950/90 to-transparent pointer-events-none" />
                    )}
                </div>
            </ChatCard>
        );
    }

    // Standard Terminal Output View
    return (
        <ChatCard
            cardClassName="[box-shadow:inset_0_0_0_1px_rgba(228,228,231,0.9)] dark:[box-shadow:inset_0_0_0_1px_rgba(39,39,42,0.95)]"
            isCollapsible={true}
            isOpen={isOpen}
            onToggle={() => setIsOpen(!isOpen)}
            header={
                <>
                    <div className="flex items-start gap-3 overflow-hidden flex-1 mr-2 min-w-0">
                        <div className={cn('mt-0.5 flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg border', toolVisual.iconContainerClassName)}>
                            <ToolIcon className={cn('h-4 w-4', toolVisual.iconClassName)} />
                        </div>
                        <div className="min-w-0 flex-1">
                            <div className="flex items-center gap-2 min-w-0">
                                <div className="text-xs font-semibold text-zinc-700 dark:text-zinc-200 font-mono tracking-tight truncate uppercase">
                                    {getToolNameCN(execution.name)}
                                </div>
                                <span className={cn('inline-flex flex-shrink-0 items-center rounded-full border px-2 py-0.5 text-[10px] font-semibold tracking-wide', toolStatusBadge.className)}>
                                    {toolStatusBadge.label}
                                </span>
                            </div>
                            <div className="text-[11px] text-zinc-500 dark:text-zinc-400 truncate" title={toolSummary || terminalStatusLabel}>
                                {toolSummary || terminalStatusLabel}
                            </div>
                            {toolTargetDisplay && (
                                <div
                                    className="text-[11px] text-zinc-500 dark:text-zinc-400 truncate"
                                    title={toolTargetDisplay.detail || toolTargetDisplay.label}
                                >
                                    {toolTargetDisplay.label}
                                </div>
                            )}
                        </div>
                    </div>
                    <div className="flex items-center gap-2 flex-shrink-0">
                        {execution.status === 'running' && execution.executionId && onCancel && (
                            <button 
                                onClick={(e) => {
                                    e.stopPropagation();
                                    onCancel(execution.executionId!);
                                }}
                                className="group relative rounded-lg bg-red-600 p-1.5 text-white"
                                title="停止执行"
                            >
                                <Square className="w-3.5 h-3.5 fill-current" />
                            </button>
                        )}
                        {isOpen ? <ChevronDown className="w-4 h-4 text-zinc-500" /> : <ChevronRight className="w-4 h-4 text-zinc-500" />}
                    </div>
                </>
            }
        >
            {shouldRenderToolArgumentDetails && (
                <div className="flex flex-wrap gap-2 border-b border-zinc-200 bg-zinc-50/80 px-4 py-3 dark:border-zinc-800 dark:bg-zinc-900/60">
                    {toolArgumentDetails.map((detail) => (
                        <div key={`${detail.label}-${detail.value}`} className="inline-flex max-w-full items-center gap-1 rounded-full border border-zinc-200 bg-white px-2.5 py-1 text-[11px] text-zinc-600 dark:border-zinc-700 dark:bg-zinc-950 dark:text-zinc-300">
                            <span className="font-semibold text-zinc-500 dark:text-zinc-400">{detail.label}</span>
                            <span className="truncate">{detail.value}</span>
                        </div>
                    ))}
                </div>
            )}
            <div className="relative">
                <div 
                    ref={scrollRef}
                    onScroll={handleScroll}
                    className="p-4 font-mono text-xs overflow-y-auto max-h-[40vh] text-zinc-900 dark:text-zinc-100 scrollbar-thin scrollbar-thumb-zinc-800 scrollbar-track-transparent"
                >
                    {execution.output ? (
                        <pre className="whitespace-pre-wrap break-all leading-relaxed">
                            {execution.output}
                        </pre>
                    ) : (
                        <span className="text-zinc-600 italic">等待输出...</span>
                    )}
                </div>
                {!isAtBottom && isOpen && (
                        <div className="absolute bottom-0 left-0 right-0 h-12 bg-gradient-to-t from-white/90 dark:from-zinc-950/90 to-transparent pointer-events-none" />
                )}
            </div>
        </ChatCard>
    );
};

const ToolExecutionView = React.memo(ToolExecutionViewComponent, (prev, next) => {
    const prevExec = prev.execution;
    const nextExec = next.execution;
    return prevExec.id === nextExec.id
        && prevExec.executionId === nextExec.executionId
        && prevExec.name === nextExec.name
        && prevExec.input === nextExec.input
        && prevExec.output === nextExec.output
        && prevExec.status === nextExec.status
        && prevExec.isCodeStream === nextExec.isCodeStream
        && prevExec.isFinalizing === nextExec.isFinalizing
        && prevExec.metadata === nextExec.metadata
        && prev.isParentActive === next.isParentActive
        && prev.onCancel === next.onCancel;
});

const ProgressBar: React.FC<{ progress: { percentage: number; details: string } }> = ({ progress }) => {
    return (
        <div className="w-full max-w-md mb-1 p-3 bg-zinc-50 dark:bg-zinc-900/50 rounded-lg border border-zinc-200 dark:border-zinc-800">
            <div className="flex justify-between items-center mb-1.5">
                <span className="text-xs font-medium text-zinc-600 dark:text-zinc-400 truncate pr-2">
                    {progress.details || '处理中...'}
                </span>
                <span className="text-xs font-bold text-blue-500">
                    {progress.percentage.toFixed(0)}%
                </span>
            </div>
            <div className="w-full h-1.5 bg-zinc-200 dark:bg-zinc-800 rounded-full overflow-hidden">
                <div
                    className="h-full bg-blue-500"
                    style={{ width: `${Math.max(0, Math.min(100, progress.percentage))}%` }}
                />
            </div>
        </div>
    );
};

type ProgressListStatus = PlanProgressStatus;

const PLAN_PROGRESS_BOTTOM_TOLERANCE_PX = 4;

export const isPlanProgressTool = (execution: ToolExecution): boolean => {
    return isSharedPlanProgressTool(execution);
};

const ProgressStatusIcon: React.FC<{ status: ProgressListStatus }> = ({ status }) => {
    if (status === 'completed') {
        return <CheckCircle2 className="h-3.5 w-3.5 fill-zinc-400 text-white dark:fill-zinc-500 dark:text-zinc-950" />;
    }
    if (status === 'failed') {
        return <XCircle className="h-3.5 w-3.5 text-red-500 dark:text-red-400" />;
    }
    if (status === 'running') {
        return <Loader2 className="h-3.5 w-3.5 animate-spin text-blue-500 dark:text-blue-400" />;
    }
    return <Circle className="h-3.5 w-3.5 text-zinc-300 dark:text-zinc-600" />;
};

export const PlanProgressCard: React.FC<{
    progress?: { percentage: number; details: string };
    executions?: ToolExecution[];
    items?: PlanProgressItem[];
    snapshot?: PlanProgressSnapshot | null;
}> = ({ progress, executions = [], items: providedItems, snapshot }) => {
    const scrollRef = useRef<HTMLDivElement | null>(null);
    const lastScrollHeightRef = useRef(0);
    const shouldAutoScrollRef = useRef(true);
    const [shouldAutoScroll, setShouldAutoScroll] = useState(true);

    const items = useMemo(() => {
        if (providedItems) return providedItems;
        if (snapshot) return snapshot.items;
        return derivePlanProgressItems(progress, executions);
    }, [progress, executions, providedItems, snapshot]);

    const hasRunning = items.some((item) => item.status === 'running');
    const derivedPercent = planProgressPercent(items);
    const progressPercent = typeof progress?.percentage === 'number'
        ? Math.round(Math.max(0, Math.min(100, progress.percentage)))
        : derivedPercent;

    const updateAutoScroll = useCallback((nextValue: boolean) => {
        shouldAutoScrollRef.current = nextValue;
        setShouldAutoScroll((previous) => (previous === nextValue ? previous : nextValue));
    }, []);

    const updateScrollMetrics = useCallback((element: HTMLDivElement) => {
        const distanceFromBottom = element.scrollHeight - element.clientHeight - element.scrollTop;
        updateAutoScroll(distanceFromBottom <= PLAN_PROGRESS_BOTTOM_TOLERANCE_PX);
    }, [updateAutoScroll]);

    const scrollToBottom = useCallback(() => {
        const element = scrollRef.current;
        if (!element) {
            return;
        }

        element.scrollTop = element.scrollHeight;
        updateScrollMetrics(element);
    }, [updateScrollMetrics]);

    useLayoutEffect(() => {
        const element = scrollRef.current;
        if (!element) {
            return;
        }

        const previousScrollHeight = lastScrollHeightRef.current;
        const nextScrollHeight = element.scrollHeight;
        const shouldStickToBottom = shouldAutoScrollRef.current;
        lastScrollHeightRef.current = nextScrollHeight;

        if (shouldStickToBottom && nextScrollHeight > previousScrollHeight + 1) {
            scrollToBottom();
            return;
        }

        updateScrollMetrics(element);
    }, [items, scrollToBottom, updateScrollMetrics]);

    useEffect(() => {
        const element = scrollRef.current;
        if (!element) {
            return;
        }

        updateScrollMetrics(element);
        lastScrollHeightRef.current = element.scrollHeight;

        const observer = new ResizeObserver(() => {
            const target = scrollRef.current;
            if (!target) {
                return;
            }

            const previousScrollHeight = lastScrollHeightRef.current;
            const nextScrollHeight = target.scrollHeight;
            const shouldStickToBottom = shouldAutoScrollRef.current;
            lastScrollHeightRef.current = nextScrollHeight;

            if (shouldStickToBottom && nextScrollHeight > previousScrollHeight + 1) {
                scrollToBottom();
                return;
            }

            updateScrollMetrics(target);
        });

        observer.observe(element);
        return () => {
            observer.disconnect();
        };
    }, [items.length, scrollToBottom, updateScrollMetrics]);

    const handleProgressListScroll = useCallback((event: React.UIEvent<HTMLDivElement>) => {
        const element = event.currentTarget;
        updateScrollMetrics(element);
    }, [updateScrollMetrics]);

    const handleProgressListUserIntent = useCallback(() => {
        if (!scrollRef.current) {
            return;
        }

        updateAutoScroll(false);
    }, [updateAutoScroll]);

    if (items.length === 0) return null;

    return (
        <>
            <div className="w-full max-w-[360px] rounded-lg border border-zinc-200 bg-white px-2.5 py-2 text-zinc-900 shadow-sm dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-100">
                <div className="mb-1.5 flex items-center justify-between gap-3">
                    <div className="flex items-center gap-2">
                        <span className="text-xs font-medium text-zinc-500 dark:text-zinc-400">进度</span>
                        {hasRunning && <span className="h-1.5 w-1.5 rounded-full bg-blue-500" />}
                    </div>
                    <div className="flex items-center gap-2">
                        <span className="text-[10px] tabular-nums text-zinc-400 dark:text-zinc-500">
                            {progressPercent}%
                        </span>
                        <Pin className="h-3 w-3 text-zinc-400 dark:text-zinc-500" aria-hidden="true" />
                    </div>
                </div>

                <div
                    ref={scrollRef}
                    onScroll={handleProgressListScroll}
                    onPointerDown={handleProgressListUserIntent}
                    onMouseDown={handleProgressListUserIntent}
                    onWheel={handleProgressListUserIntent}
                    onTouchStart={handleProgressListUserIntent}
                    onTouchMove={handleProgressListUserIntent}
                    className="h-48 space-y-1 overflow-y-auto pr-1 scrollbar-thin scrollbar-thumb-zinc-300 scrollbar-track-transparent dark:scrollbar-thumb-zinc-700"
                    style={{
                        scrollbarGutter: 'stable both-edges',
                        overscrollBehavior: 'contain',
                        overflowAnchor: 'none',
                    }}
                >
                    {items.map((item) => (
                        <div key={item.id} className="flex min-w-0 items-start gap-1.5">
                            <div className="mt-0.5 flex h-3.5 w-3.5 flex-shrink-0 items-center justify-center">
                                <ProgressStatusIcon status={item.status} />
                            </div>
                            <div className="min-w-0 flex-1">
                                <div className="truncate text-xs leading-4 text-zinc-600 dark:text-zinc-300" title={item.title}>
                                    {item.title}
                                </div>
                                {item.detail && (
                                    <div className="truncate text-[10px] leading-3.5 text-zinc-400 dark:text-zinc-500" title={item.detail}>
                                        {item.detail}
                                    </div>
                                )}
                            </div>
                        </div>
                    ))}
                </div>
            </div>
        </>
    );
};

// Lazy-loaded code block with intersection observer
const LazyCodeBlock: React.FC<{
    language: string;
    children: React.ReactNode;
}> = ({ language, children }) => {
    const [isLoaded, setIsLoaded] = useState(false);
    const codeRef = useRef<HTMLDivElement>(null);
    const [isVisible, setIsVisible] = useState(false);

    // Lazy load syntax highlighting when code block becomes visible
    useEffect(() => {
        const observer = new IntersectionObserver(
            ([entry]) => {
                if (entry.isIntersecting) {
                    setIsVisible(true);
                    observer.disconnect();
                }
            },
            { rootMargin: '200px' }
        );

        if (codeRef.current) {
            observer.observe(codeRef.current);
        }

        return () => observer.disconnect();
    }, []);

    // Load syntax highlighter only when visible
    useEffect(() => {
        if (isVisible && !isLoaded) {
            setIsLoaded(true);
        }
    }, [isVisible, isLoaded]);

    const codeContent = String(children).replace(/\n$/, '');

    return (
        <div ref={codeRef} className="my-4 overflow-hidden rounded-md border border-zinc-200/50 dark:border-zinc-700/50">
            <div className="bg-[#1e1e1e] px-3 py-1.5 text-xs text-zinc-400 border-b border-zinc-700/50 flex justify-between items-center">
                <span>{language}</span>
            </div>
            {isLoaded ? (
                <SyntaxHighlighter
                    style={syntaxTheme}
                    language={language}
                    PreTag="div"
                    customStyle={{ margin: 0, borderRadius: 0, fontSize: '11px', lineHeight: '1.5', background: '#1e1e1e' }}
                >
                    {codeContent}
                </SyntaxHighlighter>
            ) : (
                <div className="p-4 bg-[#1e1e1e] text-zinc-400 font-mono text-xs overflow-x-auto">
                    <pre className="whitespace-pre-wrap">{codeContent}</pre>
                    {isVisible && (
                        <div className="mt-2 text-center text-zinc-500 text-xs">
                            Loading syntax highlighting...
                        </div>
                    )}
                </div>
            )}
        </div>
    );
};

const MessageBubbleComponent: React.FC<MessageBubbleProps> = ({ message, isThinking = false, onCancelTool, realtimeToolExecutions, onResume }) => {
    const isUser = message.role === 'user';
    const streamingParseStateRef = useRef<StreamingParseState>(createStreamingParseState());
    const parsedContent = React.useMemo(() => {
        if (message.role === 'assistant' && message.isStreaming) {
            const currentState = streamingParseStateRef.current;
            if (!message.content.startsWith(currentState.rawContent)) {
                const rebuiltState = createStreamingParseState();
                appendStreamingChunk(rebuiltState, message.content);
                streamingParseStateRef.current = rebuiltState;
                return materializeStreamingParse(rebuiltState);
            }

            const delta = message.content.slice(currentState.rawContent.length);
            appendStreamingChunk(currentState, delta);
            return materializeStreamingParse(currentState);
        }

        streamingParseStateRef.current = createStreamingParseState();
        return parseMessageContent(message.content, false);
    }, [message.content, message.isStreaming, message.role]);

    const { thinkingContents, mainContent, hasResumeAction, resumeLabel } = parsedContent;
    const hasRenderableMainContent = (content?: string) => isMeaningfulRenderableChatText(content);

    const deferredMainContent = mainContent;
    const deferredThinkingContents = thinkingContents;
    const attachments = message.attachments ?? [];
    const attachmentMetas = message.attachmentMetas ?? [];
    const hasToolExecutions = Boolean((realtimeToolExecutions && realtimeToolExecutions.length > 0) || (message.toolExecutions && message.toolExecutions.length > 0));
    const hasLegacyAttachments = attachments.length > 0 && attachmentMetas.length === 0;
    const hasAttachmentMetas = attachmentMetas.length > 0;
    const isToolPlaceholderMessage = Boolean(message.role === 'assistant' && message.isToolPlaceholder);
    const hasSupplementaryContent = hasToolExecutions || Boolean(message.progress) || hasLegacyAttachments || hasAttachmentMetas;
    const hasThinkingContent = deferredThinkingContents.length > 0;
    const shouldRenderMainCard = hasRenderableMainContent(deferredMainContent) || hasResumeAction;
    const shouldShowAssistantAvatar = message.role === 'system' && (!isToolPlaceholderMessage || hasThinkingContent || shouldRenderMainCard || hasToolExecutions || Boolean(message.progress) || hasLegacyAttachments || hasAttachmentMetas);

    const isCompressionMessage = message.content.includes("SYSTEM: 正在压缩上下文，请等待...") ||
                                 message.content.includes("Compressing context, please wait...");

    const renderAvatar = (key?: string) => {
        if (shouldShowAssistantAvatar) {
            return (
                <div key={key} className={cn(
                    "mr-2 mt-1 flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-full",
                    message.role === 'system' ? "bg-red-100 dark:bg-red-900/30 text-red-600 dark:text-red-400" : "bg-white dark:bg-zinc-800 border border-zinc-200 dark:border-zinc-700 text-blue-600 dark:text-blue-400"
                )}>
                    {message.role === 'system' ? <RefreshCw className="w-4 h-4" /> : <Bot className="w-5 h-5" />}
                </div>
            );
        }
        if (isUser) {
            return <div key={key} className="w-8 h-8 mr-2 flex-shrink-0 mt-1" />;
        }
        return null;
    };

    const renderAttachments = () => (
        <>
            {hasLegacyAttachments && (
                <div className="flex flex-wrap gap-2 mb-2">
                    {attachments.map((attachment, index) => (
                        <div key={index} className="relative h-48 w-48 overflow-hidden rounded-lg border border-zinc-200 dark:border-zinc-800">
                            <img
                                src={attachment}
                                alt={`Attachment ${index + 1}`}
                                className={cn("w-full h-full object-cover cursor-pointer", !USE_LIGHTWEIGHT_CHAT_CARD_MOTION && "hover:scale-105 transition-transform duration-300")}
                                onClick={() => window.open(attachment, '_blank')}
                            />
                        </div>
                    ))}
                </div>
            )}
            {hasAttachmentMetas && (
                <div className={cn("flex flex-wrap gap-2 mb-2", isUser && "justify-end")}>
                    {attachmentMetas.map((meta, index) => {
                        const isImage = meta.type?.startsWith('image/') || (meta.name && /\.(jpg|jpeg|png|gif|webp)$/i.test(meta.name));
                        if (isImage && meta.url) {
                            return (
                                <div key={index} className="group/img relative h-48 w-48 overflow-hidden rounded-lg border border-zinc-200 dark:border-zinc-800">
                                    <img
                                        src={meta.url}
                                        alt={meta.name}
                                        className={cn("w-full h-full object-cover cursor-pointer", !USE_LIGHTWEIGHT_CHAT_CARD_MOTION && "hover:scale-105 transition-transform duration-300")}
                                        onClick={() => window.open(meta.url, '_blank')}
                                    />
                                    <div className={cn("absolute bottom-0 left-0 right-0 bg-black/80 text-white text-[10px] px-2 py-1 truncate opacity-0 group-hover/img:opacity-100", !USE_LIGHTWEIGHT_CHAT_CARD_MOTION && "transition-opacity")}>
                                        {meta.name}
                                    </div>
                                </div>
                            );
                        }
                        return (
                            <AttachmentFileCard key={index} meta={meta} />
                        );
                    })}
                </div>
            )}
        </>
    );

    const renderResponseMeta = () => {
        const responseTime = formatResponseTime(message.responseTimeMs);
        if (message.role !== 'assistant' || !responseTime) return null;

        return (
            <div className="mt-1 flex items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400" aria-label={`已完成，用时 ${responseTime}`}>
                <Logo className="h-5 w-5 rounded-md" />
                <span>已完成 {responseTime}</span>
            </div>
        );
    };

    const renderMainCardContent = (options?: {
        mainContent?: string;
        hasResumeAction?: boolean;
        resumeLabel?: string;
        isStreamingContent?: boolean;
    }) => {
        const mainContent = options?.mainContent ?? deferredMainContent;
        const fragmentHasResumeAction = options?.hasResumeAction ?? hasResumeAction;
        const fragmentResumeLabel = options?.resumeLabel ?? resumeLabel;
        const isStreamingContent = options?.isStreamingContent ?? Boolean(message.role === 'assistant' && message.isStreaming);
        const hasVisibleMainContent = hasRenderableMainContent(mainContent);

        if (!hasVisibleMainContent && !fragmentHasResumeAction) return null;

        return (
            <ChatCard
                fullWidth={!isUser}
                className={cn(
                    "relative bg-white dark:bg-zinc-950 border-zinc-200 dark:border-zinc-800",
                    isUser && "text-left self-end"
                )}
                contentClassName="px-4 py-3"
            >
                <div className={cn(
                    "markdown-body overflow-x-auto text-xs leading-normal text-zinc-900 dark:text-zinc-100",
                    isUser && "text-left"
                )}>
                    <MessageMarkdown
                        content={hasVisibleMainContent ? mainContent : ''}
                        isStreaming={Boolean(message.role === 'assistant' && isStreamingContent)}
                    />
                    {fragmentHasResumeAction && (
                        <div className="mt-4 flex justify-start">
                            <button
                                onClick={onResume}
                                className="flex items-center gap-2 rounded bg-blue-600 px-4 py-2 text-xs font-medium text-white"
                            >
                                <Play className="w-3.5 h-3.5" />
                                {fragmentResumeLabel}
                            </button>
                        </div>
                    )}
                </div>
            </ChatCard>
        );
    };

    const renderMainCard = () => renderMainCardContent();

    const getToolStableKey = (tool: ToolExecution) => {
        const toolCallId = tool.id?.trim();
        if (toolCallId) {
            return `tool-call:${toolCallId}`;
        }

        const executionId = tool.executionId?.trim();
        if (executionId) {
            return `execution:${executionId}`;
        }

        return `tool-name:${tool.name}`;
    };

    const toolExecs = useMemo(() => {
        const executions = [...(message.toolExecutions || []), ...(realtimeToolExecutions || [])];
        const merged: ToolExecution[] = [];
        const keyToIndex = new Map<string, number>();
        const statusRank: Record<ToolExecution['status'], number> = {
            running: 0,
            completed: 1,
            failed: 2,
        };

        const mergeText = (left?: string, right?: string): string => {
            const leftText = left ?? '';
            const rightText = right ?? '';
            if (rightText.trim().length === 0) return leftText;
            if (leftText.trim().length === 0) return rightText;
            return rightText.length >= leftText.length ? rightText : leftText;
        };

        for (const tool of executions) {
            const stableKey = getToolStableKey(tool);
            let existingIndex = keyToIndex.get(stableKey);

            if (existingIndex === undefined && tool.executionId) {
                existingIndex = merged.findIndex((item) => !item.executionId && item.name === tool.name && item.status === 'running');
            }

            if (existingIndex === undefined || existingIndex === -1) {
                const index = merged.length;
                merged.push(tool);
                keyToIndex.set(stableKey, index);
                keyToIndex.set(`tool-call:${tool.id}`, index);
                if (tool.executionId) {
                    keyToIndex.set(`execution:${tool.executionId}`, index);
                }
                continue;
            }

            const current = merged[existingIndex];
            const nextStatus = statusRank[tool.status] >= statusRank[current.status] ? tool.status : current.status;
            const next: ToolExecution = {
                ...current,
                ...tool,
                id: current.id || tool.id,
                executionId: current.executionId || tool.executionId,
                name: tool.name || current.name,
                input: mergeText(current.input, tool.input),
                output: mergeText(current.output, tool.output),
                status: nextStatus,
                isCodeStream: Boolean(current.isCodeStream || tool.isCodeStream),
                isFinalizing: nextStatus === 'running' && Boolean(current.isFinalizing || tool.isFinalizing),
                metadata: tool.metadata ?? current.metadata,
            };
            merged[existingIndex] = next;

            keyToIndex.set(getToolStableKey(next), existingIndex);
            keyToIndex.set(`tool-call:${next.id}`, existingIndex);
            if (next.executionId) {
                keyToIndex.set(`execution:${next.executionId}`, existingIndex);
            }
        }

        return merged;
    }, [message.toolExecutions, realtimeToolExecutions]);

    const regularToolExecs = useMemo(() => toolExecs.filter((tool) => !isPlanProgressTool(tool)), [toolExecs]);

    const renderProgressBlock = (key = 'progress') => {
        if (!message.progress) return null;

        return (
            <div key={key} className="flex w-full group mb-2">
                {renderAvatar(`${key}-avatar`)}
                <div className="flex flex-col flex-1 min-w-0 w-full gap-2">
                    <ProgressBar progress={message.progress} />
                </div>
            </div>
        );
    };

    if (!hasThinkingContent && !shouldRenderMainCard && !hasSupplementaryContent) {
        return null;
    }

    if (isCompressionMessage) {
        return (
            <div className={cn("flex w-full mb-6 justify-center")}>
                <div className="flex items-center gap-3 rounded-full border border-blue-100 bg-blue-50 px-4 py-3 dark:border-blue-900 dark:bg-blue-950">
                    <RefreshCw className="w-4 h-4 text-blue-500" />
                    <span className="text-sm font-medium text-blue-600 dark:text-blue-400">
                        正在优化记忆空间，请稍候...
                    </span>
                </div>
            </div>
        );
    }

    const buildMainFragment = (rawContent: string, isStreamingContent: boolean) => {
        const parsed = parseMessageContent(rawContent, isStreamingContent);
        if (!hasRenderableMainContent(parsed.mainContent) && !parsed.hasResumeAction) {
            return null;
        }

        return {
            mainContent: parsed.mainContent,
            hasResumeAction: parsed.hasResumeAction,
            resumeLabel: parsed.resumeLabel,
        };
    };

    // 按 eventTimeline 顺序渲染 thinking、正文片段和 tool
    const eventTimeline = message.eventTimeline;
    const hasTimeline = Boolean(eventTimeline && eventTimeline.length > 0);

    if (hasTimeline) {
        const items: React.ReactNode[] = [];
        const responseMeta = renderResponseMeta();
        if (responseMeta) items.push(<div key="response-meta" className="mb-1">{responseMeta}</div>);
        const matchedRegularToolKeys = new Set<string>();
        let rawContentCursor = 0;
        let hasRenderedMainFragment = false;

        const pushMainFragmentBefore = (rawEnd: number, key: string, isLastItem: boolean) => {
            const safeEnd = Math.max(rawContentCursor, Math.min(rawEnd, message.content.length));
            if (safeEnd <= rawContentCursor) {
                return;
            }

            const fragment = buildMainFragment(message.content.slice(rawContentCursor, safeEnd), false);
            rawContentCursor = safeEnd;
            if (!fragment) {
                return;
            }

            items.push(
                <div key={key} className={cn("flex w-full group", !isLastItem && "mb-2")}>
                    {renderAvatar(`${key}-avatar`)}
                    <div className="flex flex-col flex-1 min-w-0 w-full gap-2">
                        {renderMainCardContent({
                            mainContent: fragment.mainContent,
                            hasResumeAction: fragment.hasResumeAction,
                            resumeLabel: fragment.resumeLabel,
                            isStreamingContent: false,
                        })}
                    </div>
                </div>
            );
            hasRenderedMainFragment = true;
        };
        
        eventTimeline!.forEach((event, eventIndex) => {
            const isLastEvent = eventIndex === eventTimeline!.length - 1;
            const isLastItem = isLastEvent && !shouldRenderMainCard;

            if (event.type === 'thinking') {
                const thinking = deferredThinkingContents[event.index];
                if (thinking) {
                    items.push(
                        <div key={`thinking-${event.index}`} className={cn("flex w-full group", !isLastItem && "mb-2")}>
                            {renderAvatar(`thinking-avatar-${event.index}`)}
                            <div className="flex flex-col flex-1 min-w-0 w-full gap-2">
                                <ThinkingSection content={thinking} isThinking={isThinking && event.index === deferredThinkingContents.length - 1} />
                            </div>
                        </div>
                    );
                }
            } else if (event.type === 'tool') {
                if (typeof event.contentMark === 'number') {
                    pushMainFragmentBefore(event.contentMark, `main-fragment-before-tool-${eventIndex}`, false);
                }

                const toolIndex = regularToolExecs.findIndex(t => t.executionId === event.executionId || t.id === event.executionId);
                if (toolIndex !== -1) {
                    const tool = regularToolExecs[toolIndex];
                    const toolStableKey = getToolStableKey(tool);
                    if (matchedRegularToolKeys.has(toolStableKey)) {
                        return;
                    }

                    matchedRegularToolKeys.add(toolStableKey);
                    items.push(
                        <div key={`tool-${toolStableKey}`} className={cn("flex w-full group", !isLastItem && "mb-2")}>
                            {renderAvatar(`tool-avatar-${eventIndex}`)}
                            <div className="flex flex-col flex-1 min-w-0 w-full gap-2">
                                <ToolExecutionView execution={tool} isParentActive={isThinking} onCancel={onCancelTool} />
                            </div>
                        </div>
                    );
                }
            } else if (event.type === 'progress' && typeof event.contentMark === 'number') {
                pushMainFragmentBefore(event.contentMark, `main-fragment-before-progress-${eventIndex}`, false);
            }
        });

        const trailingMainFragment = buildMainFragment(
            message.content.slice(rawContentCursor),
            Boolean(message.role === 'assistant' && message.isStreaming)
        );
        const hasTrailingMainFragment = Boolean(trailingMainFragment);

        if (hasTrailingMainFragment) {
            items.push(
                <div key="main-fragment-trailing" className={cn("flex w-full group", !shouldRenderMainCard && "mb-2")}>
                    {renderAvatar('main-fragment-trailing-avatar')}
                    <div className="flex flex-col flex-1 min-w-0 w-full gap-2">
                        {renderMainCardContent({
                            mainContent: trailingMainFragment!.mainContent,
                            hasResumeAction: trailingMainFragment!.hasResumeAction,
                            resumeLabel: trailingMainFragment!.resumeLabel,
                            isStreamingContent: Boolean(message.role === 'assistant' && message.isStreaming),
                        })}
                    </div>
                </div>
            );
            hasRenderedMainFragment = true;
        }

        regularToolExecs.forEach((tool, toolIndex) => {
            const toolStableKey = getToolStableKey(tool);
            if (!matchedRegularToolKeys.has(toolStableKey)) {
                items.push(
                    <div key={`tool-extra-${toolStableKey}`} className={cn("flex w-full group", !shouldRenderMainCard && "mb-2")}>
                        {renderAvatar(`tool-avatar-extra-${toolIndex}`)}
                        <div className="flex flex-col flex-1 min-w-0 w-full gap-2">
                            <ToolExecutionView execution={tool} isParentActive={isThinking} onCancel={onCancelTool} />
                        </div>
                    </div>
                );
            }
        });

        return (
            <>
                {items}
                {renderProgressBlock()}
                {renderAttachments()}
                {shouldRenderMainCard && !hasRenderedMainFragment && (
                    <div className="flex w-full group">
                        {renderAvatar('main-avatar')}
                        <div className="flex flex-col flex-1 min-w-0 w-full gap-2">
                            {renderMainCard()}
                        </div>
                    </div>
                )}
            </>
        );
    }

    // 兼容模式：没有 eventTimeline 时，按索引配对渲染
    const maxIndex = Math.max(deferredThinkingContents.length, regularToolExecs.length);
    if (hasThinkingContent || hasToolExecutions) {
        const items: React.ReactNode[] = [];
        const responseMeta = renderResponseMeta();
        if (responseMeta) items.push(<div key="response-meta" className="mb-1">{responseMeta}</div>);

        for (let i = 0; i < maxIndex; i++) {
            const thinking = deferredThinkingContents[i];
            const tool = regularToolExecs[i];
            const isLastItem = i === maxIndex - 1 && !shouldRenderMainCard;

            if (thinking || tool) {
                items.push(
                    <div key={`round-${i}`} className={cn("flex w-full group", !isLastItem && "mb-2")}>
                        {renderAvatar(`round-avatar-${i}`)}
                        <div className="flex flex-col flex-1 min-w-0 w-full gap-2">
                            {thinking && (
                                <ThinkingSection content={thinking} isThinking={isThinking && i === deferredThinkingContents.length - 1} />
                            )}
                            {tool && (
                                <ToolExecutionView execution={tool} isParentActive={isThinking} onCancel={onCancelTool} />
                            )}
                        </div>
                    </div>
                );
            }
        }

        return (
            <>
                {items}
                {renderProgressBlock()}
                {renderAttachments()}
                {shouldRenderMainCard && (
                    <div className="flex w-full group">
                        {renderAvatar('main-avatar')}
                        <div className="flex flex-col flex-1 min-w-0 w-full gap-2">
                            {renderMainCard()}
                        </div>
                    </div>
                )}
            </>
        );
    }

    return (
        <div className="flex w-full group">
            {renderAvatar()}
            <div className={cn("flex flex-col flex-1 min-w-0 w-full", !isUser && "gap-2")}>
                {renderResponseMeta()}
                {regularToolExecs.map((tool) => (
                    <ToolExecutionView key={getToolStableKey(tool)} execution={tool} isParentActive={isThinking} onCancel={onCancelTool} />
                ))}
                {message.progress && <ProgressBar progress={message.progress} />}
                {renderAttachments()}
                {renderMainCard()}
            </div>
        </div>
    );
};

const areStringArraysEqual = (prev?: string[], next?: string[]) => {
    if (prev === next) {
        return true;
    }
    if (!prev || !next) {
        return !prev && !next;
    }
    if (prev.length !== next.length) {
        return false;
    }

    for (let index = 0; index < prev.length; index += 1) {
        if (prev[index] !== next[index]) {
            return false;
        }
    }

    return true;
};

const areAttachmentMetasEqual = (prev?: AttachmentMeta[], next?: AttachmentMeta[]) => {
    if (prev === next) {
        return true;
    }
    if (!prev || !next) {
        return !prev && !next;
    }
    if (prev.length !== next.length) {
        return false;
    }

    for (let index = 0; index < prev.length; index += 1) {
        const prevMeta = prev[index];
        const nextMeta = next[index];
        if (
            prevMeta.name !== nextMeta.name
            || prevMeta.url !== nextMeta.url
            || prevMeta.type !== nextMeta.type
        ) {
            return false;
        }
    }

    return true;
};

const areToolExecutionsEqual = (prev?: ToolExecution[], next?: ToolExecution[]) => {
    if (prev === next) {
        return true;
    }
    if (!prev || !next) {
        return !prev && !next;
    }
    if (prev.length !== next.length) {
        return false;
    }

    for (let index = 0; index < prev.length; index += 1) {
        const prevExecution = prev[index];
        const nextExecution = next[index];
        if (
            prevExecution.id !== nextExecution.id
            || prevExecution.executionId !== nextExecution.executionId
            || prevExecution.name !== nextExecution.name
            || prevExecution.input !== nextExecution.input
            || prevExecution.output !== nextExecution.output
            || prevExecution.status !== nextExecution.status
            || prevExecution.isCodeStream !== nextExecution.isCodeStream
            || prevExecution.isFinalizing !== nextExecution.isFinalizing
            || prevExecution.metadata !== nextExecution.metadata
        ) {
            return false;
        }
    }

    return true;
};

const areEventTimelinesEqual = (prev?: TimelineEvent[], next?: TimelineEvent[]) => {
    if (prev === next) {
        return true;
    }
    if (!prev || !next) {
        return !prev && !next;
    }
    if (prev.length !== next.length) {
        return false;
    }

    for (let index = 0; index < prev.length; index += 1) {
        const prevEvent = prev[index];
        const nextEvent = next[index];
        if (prevEvent.type !== nextEvent.type) {
            return false;
        }
        if (prevEvent.type === 'thinking' && nextEvent.type === 'thinking') {
            if (prevEvent.index !== nextEvent.index) {
                return false;
            }
        }
        if (prevEvent.type === 'tool' && nextEvent.type === 'tool') {
            if (prevEvent.executionId !== nextEvent.executionId || prevEvent.contentMark !== nextEvent.contentMark) {
                return false;
            }
        }
        if (prevEvent.type === 'progress' && nextEvent.type === 'progress') {
            if (prevEvent.contentMark !== nextEvent.contentMark) {
                return false;
            }
        }
    }

    return true;
};

const areMessagesEqual = (prev: Message, next: Message) => {
    return prev === next || (
        prev.id === next.id
        && prev.role === next.role
        && prev.content === next.content
        && prev.timestamp === next.timestamp
        && prev.responseStartedAt === next.responseStartedAt
        && prev.responseTimeMs === next.responseTimeMs
        && prev.isStreaming === next.isStreaming
        && prev.isToolPlaceholder === next.isToolPlaceholder
        && prev.progress?.percentage === next.progress?.percentage
        && prev.progress?.details === next.progress?.details
        && areToolExecutionsEqual(prev.toolExecutions, next.toolExecutions)
        && areStringArraysEqual(prev.attachments, next.attachments)
        && areAttachmentMetasEqual(prev.attachmentMetas, next.attachmentMetas)
        && areEventTimelinesEqual(prev.eventTimeline, next.eventTimeline)
    );
};

const areMessageBubblePropsEqual = (prev: MessageBubbleProps, next: MessageBubbleProps) => {
    return prev.isThinking === next.isThinking
        && prev.onCancelTool === next.onCancelTool
        && prev.onResume === next.onResume
        && areMessagesEqual(prev.message, next.message)
        && areToolExecutionsEqual(prev.realtimeToolExecutions, next.realtimeToolExecutions);
};

export const MessageBubble = React.memo(MessageBubbleComponent, areMessageBubblePropsEqual);

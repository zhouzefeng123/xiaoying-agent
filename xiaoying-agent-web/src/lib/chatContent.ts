const chatStopMarkers = [
    '[TURN CONTEXT]',
    '[MEMORY CAPSULE]',
    '[WORKING STATE]',
    '[CONTEXT INFO]',
    '[SYSTEM INFO]',
    '[System Summary]:',
    '[WORD_CONTEXT]',
    'User has attached the following files:',
    'User has attached the following image files:',
    'User has attached the following non-image files:',
    '请使用 read 等工具的 file_path 参数读取这些文件进行处理。',
    '对非图片附件，可使用 read 等工具的 file_path 参数读取并处理。',
    '对图片附件，优先直接使用 visual-analysis 工具，使用对应的 image_path。不要使用 read 读取图片二进制内容。',
    'Environment:',
    '<env>'
];

const chatControlLinePattern = /(SYSTEM WARNING:|SYSTEM MONITOR:|SYSTEM HINT:|SYSTEM:|STOP REPEATING|Action Required:|THIS IS A FAILURE|RETRY IMMEDIATELY|check_command_status|you have completed your thinking process|output the actual RESPONSE or TOOL CALL|you did NOT emit any tool call|you MUST use a tool|If you are waiting for something|If you are stuck, ask the user|OS:|Shell:|CWD:|ProjectRoot:|User:|Time:|Timezone:|Loc:|Installed Tools:|\[TURN CONTEXT\]|\[MEMORY CAPSULE\]|\[WORKING STATE\]|\[CONTEXT INFO\]|\[SYSTEM INFO\]|\[System Summary\]:|\[WORD_CONTEXT\]|\[\/WORD_CONTEXT\]|User has attached the following files:|User has attached the following image files:|User has attached the following non-image files:|请使用 read 等工具的 file_path 参数读取这些文件进行处理。|对非图片附件，可使用 read 等工具的 file_path 参数读取并处理。|对图片附件，优先直接使用 visual-analysis 工具，使用对应的 image_path。不要使用 read 读取图片二进制内容。|<env>|Environment:)/i;

const standaloneStructuralPatterns = [
    /^\d+[.)]\s*$/u,
    /^(?:[-*+]|>+|[|:]+|#+)\s*$/u,
];

const punctuationOrSymbolsOnlyPattern = /^[\p{P}\p{S}\s]+$/u;
const zeroWidthPattern = /[\u200B-\u200D\uFEFF]/g;
const htmlCommentPattern = /<!--[\s\S]*?-->/g;
const htmlBlockTagPattern = /<\/?(?:p|div|section|article|aside|header|footer|details|summary|ul|ol|li|blockquote|pre|table|thead|tbody|tfoot|tr|td|th|h[1-6])\b[^>]*>/gi;
const htmlLineBreakPattern = /<(?:br|hr)\s*\/?>/gi;
const htmlTagPattern = /<\/?[A-Za-z][A-Za-z0-9:-]*(?:\s[^<>]*)?>/g;
const htmlNamedEntityPattern = /&(nbsp|amp|lt|gt|quot|apos|#39|#x27);/gi;
const htmlNumericEntityPattern = /&#(?:x([0-9a-f]+)|(\d+));/gi;

const thinkingPatterns = [
    /(?:<|&lt;)(?:thinking|think|tinking)\b(?:>|&gt;)([\s\S]*?)(?:(?:<\/|&lt;\/)(?:thinking|think|tinking)(?:>|&gt;)|$)/i,
    /(?:<|&lt;)details\b(?:>|&gt;)\s*(?:<|&lt;)summary\b(?:>|&gt;)\s*Thinking Process\s*(?:<\/|&lt;\/)summary(?:>|&gt;)([\s\S]*?)(?:(?:<\/|&lt;\/)details(?:>|&gt;)|$)/i,
];

const htmlNamedEntityMap: Record<string, string> = {
    nbsp: ' ',
    amp: '&',
    lt: '<',
    gt: '>',
    quot: '"',
    apos: "'",
    '#39': "'",
    '#x27': "'",
};

function decodeBasicHtmlEntities(input: string): string {
    return input
        .replace(htmlNamedEntityPattern, (_, entity: string) => htmlNamedEntityMap[entity.toLowerCase()] ?? _)
        .replace(htmlNumericEntityPattern, (_, hex: string | undefined, decimal: string | undefined) => {
            const codePoint = hex ? Number.parseInt(hex, 16) : Number.parseInt(decimal ?? '', 10);
            if (!Number.isFinite(codePoint)) {
                return _;
            }

            try {
                return String.fromCodePoint(codePoint);
            } catch {
                return _;
            }
        });
}

export function stripInjectedChatControlContent(input: string, trimEdges = true): string {
    if (!input) {
        return '';
    }

    let normalized = input
        .replace(/Environment:\s*<env>[\s\S]*?<\/env>/gi, '')
        .replace(/<env>[\s\S]*?<\/env>/gi, '')
        .replace(/\[WORD_CONTEXT\][\s\S]*?\[\/WORD_CONTEXT\]/gi, '');

    let stopIndex = -1;
    chatStopMarkers.forEach((marker) => {
        const index = normalized.indexOf(marker);
        if (index !== -1 && (stopIndex === -1 || index < stopIndex)) {
            stopIndex = index;
        }
    });
    if (stopIndex !== -1) {
        normalized = normalized.slice(0, stopIndex);
    }

    normalized = normalized
        .split('\n')
        .filter((line) => !chatControlLinePattern.test(line))
        .join('\n')
        .replace(/\n{3,}/g, '\n\n');

    return trimEdges ? normalized.trim() : normalized;
}

export function extractRenderableChatText(input?: string | null): string {
    if (!input) {
        return '';
    }

    let normalized = stripInjectedChatControlContent(input, false)
        .replace(zeroWidthPattern, '')
        .replace(/\u00a0/g, ' ');

    normalized = decodeBasicHtmlEntities(normalized)
        .replace(htmlCommentPattern, ' ')
        .replace(htmlLineBreakPattern, '\n')
        .replace(htmlBlockTagPattern, '\n')
        .replace(htmlTagPattern, ' ')
        .replace(/\n{3,}/g, '\n\n')
        .replace(/[ \t]{2,}/g, ' ')
        .trim();

    return normalized;
}

export function isMeaningfulRenderableChatText(input?: string | null): boolean {
    const normalized = extractRenderableChatText(input);
    if (!normalized) {
        return false;
    }

    if (standaloneStructuralPatterns.some((pattern) => pattern.test(normalized))) {
        return false;
    }

    if (punctuationOrSymbolsOnlyPattern.test(normalized)) {
        return false;
    }

    return true;
}

export function extractRenderableThinkingBlocks(contentInput: string): string[] {
    if (!contentInput) {
        return [];
    }

    const blocks: string[] = [];
    let cursor = 0;

    while (cursor < contentInput.length) {
        const source = contentInput.slice(cursor);
        let bestMatch: { index: number; length: number; content: string } | null = null;

        for (const pattern of thinkingPatterns) {
            const match = pattern.exec(source);
            if (!match || match.index === undefined) {
                continue;
            }

            const cleaned = stripInjectedChatControlContent(match[1] ?? '');
            const candidate = {
                index: cursor + match.index,
                length: match[0].length,
                content: cleaned,
            };

            if (!bestMatch || candidate.index < bestMatch.index) {
                bestMatch = candidate;
            }
        }

        if (!bestMatch) {
            break;
        }

        if (isMeaningfulRenderableChatText(bestMatch.content)) {
            blocks.push(bestMatch.content);
        }

        cursor = bestMatch.index + bestMatch.length;
    }

    return blocks;
}

export function countRenderableThinkingBlocks(contentInput: string): number {
    return extractRenderableThinkingBlocks(contentInput).length;
}
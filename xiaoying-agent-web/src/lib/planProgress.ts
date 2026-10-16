export type PlanProgressStatus = 'pending' | 'running' | 'completed' | 'failed';

export interface PlanProgressItem {
    id: string;
    title: string;
    detail?: string;
    status: PlanProgressStatus;
}

export interface PlanProgressSnapshot {
    baseName?: string;
    fileName?: string;
    action?: string;
    revision?: number;
    items: PlanProgressItem[];
    updatedAt: number;
    error?: string;
}

export interface PlanProgressExecution {
    id: string;
    executionId?: string;
    name: string;
    input: string;
    output: string;
    status: 'running' | 'completed' | 'failed';
    metadata?: unknown;
}

const parseObject = (value: unknown): Record<string, unknown> | null => {
    if (!value) return null;
    if (typeof value === 'string') {
        try {
            const parsed = JSON.parse(value);
            return parsed && typeof parsed === 'object' && !Array.isArray(parsed)
                ? parsed as Record<string, unknown>
                : null;
        } catch {
            return null;
        }
    }
    return typeof value === 'object' && !Array.isArray(value)
        ? value as Record<string, unknown>
        : null;
};

const readString = (obj: Record<string, unknown> | null, fields: string[]): string => {
    if (!obj) return '';
    for (const field of fields) {
        const value = obj[field];
        if (typeof value === 'string' && value.trim()) {
            return value.trim();
        }
    }
    return '';
};

const readNumber = (obj: Record<string, unknown> | null, fields: string[]): number | undefined => {
    if (!obj) return undefined;
    for (const field of fields) {
        const value = obj[field];
        if (typeof value === 'number' && Number.isFinite(value)) {
            return value;
        }
    }
    return undefined;
};

const normalizeStatus = (status: unknown, fallback: PlanProgressStatus): PlanProgressStatus => {
    if (status === 'completed' || status === 'done' || status === 'success') return 'completed';
    if (status === 'in_progress' || status === 'running' || status === 'active') return 'running';
    if (status === 'failed' || status === 'error') return 'failed';
    if (status === 'pending' || status === 'todo') return 'pending';
    return fallback;
};

const normalizeKey = (value: string): string => value.trim().toLowerCase();

const arePlanProgressItemsEqual = (left: PlanProgressItem[], right: PlanProgressItem[]): boolean => {
    if (left === right) return true;
    if (left.length !== right.length) return false;

    for (let index = 0; index < left.length; index += 1) {
        const leftItem = left[index];
        const rightItem = right[index];
        if (
            leftItem.id !== rightItem.id
            || leftItem.title !== rightItem.title
            || leftItem.detail !== rightItem.detail
            || leftItem.status !== rightItem.status
        ) {
            return false;
        }
    }

    return true;
};

const findItemIndex = (
    items: PlanProgressItem[],
    target: { id?: string; index?: number; title?: string },
): number => {
    const id = target.id ? normalizeKey(target.id) : '';
    if (id) {
        const byId = items.findIndex((item) => {
            const itemId = normalizeKey(item.id);
            return itemId === id || itemId.startsWith(id) || id.startsWith(itemId);
        });
        if (byId !== -1) return byId;
    }

    if (target.index && target.index > 0 && target.index <= items.length) {
        return target.index - 1;
    }

    const title = target.title ? normalizeKey(target.title) : '';
    if (title) {
        return items.findIndex((item) => normalizeKey(item.title) === title);
    }

    return -1;
};

const upsertItem = (items: PlanProgressItem[], item: PlanProgressItem) => {
    const existingIndex = findItemIndex(items, { id: item.id, title: item.title });
    if (existingIndex >= 0) {
        items[existingIndex] = {
            ...items[existingIndex],
            ...item,
            id: item.id || items[existingIndex].id,
            title: item.title || items[existingIndex].title,
        };
        return;
    }
    items.push(item);
};

const itemFromSnapshotRaw = (raw: unknown, fallbackId: string, fallbackStatus: PlanProgressStatus): PlanProgressItem | null => {
    const obj = parseObject(raw);
    if (!obj) return null;

    const title = readString(obj, ['title', 'item', 'task', 'content', 'text']);
    if (!title) return null;

    return {
        id: readString(obj, ['id', 'display_id']) || fallbackId,
        title,
        status: normalizeStatus(obj.status, fallbackStatus),
    };
};

const cleanChecklistTitle = (value: string): string => {
    return value
        .replace(/\s+\[[^\]]+\]\s*$/g, '')
        .replace(/\s+\[(?:High|Medium|Low|high|medium|low)\]/g, '')
        .replace(/\s+due_date:\s*\S+/gi, '')
        .replace(/\s+depends_on_ids:\s*\[[^\]]*\]/gi, '')
        .replace(/\s+#[^\s]+/g, '')
        .trim();
};

const extractChecklistItems = (execution: PlanProgressExecution): PlanProgressItem[] => {
    if (!execution.output) return [];

    const items: PlanProgressItem[] = [];
    execution.output.split(/\r?\n/).forEach((line, index) => {
        const match = line.match(/-\s+\[([ xX-])\]\s+(.+)$/);
        if (!match) return;

        const title = cleanChecklistTitle(match[2]);
        if (!title) return;

        const mark = match[1];
        items.push({
            id: line.match(/\[ID:\s*([^\]]+)\]/i)?.[1]?.trim() || `${execution.executionId || execution.id}-plan-${index}`,
            title,
            status: mark.toLowerCase() === 'x' ? 'completed' : mark === '-' ? 'running' : 'pending',
        });
    });
    return items;
};

const extractInputListItems = (execution: PlanProgressExecution): PlanProgressItem[] => {
    const input = parseObject(execution.input);
    const list = input?.todos ?? input?.todo_list ?? input?.items ?? input?.tasks;
    if (!Array.isArray(list)) return [];

    return list
        .map((entry, index): PlanProgressItem | null => {
            if (typeof entry === 'string') {
                const title = entry.trim();
                return title
                    ? {
                        id: `${execution.executionId || execution.id}-todo-${index}`,
                        title,
                        status: execution.status === 'failed' ? 'failed' : 'pending',
                    }
                    : null;
            }

            const obj = parseObject(entry);
            if (!obj) return null;
            const title = readString(obj, ['title', 'item', 'task', 'content', 'text']);
            if (!title) return null;

            return {
                id: readString(obj, ['id']) || `${execution.executionId || execution.id}-todo-${index}`,
                title,
                status: normalizeStatus(obj.status, execution.status === 'failed' ? 'failed' : 'pending'),
            };
        })
        .filter((item): item is PlanProgressItem => Boolean(item));
};

const extractOutputPlanId = (output: string): string => {
    return output.match(/\bID:\s*([A-Za-z0-9_-]+)/i)?.[1]?.trim() || '';
};

const extractCompletedTitle = (output: string): string => {
    const line = output
        .split(/\r?\n/)
        .map((item) => item.trim())
        .find((item) => /^已完成[:：]|^completed[:：]/i.test(item));
    return line?.replace(/^已完成[:：]\s*/i, '').replace(/^completed[:：]\s*/i, '').trim() || '';
};

const fallbackHeadline = (output: string): string => {
    return output
        .split(/\r?\n/)
        .map((line) => line.trim())
        .find(Boolean)
        ?.replace(/\s+/g, ' ')
        .slice(0, 88) || '请展开历史记录查看详情';
};

const applyHeuristicExecution = (items: PlanProgressItem[], execution: PlanProgressExecution) => {
    const input = parseObject(execution.input);
    const action = readString(input, ['action']).toLowerCase();
    const title = readString(input, ['item', 'title', 'task', 'content', 'text']) || extractCompletedTitle(execution.output);
    const id = readString(input, ['id']) || (action === 'add' ? extractOutputPlanId(execution.output) : '');
    const index = readNumber(input, ['index']);
    const requestedStatus = readString(input, ['status']);
    const checklistItems = extractChecklistItems(execution);

    if (execution.status === 'failed' && checklistItems.length === 0) {
        upsertItem(items, {
            id: `${execution.executionId || execution.id}-error`,
            title: `计划更新失败：${fallbackHeadline(execution.output)}`,
            status: 'failed',
        });
        return;
    }

    if (execution.name === 'manage_todo_list') {
        const todoItems = extractInputListItems(execution);
        if (todoItems.length > 0) {
            items.splice(0, items.length, ...todoItems);
            return;
        }
    }

    if (checklistItems.length > 0) {
        const isFullList = action === 'list' && (!requestedStatus || requestedStatus === 'all');
        if (isFullList || items.length === 0) {
            items.splice(0, items.length, ...checklistItems);
        } else {
            checklistItems.forEach((item) => upsertItem(items, item));
        }
        return;
    }

    if (!title && !id && !index) return;

    const targetIndex = findItemIndex(items, { id, index, title });
    const status = action === 'complete' || execution.name === 'task_complete' || requestedStatus === 'completed'
        ? execution.status === 'running' ? 'running' : execution.status === 'failed' ? 'failed' : 'completed'
        : action === 'update'
            ? normalizeStatus(requestedStatus, execution.status === 'running' ? 'running' : 'pending')
            : execution.status === 'running'
                ? 'running'
                : execution.status === 'failed'
                    ? 'failed'
                    : 'pending';

    if (targetIndex >= 0) {
        items[targetIndex] = {
            ...items[targetIndex],
            title: title || items[targetIndex].title,
            status,
        };
        return;
    }

    if (title) {
        upsertItem(items, {
            id: id || `${execution.executionId || execution.id}-${action || execution.name}`,
            title,
            status,
        });
    }
};

export const isPlanProgressTool = (execution: Pick<PlanProgressExecution, 'name'>): boolean => {
    return execution.name === 'manage_plan'
        || execution.name === 'manage_todo_list'
        || execution.name === 'task_complete';
};

export const applyPlanProgressExecution = (
    previous: PlanProgressSnapshot | null,
    execution: PlanProgressExecution,
): PlanProgressSnapshot | null => {
    if (!isPlanProgressTool(execution)) {
        return previous;
    }

    const metadata = parseObject(execution.metadata);
    if (metadata?.type === 'plan_snapshot') {
        const baseName = readString(metadata, ['base_name', 'baseName']);
        const fileName = readString(metadata, ['file_name', 'fileName']);
        const action = readString(metadata, ['action']);
        const error = readString(metadata, ['error']);
        const revision = readNumber(metadata, ['revision']);
        if (
            previous?.revision !== undefined
            && revision !== undefined
            && previous.revision > revision
            && (!baseName || !previous.baseName || normalizeKey(previous.baseName) === normalizeKey(baseName))
        ) {
            return previous;
        }
        const incomingItems: PlanProgressItem[] = [];

        const pushRawItems = (rawItems: unknown, prefix: string, fallbackStatus: PlanProgressStatus) => {
            if (!Array.isArray(rawItems)) return;
            rawItems.forEach((raw, index) => {
                const item = itemFromSnapshotRaw(raw, `${execution.executionId || execution.id}-${prefix}-${index}`, fallbackStatus);
                if (item) upsertItem(incomingItems, item);
            });
        };

        if (Array.isArray(metadata.items)) {
            pushRawItems(metadata.items, 'item', 'pending');
        } else {
            pushRawItems(metadata.view_items ?? metadata.viewItems, 'view', 'pending');
        }

        if (error) {
            upsertItem(incomingItems, {
                id: `${execution.executionId || execution.id}-error`,
                title: `计划更新失败：${error}`,
                status: 'failed',
            });
        }

        const nextItems = [...incomingItems];

        if (nextItems.length === 0 && metadata.empty === true) {
            return null;
        }

        const nextBaseName = baseName || previous?.baseName;
        const nextFileName = fileName || previous?.fileName;
        const nextAction = action || previous?.action;
        const nextRevision = revision ?? previous?.revision;
        const nextError = error || undefined;

        if (
            previous
            && previous.baseName === nextBaseName
            && previous.fileName === nextFileName
            && previous.action === nextAction
            && previous.revision === nextRevision
            && previous.error === nextError
            && arePlanProgressItemsEqual(previous.items, nextItems)
        ) {
            return previous;
        }

        return {
            baseName: nextBaseName,
            fileName: nextFileName,
            action: nextAction,
            revision: nextRevision,
            items: nextItems,
            updatedAt: Date.now(),
            error: nextError,
        };
    }

    const nextItems = previous?.items ? [...previous.items] : [];
    applyHeuristicExecution(nextItems, execution);
    if (nextItems.length === 0) {
        return previous;
    }

    const nextAction = readString(parseObject(execution.input), ['action']) || previous?.action;
    if (previous && previous.action === nextAction && arePlanProgressItemsEqual(previous.items, nextItems)) {
        return previous;
    }

    return {
        ...previous,
        action: nextAction,
        items: nextItems,
        updatedAt: Date.now(),
    };
};

export const derivePlanProgressItems = (
    progress: { percentage: number; details: string } | undefined,
    executions: PlanProgressExecution[],
): PlanProgressItem[] => {
    const snapshot = executions.reduce<PlanProgressSnapshot | null>((current, execution) => {
        return applyPlanProgressExecution(current, execution);
    }, null);

    const items = snapshot?.items ? [...snapshot.items] : [];
    if (progress?.details?.trim()) {
        const progressTitle = progress.details.trim();
        if (!items.some((item) => item.title === progressTitle)) {
            items.push({
                id: 'current-progress',
                title: progressTitle,
                status: progress.percentage >= 100 ? 'completed' : 'running',
            });
        }
    }

    return items;
};

export const planProgressPercent = (items: PlanProgressItem[]): number => {
    if (items.length === 0) return 0;
    const completedCount = items.filter((item) => item.status === 'completed').length;
    return Math.round((completedCount / items.length) * 100);
};

// Raw types matching Go's sql.Null* JSON structure
type NullString = { String: string; Valid: boolean };
type NullBool = { Bool: boolean; Valid: boolean };
type NullInt64 = { Int64: number; Valid: boolean };

type RawScheduledTask = {
    id: string;
    task_type: string;
    parameters: string;
    schedule_expression: NullString | string; // Could be simple string if not null? Usually object for sql.NullString
    is_active: NullBool | boolean;
    last_run_at: NullInt64 | number | null;
    next_run_at: NullInt64 | number | null;
    created_at: number;
};

export type ScheduledTask = {
    id: string;
    task_type: string;
    parameters: string; // JSON string
    schedule_expression: string;
    is_active: boolean;
    last_run_at: string | null; // ISO string for frontend
    next_run_at: string | null; // ISO string for frontend
    created_at: string;
};

const API_BASE_URL = '/v1';

// Helper to extract value from Null* types
function unwrapNullString(val: NullString | string | null): string {
    if (val === null || val === undefined) return '';
    if (typeof val === 'string') return val;
    return val.Valid ? val.String : '';
}

function unwrapNullBool(val: NullBool | boolean | null): boolean {
    if (val === null || val === undefined) return false;
    if (typeof val === 'boolean') return val;
    return val.Valid ? val.Bool : false;
}

// Special handler for Go timestamps which might be simple integers
function formatTimestamp(ts: number): string {
    // Heuristic: if small, it's seconds. If large, ms.
    // 2025 is ~1.7e9 seconds, ~1.7e12 ms.
    if (ts < 10000000000) {
        return new Date(ts * 1000).toISOString();
    }
    return new Date(ts).toISOString();
}

export const taskService = {
    async list(): Promise<ScheduledTask[]> {
        const response = await fetch(`${API_BASE_URL}/tasks`);
        if (!response.ok) {
            throw new Error('Failed to fetch tasks');
        }
        const rawData: RawScheduledTask[] = await response.json();
        
        return rawData.map(raw => {
            // Handle created_at which is int64 in DB
            const createdAt = typeof raw.created_at === 'number' ? formatTimestamp(raw.created_at) : raw.created_at;

            // Handle run times
            let lastRun: string | null = null;
            if (raw.last_run_at) {
                if (typeof raw.last_run_at === 'number') lastRun = formatTimestamp(raw.last_run_at);
                else if (typeof raw.last_run_at === 'object' && raw.last_run_at.Valid) lastRun = formatTimestamp(raw.last_run_at.Int64);
            }

            let nextRun: string | null = null;
            if (raw.next_run_at) {
                if (typeof raw.next_run_at === 'number') nextRun = formatTimestamp(raw.next_run_at);
                else if (typeof raw.next_run_at === 'object' && raw.next_run_at.Valid) nextRun = formatTimestamp(raw.next_run_at.Int64);
            }

            return {
                id: raw.id,
                task_type: raw.task_type,
                parameters: raw.parameters,
                schedule_expression: unwrapNullString(raw.schedule_expression),
                is_active: unwrapNullBool(raw.is_active),
                last_run_at: lastRun,
                next_run_at: nextRun,
                created_at: createdAt
            };
        });
    },

    async delete(id: string): Promise<void> {
        const response = await fetch(`${API_BASE_URL}/tasks/${id}`, {
            method: 'DELETE',
        });
        if (!response.ok) {
            throw new Error('Failed to delete task');
        }
    },

    async toggle(id: string): Promise<void> {
        const response = await fetch(`${API_BASE_URL}/tasks/${id}?action=toggle`, {
            method: 'POST',
        });
        if (!response.ok) {
            throw new Error('Failed to toggle task');
        }
    }
};

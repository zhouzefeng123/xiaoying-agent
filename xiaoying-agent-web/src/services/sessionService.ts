import type { ContextStats } from '../lib/ws';

export interface Session {
    ID: string; // Go struct fields are capitalized in JSON usually? No, check encoding/json tags
    Title: string;
    MessageCount: number;
    PromptTokens: number;
    CompletionTokens: number;
    SummaryMessageID: string;
    Cost: number;
    CreatedAt: number;
    UpdatedAt: number;
    summary?: string; // From SessionWithSummary
}

export interface SessionMessageRecord {
    id: string;
    session_id: string;
    role: string;
    parts: string;
    created_at: number;
    updated_at: number;
}

export interface ContextStatsRequestOptions {
    model?: string;
    mode?: string;
    enableSubagents?: boolean;
}

// Check if Go struct tags use snake_case or match field names.
// Looking at models.go in internal/db: `json:"id"` (snake_case)
// Looking at internal/session/session.go: No json tags visible in the struct definition I read earlier?
// Wait, I read internal/session/session.go and it didn't have json tags in the snippet.
// But the API response usually relies on default JSON marshaling or explicit tags.
// If no tags, it uses field names (Title).
// If DB models have tags, maybe they are used?
// No, internal/session/session.go `fromDBItem` maps to `Session` struct.
// Let's assume Capitalized unless I verify tags.

const API_BASE_URL = '/v1';

async function fetchWithTimeout(url: string, init?: RequestInit, timeoutMs = 8000): Promise<Response> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    try {
        return await fetch(url, { ...init, signal: controller.signal });
    } finally {
        clearTimeout(timer);
    }
}

export const sessionService = {
    async list(timeoutMs = 8000): Promise<Session[]> {
        const response = await fetchWithTimeout(`${API_BASE_URL}/sessions`, undefined, timeoutMs);
        if (!response.ok) {
            throw new Error('Failed to fetch sessions');
        }
        return response.json();
    },

    async get(id: string): Promise<Session> {
        const response = await fetch(`${API_BASE_URL}/sessions/${id}`);
        if (!response.ok) {
            throw new Error('Failed to get session');
        }
        return response.json();
    },

    async getMessages(id: string, timeoutMs = 10000): Promise<SessionMessageRecord[]> {
        const response = await fetchWithTimeout(`${API_BASE_URL}/sessions/${id}/messages`, undefined, timeoutMs);
        if (!response.ok) {
            throw new Error('Failed to get session messages');
        }
        return response.json();
    },

    async getContextStats(id: string, options?: ContextStatsRequestOptions, timeoutMs = 10000): Promise<ContextStats> {
        const params = new URLSearchParams();
        if (options?.model) {
            params.set('model', options.model);
        }
        if (options?.mode) {
            params.set('mode', options.mode);
        }
        if (typeof options?.enableSubagents === 'boolean') {
            params.set('enable_subagents', String(options.enableSubagents));
        }
        const query = params.toString();
        const response = await fetchWithTimeout(`${API_BASE_URL}/sessions/${id}/context-stats${query ? `?${query}` : ''}`, undefined, timeoutMs);
        if (!response.ok) {
            throw new Error('Failed to get session context stats');
        }
        return response.json();
    },

    async getPlanSnapshot(id: string, timeoutMs = 10000): Promise<unknown | null> {
        const response = await fetchWithTimeout(`${API_BASE_URL}/sessions/${id}/plan-snapshot`, undefined, timeoutMs);
        if (response.status === 204) {
            return null;
        }
        if (!response.ok) {
            throw new Error('Failed to get session plan snapshot');
        }
        return response.json();
    },

    async delete(id: string): Promise<void> {
        const response = await fetch(`${API_BASE_URL}/sessions/${id}`, {
            method: 'DELETE',
        });
        if (!response.ok) {
            throw new Error('Failed to delete session');
        }
    },

    async generateSummary(id: string): Promise<{ summary: string; message_id: string }> {
        const response = await fetch(`${API_BASE_URL}/sessions/${id}/summary`, {
            method: 'POST',
        });
        if (!response.ok) {
            throw new Error('Failed to generate summary');
        }
        return response.json();
    }
};

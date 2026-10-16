export interface Memory {
    id: string;
    category: 'habit' | 'preference' | 'instruction' | 'fact' | string;
    content: string;
    source: string;
    created_at: number;
    updated_at: number;
    layer?: number;
    summary?: { String: string; Valid: boolean }; // Matches Go sql.NullString JSON
}

export interface MemoryDedupStats {
    total_memories: number;
    scanned_categories: number;
    duplicate_by_text: number;
    duplicate_by_semantic: number;
    deleted: number;
}

const API_BASE_URL = '/v1';

export const memoryService = {
    async search(query: string): Promise<Memory[]> {
        const url = new URL(`${API_BASE_URL}/memories`, window.location.origin);
        url.searchParams.append('q', query);
        const response = await fetch(url.toString());
        if (!response.ok) {
            throw new Error('Failed to search memories');
        }
        return response.json();
    },

    async list(category?: string): Promise<Memory[]> {
        const url = new URL(`${API_BASE_URL}/memories`, window.location.origin);
        if (category) {
            url.searchParams.append('category', category);
        }
        const response = await fetch(url.toString());
        if (!response.ok) {
            throw new Error('Failed to fetch memories');
        }
        return response.json();
    },

    async add(content: string, category: string, source: string = 'user_input'): Promise<Memory> {
        const response = await fetch(`${API_BASE_URL}/memories`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({ content, category, source }),
        });
        if (!response.ok) {
            throw new Error('Failed to add memory');
        }
        return response.json();
    },

    async update(id: string, content: string, category: string): Promise<Memory> {
        const response = await fetch(`${API_BASE_URL}/memories/${id}`, {
            method: 'PUT',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({ content, category }),
        });
        if (!response.ok) {
            throw new Error('Failed to update memory');
        }
        return response.json();
    },

    async delete(id: string): Promise<void> {
        const response = await fetch(`${API_BASE_URL}/memories/${id}`, {
            method: 'DELETE',
        });
        if (!response.ok) {
            throw new Error('Failed to delete memory');
        }
    },

    async deduplicate(dryRun: boolean = true, threshold: number = 0.92): Promise<MemoryDedupStats> {
        const response = await fetch(`${API_BASE_URL}/memories/dedup`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({ dry_run: dryRun, threshold }),
        });
        if (!response.ok) {
            throw new Error('Failed to deduplicate memories');
        }
        return response.json();
    }
};

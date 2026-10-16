import { describe, expect, it } from 'vitest';

import { formatWarmContextSource } from './ContextWindowButton';

describe('ContextWindowButton warm context observability', () => {
    it('explains session-scope misses instead of showing generic not injected', () => {
        const text = formatWarmContextSource({
            warm_context_status: 'session_scope_miss',
            implicit_global_fallback_skipped: true,
        });

        expect(text).toContain('当前/父/祖先会话');
        expect(text).toContain('已跳过隐式全局回退');
    });

    it('shows when warm context was not triggered due to missing user input', () => {
        const text = formatWarmContextSource({
            warm_context_status: 'disabled_no_user_input',
        });

        expect(text).toContain('当前没有可检索的用户输入');
    });

    it('prefers explicit warm-context source labels on hits', () => {
        const text = formatWarmContextSource({
            warm_context_source: 'parent_session',
            warm_context_session_id: 'parent-session',
            warm_context_status: 'hit',
        });

        expect(text).toBe('父会话 · parent-session');
    });
});
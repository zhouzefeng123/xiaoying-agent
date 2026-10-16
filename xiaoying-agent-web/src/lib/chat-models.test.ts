import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { recordResolvedChatModelObservation, resolveChatModelAttemptSequence, resolveChatModelSelection, shouldRetryAutoModelAttempt } from './chat-models'

function installLocalStorageMock() {
    const values = new Map<string, string>()
    vi.stubGlobal('window', {
        localStorage: {
            getItem: (key: string) => values.get(key) ?? null,
            setItem: (key: string, value: string) => values.set(key, value),
            removeItem: (key: string) => values.delete(key),
            clear: () => values.clear(),
        },
    })
}

describe('chat-models auto routing', () => {
    beforeEach(() => {
        installLocalStorageMock()
    })

    afterEach(() => {
        vi.unstubAllGlobals()
    })

    it('keeps auto requests on preset paid model first', () => {
        expect(resolveChatModelSelection('auto', '随便聊聊')).toBe('deepseek-v4-flash')
        expect(resolveChatModelAttemptSequence('auto', '帮我改一下这个前端 bug')).toEqual(['deepseek-v4-flash', 'deepseek-v4-pro', 'kimi-k2.6', 'glm-5.1', 'minimax-m2.7'])
    })

    it('does not change auto routing for complex prompts or code attachments', () => {
        const attachments = [new File(['const a = 1;'], 'demo.ts', { type: 'text/typescript' })]

        expect(resolveChatModelAttemptSequence('auto', '请重构这个复杂仓库并排查白屏问题', attachments)).toEqual(['deepseek-v4-flash', 'deepseek-v4-pro', 'kimi-k2.6', 'glm-5.1', 'minimax-m2.7'])
    })

    it('keeps explicit selections unchanged', () => {
        expect(resolveChatModelAttemptSequence('kimi-k2.6', '测试')).toEqual(['kimi-k2.6'])
        expect(resolveChatModelAttemptSequence('custom-main-model', '测试')).toEqual(['custom-main-model'])
    })

    it('prefers the model with faster output throughput after observations', () => {
        const observations = {
            'deepseek-v4-flash': { ttftMs: 80, totalMs: 2600, outputChars: 600 },
            'deepseek-v4-pro': { ttftMs: 500, totalMs: 1100, outputChars: 600 },
            'kimi-k2.6': { ttftMs: 120, totalMs: 3600, outputChars: 600 },
            'glm-5.1': { ttftMs: 140, totalMs: 4200, outputChars: 600 },
            'minimax-m2.7': { ttftMs: 160, totalMs: 4600, outputChars: 600 },
        } as const

        for (const [model, observation] of Object.entries(observations)) {
            recordResolvedChatModelObservation(model as keyof typeof observations, {
                status: 'success',
                ...observation,
            })
        }

        expect(resolveChatModelSelection('auto', '输出一段长文本')).toBe('deepseek-v4-pro')
    })

    it('keeps default order until at least two models have observations', () => {
        recordResolvedChatModelObservation('deepseek-v4-pro', {
            status: 'success',
            ttftMs: 120,
            totalMs: 900,
            outputChars: 600,
        })

        expect(resolveChatModelSelection('auto', 'write a long answer')).toBe('deepseek-v4-flash')
    })

    it('uses latency ranking after two models have observations', () => {
        recordResolvedChatModelObservation('deepseek-v4-flash', {
            status: 'success',
            ttftMs: 80,
            totalMs: 2600,
            outputChars: 600,
        })
        recordResolvedChatModelObservation('deepseek-v4-pro', {
            status: 'success',
            ttftMs: 120,
            totalMs: 900,
            outputChars: 600,
        })

        expect(resolveChatModelSelection('auto', 'write a long answer')).toBe('deepseek-v4-pro')
    })

    it('retries 1403-style early failures before first token', () => {
        expect(shouldRetryAutoModelAttempt({ errorCode: 1403 })).toBe(true)
        expect(shouldRetryAutoModelAttempt({ errorCode: 1403, ttftMs: 0 })).toBe(true)
        expect(shouldRetryAutoModelAttempt({ errorCode: 1403, ttftMs: 120 })).toBe(false)
    })

    it('keeps retrying other auto-model failures', () => {
        expect(shouldRetryAutoModelAttempt({ errorCode: 1500, ttftMs: 120 })).toBe(true)
        expect(shouldRetryAutoModelAttempt({ errorCode: null })).toBe(true)
    })
})

import { describe, expect, it } from 'vitest'
import { CHAT_MODEL_OPTIONS, DEFAULT_CHAT_MODEL_SELECTION, resolveChatModelAttemptSequence, resolveChatModelSelection, shouldRetryAutoModelAttempt } from './chat-models'

describe('account model routing', () => {
    it('starts without any preconfigured models', () => {
        expect(DEFAULT_CHAT_MODEL_SELECTION).toBe('')
        expect(CHAT_MODEL_OPTIONS).toEqual([])
    })
    it.each(['', 'auto', 'custom-main-model', 'deepseek-v4-pro'])('does not turn legacy selection %s into a global model', selection => {
        expect(resolveChatModelSelection(selection, 'hello')).toBe('')
        expect(resolveChatModelAttemptSequence(selection, 'hello')).toEqual([])
    })
    it('uses precisely the selected account model on each request', () => {
        for (const id of ['user-model-first', 'user-model-second']) {
            expect(resolveChatModelSelection(id, 'hello')).toBe(id)
            expect(resolveChatModelAttemptSequence(id, 'hello')).toEqual([id])
        }
    })
    it('does not retry a different provider on error', () => {
        expect(shouldRetryAutoModelAttempt({errorCode:1403})).toBe(false)
    })
})

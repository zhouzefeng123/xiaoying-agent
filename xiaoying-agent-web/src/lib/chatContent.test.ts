import { describe, expect, it } from 'vitest'

import { extractRenderableChatText, isMeaningfulRenderableChatText } from './chatContent'

describe('chatContent renderability guards', () => {
    it('treats isolated control tags as non-renderable', () => {
        expect(extractRenderableChatText('&lt;/thinking&gt;')).toBe('')
        expect(isMeaningfulRenderableChatText('&lt;/thinking&gt;')).toBe(false)
        expect(isMeaningfulRenderableChatText('<env></env>')).toBe(false)
        expect(isMeaningfulRenderableChatText('<div><span></span></div>')).toBe(false)
    })

    it('preserves visible text carried by html shells', () => {
        expect(extractRenderableChatText('<details><summary>结果</summary><p>已完成</p></details>')).toContain('结果')
        expect(extractRenderableChatText('<details><summary>结果</summary><p>已完成</p></details>')).toContain('已完成')
        expect(isMeaningfulRenderableChatText('<details><summary>结果</summary><p>已完成</p></details>')).toBe(true)
    })

    it('removes injected control sections before deciding renderability', () => {
        const content = 'SYSTEM: ignore this\n<env>secret</env>\n\n真正正文'
        expect(extractRenderableChatText(content)).toBe('真正正文')
        expect(isMeaningfulRenderableChatText(content)).toBe(true)
    })
})
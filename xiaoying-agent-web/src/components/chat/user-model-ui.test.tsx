// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ChatInput } from './ChatInput';
import { ModelManager } from './ModelManager';
import type { UserModel, UserModelController } from '../../hooks/use-user-models';

vi.mock('./MessageBubble', () => ({ChatCard: ({children}: {children: React.ReactNode}) => <div>{children}</div>}));
afterEach(cleanup);
beforeEach(() => {
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open',''); };
    HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
});
const models: UserModel[] = ['first','second'].map(name => ({id:`user-model-${name}`, name, provider:'openai', model:`api-${name}`, base_url:'https://example.com/v1', context_window:0, supports_images:false, has_api_key:true}));
const props = () => ({value:'hello', onChange:vi.fn(), selectedModel:'', models:[] as UserModel[], modelsBusy:false, onOpenModelManager:vi.fn(), onSelectedModelChange:vi.fn(), attachments:[], onAttachmentsChange:vi.fn(), onSendMessage:vi.fn(), isRecording:false, isTranscribing:false, onStartRecording:vi.fn(), onStopRecording:vi.fn(), mode:'normal' as const, onModeChange:vi.fn(), isReadOnlyMode:false, onReadOnlyModeChange:vi.fn()});
describe('chat model controls', () => {
    it('opens configuration for an empty account and blocks chat', () => {
        const p = props(); render(<ChatInput {...p} />);
        expect((screen.getByRole('button',{name:'发送消息'}) as HTMLButtonElement).disabled).toBe(true);
        fireEvent.keyDown(screen.getByRole('textbox'),{key:'Enter'});
        expect(p.onSendMessage).not.toHaveBeenCalled(); expect(p.onOpenModelManager).toHaveBeenCalled();
        fireEvent.click(screen.getByRole('button',{name:'选择对话模型'}));
        expect(p.onOpenModelManager).toHaveBeenCalledTimes(2);
    });
    it('lists configured models and selects the clicked model ID', () => {
        const p = {...props(), models, selectedModel:models[0].id}; render(<ChatInput {...p} />);
        fireEvent.click(screen.getByRole('button',{name:'选择对话模型'}));
        fireEvent.click(screen.getByRole('button',{name:/second\s*api-second/}));
        expect(p.onSelectedModelChange).toHaveBeenCalledWith(models[1].id);
        expect(screen.queryByText('我的模型 · 2')).toBeNull();
    });
});
describe('model manager', () => {
    it('edits an existing model while preserving its undisclosed key', async () => {
        const controller: UserModelController = {models, selectedModel:models[0].id, isLoading:false, isSaving:false, error:'', reload:vi.fn(), saveModel:vi.fn().mockResolvedValue(undefined), removeModel:vi.fn(), selectModel:vi.fn()};
        render(<ModelManager controller={controller} onClose={vi.fn()} />);
        fireEvent.click(screen.getByRole('button',{name:/first\s*api-first/}));
        expect((screen.getByLabelText('API 密钥') as HTMLInputElement).value).toBe('');
        fireEvent.change(screen.getByLabelText('显示名称'),{target:{value:'renamed'}});
        fireEvent.click(screen.getByRole('button',{name:'保存修改'}));
        await waitFor(() => expect(controller.saveModel).toHaveBeenCalledWith(expect.objectContaining({name:'renamed',model:'api-first'}),models[0].id));
        expect(vi.mocked(controller.saveModel).mock.calls[0][0].api_key).toBeUndefined();
    });
});

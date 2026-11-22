// @vitest-environment jsdom
import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useUserModels } from './use-user-models';

beforeEach(() => {
    const values = new Map<string, string>();
    vi.stubGlobal('localStorage', {
        getItem: (key: string) => values.get(key) ?? null,
        setItem: (key: string, value: string) => values.set(key, value),
        removeItem: (key: string) => values.delete(key),
        clear: () => values.clear(),
    });
});
afterEach(() => { cleanup(); localStorage.clear(); vi.unstubAllGlobals(); });
const model = (id: string) => ({id, name:id, provider:'openai', model:id, base_url:'https://example.com/v1', context_window:0, supports_images:false, has_api_key:true});
const response = (models: ReturnType<typeof model>[], selected_id = '') => ({ok:true, text:async () => JSON.stringify({models,selected_id})});

describe('user model state', () => {
    it('loads only after login and starts with an empty account', async () => {
        const fetcher = vi.fn().mockResolvedValue(response([])); vi.stubGlobal('fetch',fetcher);
        const {result,rerender} = renderHook(({owner}) => useUserModels(owner), {initialProps:{owner:undefined as string | undefined}});
        expect(fetcher).not.toHaveBeenCalled();
        localStorage.setItem('auth_token','account-token');
        rerender({owner:'alice@example.com'});
        await waitFor(() => expect(result.current.isLoading).toBe(false));
        expect(result.current.models).toEqual([]); expect(result.current.selectedModel).toBe('');
        expect(fetcher.mock.calls[0][1].headers.Authorization).toBe('Bearer account-token');
    });
    it('waits for the server to save selection before using the next model', async () => {
        const models = [model('user-model-first'),model('user-model-second')];
        let finish!: (value: ReturnType<typeof response>) => void;
        const fetcher = vi.fn().mockResolvedValueOnce(response(models,models[0].id)).mockImplementationOnce(() => new Promise(resolve => {finish=resolve;}));
        vi.stubGlobal('fetch',fetcher);
        const {result} = renderHook(() => useUserModels('alice@example.com'));
        await waitFor(() => expect(result.current.selectedModel).toBe(models[0].id));
        let pending!: Promise<void>;
        act(() => {pending=result.current.selectModel(models[1].id);});
        expect(result.current.isSaving).toBe(true); expect(result.current.selectedModel).toBe(models[0].id);
        await act(async () => {finish(response(models,models[1].id)); await pending;});
        expect(result.current.selectedModel).toBe(models[1].id); expect(result.current.isSaving).toBe(false);
        expect(fetcher.mock.calls[1][0]).toBe('/v1/user-models/selection');
        expect(JSON.parse(fetcher.mock.calls[1][1].body)).toEqual({selected_id:models[1].id});
    });
    it('discards pending model requests when another account logs in', async () => {
        let finish!: (value: ReturnType<typeof response>) => void;
        const fetcher = vi.fn().mockImplementationOnce(() => new Promise(resolve => {finish=resolve;})).mockResolvedValueOnce(response([]));
        vi.stubGlobal('fetch',fetcher);
        const {result,rerender} = renderHook(({owner}) => useUserModels(owner),{initialProps:{owner:'alice@example.com'}});
        rerender({owner:'bob@example.com'});
        await waitFor(() => expect(result.current.isLoading).toBe(false));
        await act(async () => {finish(response([model('user-model-private')],'user-model-private'));});
        expect(result.current.models).toEqual([]); expect(result.current.selectedModel).toBe('');
    });
});

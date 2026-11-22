import { useCallback, useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';

export interface UserModel {
    id: string;
    name: string;
    provider: 'openai' | 'custom' | 'ollama';
    model: string;
    base_url: string;
    context_window: number;
    supports_images: boolean;
    has_api_key: boolean;
}
export type UserModelInput = Omit<UserModel, 'id' | 'has_api_key'> & { api_key?: string };
interface ModelSettings { models: UserModel[]; selected_id: string; }
const EMPTY: ModelSettings = { models: [], selected_id: '' };

export function useUserModels(owner: string | undefined) {
    const [state, setState] = useState<{owner?: string; settings: ModelSettings}>({settings: EMPTY});
    const [isLoading, setLoading] = useState(false);
    const [isSaving, setSaving] = useState(false);
    const [error, setError] = useState('');
    const version = useRef(0);
    const ownerRef = useRef(owner);
    ownerRef.current = owner;
    const settings = state.owner === owner ? state.settings : EMPTY;

    const request = useCallback(async (path = '', method = 'GET', body?: unknown) => {
        const token = localStorage.getItem('auth_token');
        const response = await fetch(`/v1/user-models${path}`, {
            method, headers: {'Content-Type': 'application/json', ...(token ? {Authorization: `Bearer ${token}`} : {})},
            body: body === undefined ? undefined : JSON.stringify(body),
        });
        const text = await response.text();
        let data;
        try { data = JSON.parse(text); } catch { throw new Error(text || '模型服务返回了无效响应'); }
        if (!response.ok) throw new Error(data.message || '模型配置操作失败');
        return data as ModelSettings;
    }, []);

    const reload = useCallback(async () => {
        const current = ++version.current;
        setSaving(false);
        if (!owner) { setState({settings: EMPTY}); setLoading(false); return; }
        setLoading(true); setError('');
        try {
            const data = await request();
            if (current === version.current && ownerRef.current === owner) setState({owner, settings: data});
        } catch (e) {
            if (current === version.current && ownerRef.current === owner) setError(e instanceof Error ? e.message : '读取模型配置失败');
        } finally { if (current === version.current) setLoading(false); }
    }, [owner, request]);
    useEffect(() => { void reload(); return () => { version.current++; }; }, [reload]);

    const mutate = useCallback(async (path: string, method: string, body?: unknown) => {
        if (!owner) throw new Error('请先登录');
        const current = ++version.current;
        setSaving(true);
        try {
            const data = await request(path, method, body);
            if (current === version.current && ownerRef.current === owner) { setState({owner, settings:data}); setError(''); }
        } finally { if (current === version.current && ownerRef.current === owner) { setSaving(false); setLoading(false); } }
    }, [owner, request]);
    const saveModel = useCallback((input: UserModelInput, id?: string) => mutate(id ? `/${id}` : '', id ? 'PUT' : 'POST', input), [mutate]);
    const removeModel = useCallback((id: string) => mutate(`/${id}`, 'DELETE'), [mutate]);
    const selectModel = useCallback(async (id: string) => {
        try { await mutate('/selection', 'PATCH', {selected_id:id}); }
        catch (e) { toast.error(e instanceof Error ? e.message : '切换模型失败'); }
    }, [mutate]);
    return { models:settings.models, selectedModel:settings.selected_id, isLoading, isSaving, error, reload, saveModel, removeModel, selectModel };
}
export type UserModelController = ReturnType<typeof useUserModels>;

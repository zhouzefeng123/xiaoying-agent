import { useEffect, useRef, useState } from 'react';
import { Check, Cpu, Eye, EyeOff, Loader2, Plus, Trash2, X } from 'lucide-react';
import { toast } from 'sonner';
import type { UserModel, UserModelController, UserModelInput } from '../../hooks/use-user-models';
import { cn } from '../../lib/utils';

const blankModel = (): UserModelInput => ({name:'', provider:'openai', model:'', base_url:'', api_key:'', context_window:0, supports_images:false});

export function ModelManager({controller, onClose}: {controller:UserModelController; onClose:()=>void}) {
    const dialogRef = useRef<HTMLDialogElement>(null);
    const [editingId, setEditingId] = useState<string>();
    const [form, setForm] = useState<UserModelInput>(blankModel);
    const [keyVisible, setKeyVisible] = useState(false);
    const [deleteId, setDeleteId] = useState<string>();
    const {models, selectedModel, isSaving, isLoading, error, saveModel, removeModel, selectModel, reload} = controller;
    const editing = models.find(model => model.id === editingId);
    useEffect(() => {
        const dialog = dialogRef.current;
        dialog?.showModal();
        return () => dialog?.close();
    }, []);
    const edit = (model?:UserModel) => {
        setEditingId(model?.id); setDeleteId(undefined); setKeyVisible(false);
        setForm(model ? {name:model.name, provider:model.provider, model:model.model, base_url:model.base_url, context_window:model.context_window, supports_images:model.supports_images} : blankModel());
    };
    const inputClass = 'w-full rounded-xl border border-slate-200 bg-white px-3 py-2.5 text-sm text-slate-800 outline-none transition focus:border-blue-400 focus:ring-4 focus:ring-blue-500/10 disabled:opacity-50 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100';
    const save = async (event:React.FormEvent) => {
        event.preventDefault();
        if (!form.model.trim()) { toast.error('请填写模型名称'); return; }
        try {
            await saveModel(form, editingId);
            toast.success(editingId ? '模型配置已更新' : '模型已添加');
            edit();
        } catch (e) { toast.error(e instanceof Error ? e.message : '保存失败'); }
    };
    const remove = async (id:string) => {
        try { await removeModel(id); if (editingId === id) edit(); setDeleteId(undefined); toast.success('模型已删除'); }
        catch (e) { toast.error(e instanceof Error ? e.message : '删除失败'); }
    };
    return (
        <dialog ref={dialogRef} onCancel={onClose} onClick={event => {if (event.target === event.currentTarget) onClose();}} aria-labelledby="model-manager-title" className="fixed inset-auto bottom-5 right-5 m-0 w-[min(760px,calc(100vw-40px))] max-h-[calc(100dvh-40px)] overflow-hidden rounded-3xl border border-slate-200 bg-white p-0 text-slate-800 shadow-[0_24px_100px_rgba(35,61,96,0.22)] backdrop:bg-slate-950/25 backdrop:backdrop-blur-sm dark:border-zinc-700 dark:bg-zinc-950 dark:text-zinc-100">
            <header className="flex items-center justify-between border-b border-slate-100 px-6 py-5 dark:border-zinc-800">
                <div className="flex items-center gap-3">
                    <span className="flex h-10 w-10 items-center justify-center rounded-2xl bg-blue-50 text-blue-600 dark:bg-blue-500/10"><Cpu className="h-5 w-5" /></span>
                    <div><h2 id="model-manager-title" className="text-base font-semibold">我的模型</h2><p className="mt-0.5 text-xs text-slate-500">仅当前账号可用，保存后即可切换使用</p></div>
                </div>
                <button type="button" onClick={onClose} aria-label="关闭模型配置" className="rounded-full p-2 text-slate-400 hover:bg-slate-100 dark:hover:bg-zinc-800"><X className="h-5 w-5" /></button>
            </header>
            <div className="grid max-h-[calc(100dvh-140px)] overflow-y-auto sm:grid-cols-[240px_1fr]">
                <aside className="border-b border-slate-100 bg-slate-50/70 p-4 sm:border-b-0 sm:border-r dark:border-zinc-800 dark:bg-zinc-900/40">
                    <div className="mb-3 flex items-center justify-between text-xs text-slate-500"><span>已配置 · {models.length}</span><button type="button" onClick={() => edit()} disabled={isSaving} className="flex items-center gap-1 rounded-md px-2 py-1 text-blue-600 hover:bg-blue-50"><Plus className="h-3.5 w-3.5" />添加模型</button></div>
                    {isLoading && <Loader2 className="mx-auto my-6 h-5 w-5 animate-spin text-blue-500" />}
                    {error && <div role="alert" className="rounded-xl bg-red-50 p-3 text-xs text-red-600">{error}<button type="button" onClick={() => void reload()} className="mt-2 block underline">重新加载</button></div>}
                    {!isLoading && !models.length && !error && <div className="rounded-2xl border border-dashed border-slate-200 px-4 py-7 text-center dark:border-zinc-700"><Cpu className="mx-auto mb-3 h-7 w-7 text-slate-300" /><p className="text-sm font-medium">还没有模型</p><p className="mt-2 text-xs leading-5 text-slate-500">添加你的第一个模型，<br />开始今天的对话。</p></div>}
                    <div className="space-y-2">{models.map(model => <div key={model.id} className={cn('rounded-xl border p-3 transition', editingId === model.id ? 'border-blue-200 bg-white shadow-sm dark:border-blue-700 dark:bg-zinc-900' : 'border-transparent hover:bg-white dark:hover:bg-zinc-900')}>
                        <button type="button" disabled={isSaving} onClick={() => edit(model)} className="w-full text-left"><span className="flex items-center gap-2 text-sm font-medium"><span className="min-w-0 flex-1 truncate">{model.name}</span>{model.id === selectedModel && <Check className="h-4 w-4 text-blue-600" />}</span><span className="mt-1 block truncate text-xs text-slate-400">{model.model}</span></button>
                        <div className="mt-3 flex items-center justify-between"><button type="button" disabled={isSaving || selectedModel === model.id} onClick={() => void selectModel(model.id)} className="text-xs font-medium text-blue-600 disabled:text-slate-400">{selectedModel === model.id ? '正在使用' : '切换使用'}</button><button type="button" disabled={isSaving} aria-label={`删除 ${model.name}`} onClick={() => setDeleteId(model.id)} className="p-1 text-slate-400 hover:text-red-500"><Trash2 className="h-3.5 w-3.5" /></button></div>
                        {deleteId === model.id && <div className="mt-3 border-t border-slate-100 pt-3 text-xs"><p className="mb-2 text-slate-500">确定删除这个模型？</p><div className="flex gap-3"><button type="button" disabled={isSaving} onClick={() => void remove(model.id)} className="text-red-600">确认删除</button><button type="button" onClick={() => setDeleteId(undefined)}>取消</button></div></div>}
                    </div>)}</div>
                </aside>
                <form onSubmit={save} className="space-y-4 p-6">
                    <div><h3 className="text-sm font-semibold">{editingId ? '编辑模型' : '添加新模型'}</h3><p className="mt-1 text-xs leading-5 text-slate-400">填写模型服务信息，支持配置多个服务商。</p></div>
                    <fieldset disabled={isSaving || isLoading || !!error} className="space-y-4">
                        <label className="block space-y-1.5 text-xs font-medium text-slate-500"><span>显示名称</span><input aria-label="显示名称" value={form.name} onChange={e => setForm({...form,name:e.target.value})} placeholder="例如：日常助手、代码专家" maxLength={120} className={inputClass} /></label>
                        <label className="block space-y-1.5 text-xs font-medium text-slate-500"><span>模型服务</span><select aria-label="模型服务" value={form.provider} onChange={e => setForm({...form,provider:e.target.value as UserModelInput['provider']})} className={inputClass}><option value="openai">OpenAI 兼容接口</option><option value="custom">自定义兼容接口</option><option value="ollama">Ollama · 本地模型</option></select></label>
                        <label className="block space-y-1.5 text-xs font-medium text-slate-500"><span>模型名称 <span className="text-blue-500">*</span></span><input aria-label="模型名称" required value={form.model} onChange={e => setForm({...form,model:e.target.value})} placeholder={form.provider === 'ollama' ? 'qwen3:latest' : '服务商提供的模型 ID'} maxLength={200} className={inputClass} /></label>
                        <label className="block space-y-1.5 text-xs font-medium text-slate-500"><span>接口地址</span><input aria-label="接口地址" type="url" required={form.provider === 'custom'} value={form.base_url} onChange={e => setForm({...form,base_url:e.target.value})} placeholder={form.provider === 'ollama' ? 'http://localhost:11434/v1' : 'https://api.openai.com/v1'} className={inputClass} /></label>
                        {form.provider !== 'ollama' && <label className="block space-y-1.5 text-xs font-medium text-slate-500"><span>API 密钥</span><div className="relative"><input aria-label="API 密钥" autoComplete="off" type={keyVisible ? 'text' : 'password'} value={form.api_key ?? ''} onChange={e => setForm({...form,api_key:e.target.value})} placeholder={editing?.has_api_key ? '已保存密钥，留空不修改' : '输入服务商提供的 API Key'} className={cn(inputClass,'pr-10')} /><button type="button" aria-label={keyVisible ? '隐藏密钥' : '显示密钥'} onClick={() => setKeyVisible(!keyVisible)} className="absolute right-3 top-3 text-slate-400">{keyVisible ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}</button></div>{editing?.has_api_key && <button type="button" onClick={() => setForm({...form,api_key:''})} className="text-xs text-slate-400 underline">清除已保存的密钥</button>}</label>}
                        <label className="block space-y-1.5 text-xs font-medium text-slate-500"><span>上下文长度（可选）</span><input aria-label="上下文长度" type="number" min="1" max="10000000" value={form.context_window || ''} onChange={e => setForm({...form,context_window:Number(e.target.value)})} placeholder="默认 256000 tokens" className={inputClass} /></label>
                        <label className="flex items-center gap-2.5 text-xs text-slate-500"><input type="checkbox" checked={form.supports_images} onChange={e => setForm({...form,supports_images:e.target.checked})} className="h-4 w-4 rounded accent-blue-600" />此模型支持图片理解</label>
                        <button type="submit" className="flex w-full items-center justify-center gap-2 rounded-xl bg-blue-600 px-4 py-3 text-sm font-medium text-white transition hover:bg-blue-700 disabled:opacity-50">{isSaving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Plus className="h-4 w-4" />}{editingId ? '保存修改' : '添加模型'}</button>
                    </fieldset>
                </form>
            </div>
        </dialog>
    );
}

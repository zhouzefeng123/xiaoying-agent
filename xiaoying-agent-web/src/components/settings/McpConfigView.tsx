import React, { useState, useEffect } from 'react';
import { Loader2, RefreshCw, Terminal, Plus, Trash2, Settings, CheckCircle2, Save, X } from 'lucide-react';
import { toast } from 'sonner';
import { waitForBackendRecovery } from '../../lib/backendRestart.ts';
import { cn } from '../../lib/utils';

interface McpServerConfig {
    type?: 'stdio' | 'sse'; // made optional for JSON parsing leniency
    command?: string;
    args?: string[];
    env?: Record<string, string>; // Changed to Record to match standard JSON format
    url?: string;
    headers?: Record<string, string>;
    disabled?: boolean;
}

export const McpConfigView: React.FC = () => {
    const [servers, setServers] = useState<Record<string, McpServerConfig>>({});
    const [isLoading, setIsLoading] = useState(false);
    const [isSaving, setIsSaving] = useState(false);
    const [isRestarting, setIsRestarting] = useState(false);
    const [restartStatusText, setRestartStatusText] = useState('MCP 配置已更新，正在等待服务重启完成...');
    
    // JSON Add/Edit state
    const [isJsonModalOpen, setIsJsonModalOpen] = useState(false);
    const [jsonInput, setJsonInput] = useState('');
    const [editingId, setEditingId] = useState<string | null>(null);
    const [editForm, setEditForm] = useState<any>({});

    useEffect(() => {
        fetchConfig();
    }, []);

    const fetchConfig = async () => {
        setIsLoading(true);
        try {
            const res = await fetch('/v1/config');
            if (res.ok) {
                const data = await res.json();
                if (data.mcpServers) {
                    setServers(data.mcpServers);
                }
            } else {
                toast.error('Failed to fetch MCP configuration');
            }
        } catch (error) {
            console.error('Error fetching config:', error);
            toast.error('Failed to connect to backend');
        } finally {
            setIsLoading(false);
        }
    };

    const handleSave = async (updatedServers: Record<string, McpServerConfig>) => {
        setIsSaving(true);
        try {
            const res = await fetch('/v1/config', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({
                    mcpServers: updatedServers
                })
            });

            if (res.ok) {
                const data = await res.json();
                if (data.needs_restart === 'true') {
                    toast.success('MCP 配置已保存，正在重启服务...');
                    setIsRestarting(true);
                    setRestartStatusText('MCP 配置已更新，正在等待服务重启完成...');

                    try {
                        const result = await window.electronAPI?.restartBackend?.();
                        if (result?.ok) {
                            await waitForBackendRecovery();
                            setRestartStatusText('服务已恢复，正在重新加载最新 MCP 配置...');
                            await fetchConfig();
                            toast.success('服务已重启，MCP 配置已自动恢复');
                        } else {
                            throw new Error('Backend restart failed');
                        }
                    } catch (restartError) {
                        console.error('Backend recovery check failed:', restartError);
                        toast.error('服务重启超时，请稍后重试或检查服务状态');
                    } finally {
                        setIsRestarting(false);
                        setIsSaving(false);
                    }
                } else {
                    toast.success('MCP 配置已保存（热重载）');
                    setIsSaving(false);
                }
            } else {
                const errText = await res.text();
                toast.error(`保存失败: ${errText}`);
                setIsSaving(false);
            }
        } catch (error) {
            console.error('Error saving config:', error);
            toast.error('保存配置时发生错误');
            setIsSaving(false);
        }
    };

    if (isRestarting) {
        return (
            <div className="flex flex-col items-center justify-center h-full space-y-4">
                <RefreshCw className="w-12 h-12 animate-spin text-blue-500" />
                <div className="text-center">
                    <h3 className="text-lg font-medium text-zinc-900 dark:text-zinc-100">正在重启服务...</h3>
                    <p className="text-sm text-zinc-500 dark:text-zinc-400">{restartStatusText}</p>
                </div>
            </div>
        );
    }

    const handleToggleEnable = (id: string) => {
        const updatedServers = { ...servers };
        if (updatedServers[id]) {
            updatedServers[id] = {
                ...updatedServers[id],
                disabled: !updatedServers[id].disabled
            };
            setServers(updatedServers);
            handleSave(updatedServers);
        }
    };

    const handleDelete = (id: string) => {
        if (!confirm(`确定要删除 MCP 服务 "${id}" 吗？`)) return;
        const updatedServers = { ...servers };
        delete updatedServers[id];
        setServers(updatedServers);
        handleSave(updatedServers);
    };

    const startEdit = (id: string) => {
        setEditingId(id);
        const srv = servers[id];
        
        // Convert env object to array of strings for the input field
        let envArray: string[] = [];
        if (srv.env && typeof srv.env === 'object') {
            envArray = Object.entries(srv.env).map(([k, v]) => `${k}=${v}`);
        } else if (Array.isArray(srv.env)) {
            envArray = srv.env;
        }

        setEditForm({
            id,
            type: srv.type || 'stdio',
            command: srv.command || '',
            args: srv.args || [],
            env: envArray,
            url: srv.url || '',
            headers: srv.headers || {},
            disabled: srv.disabled || false
        });
    };

    const startAdd = () => {
        setEditingId('__new__');
        setEditForm({
            id: '',
            type: 'stdio',
            command: '',
            args: [],
            env: [],
            url: '',
            headers: {},
            disabled: false
        });
    };

    const saveEdit = () => {
        if (!editForm.id.trim()) {
            toast.error('请输入 MCP 服务 ID');
            return;
        }

        const updatedServers = { ...servers };
        
        // If renaming, delete old
        if (editingId && editingId !== '__new__' && editingId !== editForm.id) {
            delete updatedServers[editingId];
        }

        const newConfig: McpServerConfig = {
            type: editForm.type,
            disabled: editForm.disabled
        };

        if (editForm.type === 'stdio') {
            newConfig.command = editForm.command;
            if (editForm.args && editForm.args.length > 0) newConfig.args = editForm.args;
            
            // Convert env array back to object for backend
            if (editForm.env && editForm.env.length > 0) {
                const envObj: Record<string, string> = {};
                editForm.env.forEach((e: string) => {
                    const idx = e.indexOf('=');
                    if (idx > 0) {
                        envObj[e.substring(0, idx).trim()] = e.substring(idx + 1).trim();
                    }
                });
                if (Object.keys(envObj).length > 0) {
                    newConfig.env = envObj;
                }
            }
        } else {
            newConfig.url = editForm.url;
            if (Object.keys(editForm.headers || {}).length > 0) newConfig.headers = editForm.headers;
        }

        updatedServers[editForm.id] = newConfig;
        
        setServers(updatedServers);
        setEditingId(null);
        handleSave(updatedServers);
    };

    const saveJsonEdit = () => {
        try {
            const parsed = JSON.parse(jsonInput);
            const mcpData = parsed.mcpServers || parsed;
            
            const updatedServers = { ...servers };
            
            for (const [id, srv] of Object.entries(mcpData)) {
                updatedServers[id] = srv as McpServerConfig;
            }
            
            setServers(updatedServers);
            setIsJsonModalOpen(false);
            setJsonInput('');
            handleSave(updatedServers);
        } catch (e) {
            toast.error('JSON 格式错误，请检查！');
        }
    };

    if (isLoading) {
        return (
            <div className="flex items-center justify-center h-full">
                <Loader2 className="w-6 h-6 animate-spin text-zinc-400" />
            </div>
        );
    }


    return (
        <div className="space-y-6 animate-in fade-in duration-300">
            <div className="flex items-center justify-between">
                <div className="space-y-1 flex items-center gap-2">
                    <div>
                        <div className="flex items-center gap-2">
                            <h3 className="text-lg font-medium text-zinc-900 dark:text-zinc-100">
                                MCP
                            </h3>
                            <div className="flex items-center justify-center w-4 h-4 rounded-full bg-zinc-200 dark:bg-zinc-700 text-[10px] text-zinc-500 cursor-help" title="Model Context Protocol">
                                ?
                            </div>
                        </div>
                        <p className="text-sm text-zinc-500 dark:text-zinc-400">
                            修改 MCP 服务配置后需要重启服务才能生效，保存后将自动重启。
                        </p>
                    </div>
                </div>
                <div className="flex items-center gap-2">
                    <button 
                        onClick={fetchConfig}
                        className="p-1.5 bg-zinc-100 hover:bg-zinc-200 dark:bg-zinc-800 dark:hover:bg-zinc-700 text-zinc-600 dark:text-zinc-300 rounded-md transition-colors"
                        title="刷新"
                    >
                        <RefreshCw className={cn("w-4 h-4", isLoading && "animate-spin")} />
                    </button>
                    {!editingId && (
                        <button
                            onClick={() => {
                                setJsonInput('{\n  "mcpServers": {\n    "my-server": {\n      "command": "npx",\n      "args": ["-y", "my-server@latest"],\n      "env": {}\n    }\n  }\n}');
                                setIsJsonModalOpen(true);
                            }}
                            className="flex items-center gap-1.5 px-3 py-1.5 bg-zinc-100 hover:bg-zinc-200 dark:bg-zinc-800 dark:hover:bg-zinc-700 text-zinc-900 dark:text-zinc-100 text-sm font-medium rounded-md transition-colors border border-zinc-200 dark:border-zinc-700"
                        >
                            <Plus className="w-4 h-4" />
                            添加
                        </button>
                    )}
                </div>
            </div>

            {editingId ? (
                <div className="p-6 bg-zinc-50 dark:bg-zinc-800/50 rounded-xl border border-zinc-200 dark:border-zinc-700 space-y-4">
                    <h4 className="text-sm font-medium text-zinc-900 dark:text-zinc-100 mb-4">
                        {editingId === '__new__' ? '添加新的 MCP 服务' : '编辑 MCP 服务'}
                    </h4>
                    
                    <div className="space-y-4">
                        <div className="space-y-2">
                            <label className="text-xs font-medium text-zinc-500 dark:text-zinc-400">服务 ID (唯一标识)</label>
                            <input
                                type="text"
                                value={editForm.id}
                                onChange={(e) => setEditForm({...editForm, id: e.target.value})}
                                placeholder="例如: local-python-tools"
                                disabled={editingId !== '__new__'}
                                className="w-full px-3 py-2 rounded-lg border border-zinc-300 dark:border-zinc-600 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 text-sm outline-none focus:ring-2 focus:ring-blue-500/20 disabled:opacity-50"
                            />
                        </div>

                        <div className="space-y-2">
                            <label className="text-xs font-medium text-zinc-500 dark:text-zinc-400">连接类型</label>
                            <select
                                value={editForm.type}
                                onChange={(e) => setEditForm({...editForm, type: e.target.value as 'stdio'|'sse'})}
                                className="w-full px-3 py-2 rounded-lg border border-zinc-300 dark:border-zinc-600 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 text-sm outline-none focus:ring-2 focus:ring-blue-500/20"
                            >
                                <option value="stdio">标准输入输出 (stdio) - 本地程序</option>
                                <option value="sse">Server-Sent Events (sse) - 远程服务</option>
                            </select>
                        </div>

                        {editForm.type === 'stdio' ? (
                            <>
                                <div className="space-y-2">
                                    <label className="text-xs font-medium text-zinc-500 dark:text-zinc-400">执行命令 (Command)</label>
                                    <input
                                        type="text"
                                        value={editForm.command}
                                        onChange={(e) => setEditForm({...editForm, command: e.target.value})}
                                        placeholder="例如: python, node, npx"
                                        className="w-full px-3 py-2 rounded-lg border border-zinc-300 dark:border-zinc-600 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 text-sm outline-none focus:ring-2 focus:ring-blue-500/20"
                                    />
                                </div>
                                <div className="space-y-2">
                                    <label className="text-xs font-medium text-zinc-500 dark:text-zinc-400">运行参数 (Args, 逗号分隔)</label>
                                    <input
                                        type="text"
                                        value={editForm.args?.join(', ')}
                                        onChange={(e) => setEditForm({...editForm, args: e.target.value.split(',').map(s=>s.trim()).filter(Boolean)})}
                                        placeholder="例如: server.py, --port, 8080"
                                        className="w-full px-3 py-2 rounded-lg border border-zinc-300 dark:border-zinc-600 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 text-sm outline-none focus:ring-2 focus:ring-blue-500/20"
                                    />
                                </div>
                                <div className="space-y-2">
                                    <label className="text-xs font-medium text-zinc-500 dark:text-zinc-400">环境变量 (Env, 逗号分隔的 KEY=VALUE)</label>
                                    <input
                                        type="text"
                                        value={editForm.env?.join(', ')}
                                        onChange={(e) => setEditForm({...editForm, env: e.target.value.split(',').map(s=>s.trim()).filter(Boolean)})}
                                        placeholder="例如: API_KEY=123, ENV=prod"
                                        className="w-full px-3 py-2 rounded-lg border border-zinc-300 dark:border-zinc-600 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 text-sm outline-none focus:ring-2 focus:ring-blue-500/20"
                                    />
                                </div>
                            </>
                        ) : (
                            <div className="space-y-2">
                                <label className="text-xs font-medium text-zinc-500 dark:text-zinc-400">服务地址 (URL)</label>
                                <input
                                    type="text"
                                    value={editForm.url}
                                    onChange={(e) => setEditForm({...editForm, url: e.target.value})}
                                    placeholder="例如: http://localhost:8080/mcp"
                                    className="w-full px-3 py-2 rounded-lg border border-zinc-300 dark:border-zinc-600 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 text-sm outline-none focus:ring-2 focus:ring-blue-500/20"
                                />
                            </div>
                        )}

                        <div className="flex justify-end gap-3 pt-4 border-t border-zinc-200 dark:border-zinc-700">
                            <button
                                onClick={() => setEditingId(null)}
                                className="px-4 py-2 text-sm font-medium text-zinc-600 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded-lg transition-colors"
                            >
                                取消
                            </button>
                            <button
                                onClick={saveEdit}
                                disabled={isSaving}
                                className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white text-sm font-medium rounded-lg transition-colors disabled:opacity-50"
                            >
                                {isSaving ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />}
                                保存并重启
                            </button>
                        </div>
                    </div>
                </div>
            ) : (
                <>
                    {/* Server List */}
                    <div className="border border-zinc-200 dark:border-zinc-800 rounded-xl overflow-hidden divide-y divide-zinc-200 dark:divide-zinc-800">
                        {Object.keys(servers).length === 0 ? (
                            <div className="p-8 text-center bg-zinc-50 dark:bg-zinc-800/30">
                                <Terminal className="w-8 h-8 text-zinc-400 mx-auto mb-3" />
                                <p className="text-sm text-zinc-500">暂未配置任何 MCP 服务</p>
                            </div>
                        ) : (
                            Object.entries(servers).map(([id, srv]) => (
                                <div key={id} className="flex items-center justify-between p-4 bg-zinc-50 dark:bg-zinc-800/30 hover:bg-zinc-100 dark:hover:bg-zinc-800/50 transition-colors">
                                    <div className="flex items-center gap-4">
                                        <div className={cn(
                                            "w-8 h-8 rounded-lg flex items-center justify-center font-bold text-white shadow-sm text-sm",
                                            srv.disabled ? "bg-zinc-400 dark:bg-zinc-600" : "bg-gradient-to-br from-purple-400 to-purple-600"
                                        )}>
                                            {id.charAt(0).toUpperCase()}
                                        </div>
                                        <div>
                                            <h4 className="text-sm font-medium text-zinc-900 dark:text-zinc-100 flex items-center gap-2">
                                                {id}
                                                {!srv.disabled && <CheckCircle2 className="w-3.5 h-3.5 text-green-500" />}
                                            </h4>
                                        </div>
                                    </div>
                                    <div className="flex items-center gap-3">
                                        <button
                                            onClick={() => handleDelete(id)}
                                            className="p-1.5 text-zinc-400 hover:text-red-500 hover:bg-zinc-200 dark:hover:bg-zinc-700 rounded-md transition-colors"
                                            title="删除"
                                        >
                                            <Trash2 className="w-4 h-4" />
                                        </button>
                                        
                                        <button
                                            onClick={() => startEdit(id)}
                                            className="p-1.5 text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-200 dark:hover:bg-zinc-700 rounded-md transition-colors"
                                            title="设置"
                                        >
                                            <Settings className="w-4 h-4" />
                                        </button>

                                        {/* Toggle Switch */}
                                        <button
                                            onClick={() => handleToggleEnable(id)}
                                            className={cn(
                                                "relative inline-flex h-6 w-11 items-center rounded-full transition-colors focus:outline-none",
                                                srv.disabled ? "bg-zinc-300 dark:bg-zinc-700" : "bg-green-500"
                                            )}
                                        >
                                            <span
                                                className={cn(
                                                    "inline-block h-5 w-5 transform rounded-full bg-white transition-transform shadow-sm",
                                                    srv.disabled ? "translate-x-0.5" : "translate-x-5"
                                                )}
                                            />
                                        </button>
                                    </div>
                                </div>
                            ))
                        )}
                    </div>
                </>
            )}
            {/* JSON Add Modal */}
            {isJsonModalOpen && (
                <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/50 backdrop-blur-sm animate-in fade-in duration-200">
                    <div className="w-full max-w-2xl bg-zinc-900 rounded-xl shadow-2xl border border-zinc-800 overflow-hidden flex flex-col">
                        <div className="flex items-center justify-between p-4 border-b border-zinc-800">
                            <h3 className="text-sm font-medium text-zinc-100 flex items-center gap-2">
                                <Terminal className="w-4 h-4 text-blue-400" />
                                添加 MCP Server 配置 (JSON)
                            </h3>
                            <button 
                                onClick={() => setIsJsonModalOpen(false)}
                                className="text-zinc-400 hover:text-zinc-100 transition-colors"
                            >
                                <X className="w-4 h-4" />
                            </button>
                        </div>
                        <div className="p-4 space-y-3 bg-zinc-950">
                            <p className="text-xs text-zinc-400">
                                请从 MCP Servers 的介绍页面复制配置 JSON，并粘贴到输入框中。
                            </p>
                            <textarea
                                value={jsonInput}
                                onChange={(e) => setJsonInput(e.target.value)}
                                className="w-full h-64 p-4 font-mono text-sm bg-zinc-900 text-zinc-300 border border-zinc-800 rounded-lg outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500/50 resize-none"
                                spellCheck={false}
                            />
                        </div>
                        <div className="flex items-center justify-between p-4 border-t border-zinc-800 bg-zinc-900">
                            <p className="text-xs text-yellow-500 flex items-center gap-1">
                                ⚠️ 配置前请确认来源，甄别风险
                            </p>
                            <div className="flex gap-3">
                                <button
                                    onClick={() => setIsJsonModalOpen(false)}
                                    className="px-4 py-2 text-sm font-medium text-zinc-400 hover:text-zinc-100 hover:bg-zinc-800 rounded-lg transition-colors"
                                >
                                    取消
                                </button>
                                <button
                                    onClick={saveJsonEdit}
                                    className="px-4 py-2 text-sm font-medium text-zinc-900 bg-white hover:bg-zinc-200 rounded-lg transition-colors"
                                >
                                    确认
                                </button>
                            </div>
                        </div>
                    </div>
                </div>
            )}
        </div>
    );
};

import React, { useState, useEffect } from 'react';
import { Eye, EyeOff, Save, Loader2, RefreshCw, Server } from 'lucide-react';
import { toast } from 'sonner';
import { waitForBackendRecovery } from '../../lib/backendRestart.ts';
import { cn } from '../../lib/utils';

export const ConfigView: React.FC = () => {
    // Main Model State
    const [mainProvider, setMainProvider] = useState('');
    const [mainModel, setMainModel] = useState('');
    const [mainApiKey, setMainApiKey] = useState('');
    const [mainBaseUrl, setMainBaseUrl] = useState('');
    const [mainMaxTokens, setMainMaxTokens] = useState<number | ''>('');
    const [isMainKeyVisible, setIsMainKeyVisible] = useState(false);

    const [isLoading, setIsLoading] = useState(false);
    const [isSaving, setIsSaving] = useState(false);
    const [isRestarting, setIsRestarting] = useState(false);
    const [restartStatusText, setRestartStatusText] = useState('配置已更新，正在等待服务重启完成...');

    useEffect(() => {
        fetchConfig();
    }, []);

    const fetchConfig = async () => {
        setIsLoading(true);
        try {
            const res = await fetch('/v1/config');
            if (res.ok) {
                const data = await res.json();
                if (data.main_model) {
                    setMainProvider(data.main_model.provider || '');
                    setMainModel(data.main_model.model || '');
                    setMainMaxTokens(data.main_model.maxTokens || '');

                    setMainApiKey(data.main_model.apiKey || '');
                    setMainBaseUrl(data.main_model.baseURL || '');
                } else {
                    setMainProvider('');
                    setMainModel('');
                    setMainApiKey('');
                    setMainBaseUrl('');
                    setMainMaxTokens('');
                }
            } else {
                toast.error('Failed to fetch configuration');
            }
        } catch (error) {
            console.error('Error fetching config:', error);
            toast.error('Failed to connect to backend');
        } finally {
            setIsLoading(false);
        }
    };

    const handleSaveAndRestart = async () => {
        setIsSaving(true);
        try {
            const res = await fetch('/v1/config', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({
                    main_model: {
                        provider: mainProvider,
                        model: mainModel.trim(),
                        api_key: mainProvider === 'ollama' ? '' : mainApiKey.trim(),
                        base_url: mainBaseUrl.trim(),
                        max_tokens: mainMaxTokens ? Number(mainMaxTokens) : -1
                    }
                }),
            });

            if (res.ok) {
                const data = await res.json();
                if (data.needs_restart === 'true') {
                    toast.success('配置已保存，正在重启服务...');
                    setIsRestarting(true);
                    setRestartStatusText('配置已更新，正在等待服务重启完成...');

                    try {
                        const result = await window.electronAPI?.restartBackend?.();
                        if (result?.ok) {
                            await waitForBackendRecovery();
                            setRestartStatusText('服务已恢复，正在重新加载最新配置...');
                            await fetchConfig();
                            toast.success('服务已重启，当前页面已自动恢复');
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
                    toast.success('配置已保存');
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

    if (isLoading) {
        return (
            <div className="flex items-center justify-center h-full">
                <Loader2 className="w-6 h-6 animate-spin text-zinc-400" />
            </div>
        );
    }

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

    return (
        <div className="space-y-6 animate-in fade-in duration-300">
            <div className="space-y-1">
                <h3 className="text-lg font-medium text-zinc-900 dark:text-zinc-100 flex items-center gap-2">
                    <Server className="w-5 h-5" />
                    自定义模型配置
                </h3>
                <p className="text-sm text-zinc-500 dark:text-zinc-400">
                    配置写入 backend/.auramate.json 的 main_model 节点。修改后需要重启服务才能生效。
                </p>
            </div>

            <div className="p-6 bg-zinc-50 dark:bg-zinc-800/50 rounded-xl border border-zinc-200 dark:border-zinc-700 space-y-6">
                
                {/* Main Model Config Section */}
                <div>
                    <h4 className="text-sm font-medium text-zinc-900 dark:text-zinc-100 mb-4">自定义模型配置 (main_model)</h4>
                    <p className="text-xs text-zinc-500 dark:text-zinc-400 mb-4">
                        此配置将写入 provider、model、apiKey、baseURL、maxTokens，并应用于所有文本类 Agent。
                    </p>
                        <div className="space-y-4">
                            <div className="space-y-2">
                                <label className="text-xs font-medium text-zinc-500 dark:text-zinc-400">模型服务商 (Provider)</label>
                                <select 
                                    value={mainProvider}
                                    onChange={(e) => setMainProvider(e.target.value)}
                                    className="w-full px-3 py-2 rounded-lg border border-zinc-300 dark:border-zinc-600 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 text-sm outline-none focus:ring-2 focus:ring-blue-500/20 focus:border-blue-500"
                                >
                                    <option value="">自动 / 留空</option>
                                    <option value="openai">OpenAI 兼容接口</option>
                                    <option value="custom">自定义兼容接口</option>
                                    <option value="ollama">Ollama (本地)</option>
                                </select>
                            </div>

                            <div className="space-y-2">
                                <label className="text-xs font-medium text-zinc-500 dark:text-zinc-400">模型名称 (Model Name)</label>
                                <input
                                    type="text"
                                    value={mainModel}
                                    onChange={(e) => setMainModel(e.target.value)}
                                    placeholder={mainProvider === 'ollama' ? '例如: qwen3:latest, llama3.1:8b' : '例如: deepseek-v4-flash, glm-5.1, minimax-m2.7'}
                                    className="w-full px-3 py-2 rounded-lg border border-zinc-300 dark:border-zinc-600 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 placeholder:text-zinc-400 text-sm outline-none focus:ring-2 focus:ring-blue-500/20 focus:border-blue-500"
                                />
                            </div>
                            <div className="space-y-2">
                                <label className="text-xs font-medium text-zinc-500 dark:text-zinc-400">接口地址 (Base URL)</label>
                                <input
                                    type="text"
                                    value={mainBaseUrl}
                                    onChange={(e) => setMainBaseUrl(e.target.value)}
                                    placeholder={mainProvider === 'ollama' ? '例如: http://localhost:11434/v1' : '例如: https://api.openai.com/v1 或认证代理地址'}
                                    className="w-full px-3 py-2 rounded-lg border border-zinc-300 dark:border-zinc-600 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 placeholder:text-zinc-400 text-sm outline-none focus:ring-2 focus:ring-blue-500/20 focus:border-blue-500"
                                />
                            </div>

                            {mainProvider !== 'ollama' && (
                                <div className="space-y-2">
                                    <label className="text-xs font-medium text-zinc-500 dark:text-zinc-400">API 密钥 (API Key)</label>
                                    <div className="relative">
                                        <input
                                            type={isMainKeyVisible ? "text" : "password"}
                                            value={mainApiKey}
                                            onChange={(e) => setMainApiKey(e.target.value)}
                                            placeholder="sk-..."
                                            className="w-full pl-3 pr-10 py-2 rounded-lg border border-zinc-300 dark:border-zinc-600 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 placeholder:text-zinc-400 text-sm outline-none focus:ring-2 focus:ring-blue-500/20 focus:border-blue-500"
                                        />
                                        <button
                                            type="button"
                                            onClick={() => setIsMainKeyVisible(!isMainKeyVisible)}
                                            className="absolute right-3 top-1/2 -translate-y-1/2 text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200"
                                        >
                                            {isMainKeyVisible ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
                                        </button>
                                    </div>
                                </div>
                            )}

                            <div className="space-y-2">
                                <label className="text-xs font-medium text-zinc-500 dark:text-zinc-400">上下文窗口 (Context Window / Max Tokens) - 可选</label>
                                <select
                                    value={mainMaxTokens}
                                    onChange={(e) => setMainMaxTokens(e.target.value ? Number(e.target.value) : '')}
                                    className="w-full px-3 py-2 rounded-lg border border-zinc-300 dark:border-zinc-600 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 text-sm outline-none focus:ring-2 focus:ring-blue-500/20 focus:border-blue-500"
                                >
                                    <option value="">自动检测 / 系统默认 (256K)</option>
                                    <option value="128000">128K (基础档位)</option>
                                    <option value="256000">256K (标准档位)</option>
                                    <option value="1000000">1M+ (顶级长上下文档位)</option>
                                </select>
                                <p className="text-xs text-zinc-500">该值会写入 main_model.maxTokens，用于控制自定义模型的上下文窗口上限。</p>
                            </div>
                        </div>
                </div>

                <div className="pt-4 flex justify-end">
                    <button
                        onClick={handleSaveAndRestart}
                        disabled={isSaving}
                        className={cn(
                            "flex items-center gap-2 px-4 py-2 rounded-lg text-sm font-medium transition-all shadow-sm",
                            isSaving
                                ? "bg-zinc-100 text-zinc-400 dark:bg-zinc-800 dark:text-zinc-600 cursor-not-allowed"
                                : "bg-blue-600 text-white hover:bg-blue-700 hover:shadow-md active:scale-95"
                        )}
                    >
                        {isSaving ? (
                            <>
                                <Loader2 className="w-4 h-4 animate-spin" />
                                保存中...
                            </>
                        ) : (
                            <>
                                <Save className="w-4 h-4" />
                                保存并重启
                            </>
                        )}
                    </button>
                </div>
            </div>
        </div>
    );
};

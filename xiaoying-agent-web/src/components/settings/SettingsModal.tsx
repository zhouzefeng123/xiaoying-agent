import React, { useState, useEffect, useMemo } from 'react';
import { X, Save, Monitor, Brain, Terminal, Server, LogOut, User, GitBranch } from 'lucide-react';
import { cn } from '../../lib/utils';
import { MemoriesView } from './MemoriesView';
import { TasksView } from './TasksView';
import { SkillsView } from './SkillsView';
import { ConfigView } from './ConfigView';
import { McpConfigView } from './McpConfigView';
import { toast } from 'sonner';
import { useTranslation } from 'react-i18next';
import { useAuth } from '../../contexts/useAuth';

interface SettingsModalProps {
    isOpen: boolean;
    onClose: () => void;
    initialTab?: 'general' | 'memories' | 'tools' | 'tasks' | 'config' | 'mcp';
    enableSubagents: boolean;
    onEnableSubagentsChange: (enabled: boolean) => void;
}

interface SettingsState {
    language: 'zh' | 'en';
    enableSubagents: boolean;
}

const DEFAULT_SETTINGS: SettingsState = {
    language: 'zh',
    enableSubagents: true,
};

function isMembershipActive(expireAt?: number): boolean {
    if (!expireAt) {
        return false;
    }

    return expireAt > Date.now() / 1000;
}

function formatExpireTime(expireAt?: number): string {
    if (!expireAt || !isMembershipActive(expireAt)) {
        return '未开通';
    }

    return new Date(expireAt * 1000).toLocaleString('zh-CN', {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
    });
}

export const SettingsModal: React.FC<SettingsModalProps> = ({
    isOpen,
    onClose,
    initialTab = 'general',
    enableSubagents,
    onEnableSubagentsChange,
}) => {
    const { t } = useTranslation();
    const { user, logout, refreshUser } = useAuth();
    const [activeTab, setActiveTab] = useState<'general' | 'memories' | 'tasks' | 'tools' | 'config' | 'mcp'>(initialTab);
    const [settings, setSettings] = useState<SettingsState>(DEFAULT_SETTINGS);

    const membership = useMemo(() => {
        const expireAt = user?.member?.expireAt;
        const active = isMembershipActive(expireAt);
        const isPaidMember = active && user?.member?.actualPaid === true;
        const isTrialMember = active && user?.member?.actualPaid === false;

        if (isPaidMember) {
            return {
                label: 'Pro 会员',
                detail: `有效期至 ${formatExpireTime(expireAt)}`,
                className: 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300',
            };
        }

        if (isTrialMember) {
            return {
                label: '试用会员',
                detail: `有效期至 ${formatExpireTime(expireAt)}`,
                className: 'bg-sky-100 text-sky-700 dark:bg-sky-900/30 dark:text-sky-300',
            };
        }

        if (user?.memberUsable) {
            return {
                label: '会员可用',
                detail: formatExpireTime(expireAt),
                className: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300',
            };
        }

        return {
            label: '普通用户',
            detail: '当前未开通会员',
            className: 'bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300',
        };
    }, [user]);

    // Load settings from localStorage on mount
    useEffect(() => {
        const saved = localStorage.getItem('opencode_settings');
        if (saved) {
            try {
                setSettings({ ...DEFAULT_SETTINGS, ...JSON.parse(saved) });
            } catch (e) {
                console.error('Failed to parse settings', e);
            }
        }
        
        // Fetch current config from backend
        const fetchConfig = async () => {
            try {
                const res = await fetch('/v1/config');
                if (res.ok) {
                    await res.json();
                }
            } catch (e) {
                console.error('Failed to fetch config', e);
            }
        };
        fetchConfig();

    }, []);

    useEffect(() => {
        if (!isOpen) {
            return;
        }

        setSettings((current) => ({ ...current, enableSubagents }));
    }, [enableSubagents, isOpen]);

    useEffect(() => {
        if (!isOpen || activeTab !== 'general' || !user) {
            return;
        }

        void refreshUser();
    }, [activeTab, isOpen, refreshUser, user]);

    const handleSave = async () => {
        localStorage.setItem('opencode_settings', JSON.stringify(settings));
        onEnableSubagentsChange(settings.enableSubagents);
        toast.success(t('settings.saved'));
        onClose();
    };

    if (!isOpen) return null;

    return (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm animate-in fade-in duration-200">
            <div className="w-full max-w-2xl bg-white dark:bg-zinc-900 rounded-xl shadow-2xl border border-zinc-200 dark:border-zinc-800 overflow-hidden flex flex-col max-h-[85vh]">
                
                {/* Header */}
                <div className="h-14 flex items-center justify-between px-6 border-b border-zinc-200 dark:border-zinc-800 bg-zinc-50 dark:bg-zinc-900">
                    <h2 className="font-semibold text-lg">{t('settings.title')}</h2>
                    <button 
                        onClick={onClose}
                        className="p-1.5 hover:bg-zinc-200 dark:hover:bg-zinc-800 rounded-md text-zinc-500 transition-colors"
                    >
                        <X className="w-5 h-5" />
                    </button>
                </div>

                <div className="flex flex-1 overflow-hidden">
                    {/* Sidebar Tabs */}
                    <div className="w-48 border-r border-zinc-200 dark:border-zinc-800 bg-zinc-50/50 dark:bg-zinc-900/50 p-4 space-y-2">
                        <button
                            onClick={() => setActiveTab('general')}
                            className={cn(
                                "w-full flex items-center gap-3 p-2 rounded-lg text-sm font-medium transition-colors",
                                activeTab === 'general' 
                                    ? "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300" 
                                    : "text-zinc-600 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800"
                            )}
                        >
                            <Monitor className="w-4 h-4" />
                            {t('settings.general')}
                        </button>
                        <button
                            onClick={() => setActiveTab('memories')}
                            className={cn(
                                "w-full flex items-center gap-3 p-2 rounded-lg text-sm font-medium transition-colors",
                                activeTab === 'memories' 
                                    ? "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300" 
                                    : "text-zinc-600 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800"
                            )}
                        >
                            <Brain className="w-4 h-4" />
                            {t('settings.memories')}
                        </button>
                        <button
                            onClick={() => setActiveTab('tasks')}
                            className={cn(
                                "w-full flex items-center gap-3 p-2 rounded-lg text-sm font-medium transition-colors",
                                activeTab === 'tasks' 
                                    ? "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300" 
                                    : "text-zinc-600 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800"
                            )}
                        >
                            <Monitor className="w-4 h-4" />
                            {t('settings.tasks')}
                        </button>
                        <button
                            onClick={() => setActiveTab('tools')}
                            className={cn(
                                "w-full flex items-center gap-3 p-2 rounded-lg text-sm font-medium transition-colors",
                                activeTab === 'tools' 
                                    ? "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300" 
                                    : "text-zinc-600 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800"
                            )}
                        >
                            <Terminal className="w-4 h-4" />
                            {t('settings.skills')}
                        </button>
                        <button
                            onClick={() => setActiveTab('config')}
                            className={cn(
                                "w-full flex items-center gap-3 p-2 rounded-lg text-sm font-medium transition-colors",
                                activeTab === 'config' 
                                    ? "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300" 
                                    : "text-zinc-600 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800"
                            )}
                        >
                            <Server className="w-4 h-4" />
                            自定义模型配置
                        </button>
                        <button
                            onClick={() => setActiveTab('mcp')}
                            className={cn(
                                "w-full flex items-center gap-3 p-2 rounded-lg text-sm font-medium transition-colors",
                                activeTab === 'mcp' 
                                    ? "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300" 
                                    : "text-zinc-600 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800"
                            )}
                        >
                            <Terminal className="w-4 h-4" />
                            MCP 配置
                        </button>
                    </div>

                    {/* Content */}
                    <div className="flex-1 p-6 overflow-y-auto">
                        {activeTab === 'config' && (
                            <ConfigView />
                        )}

                        {activeTab === 'mcp' && (
                            <McpConfigView />
                        )}

                        {activeTab === 'general' && (
                            <div className="space-y-6">
                                <div className="space-y-2">
                                    <label className="text-sm font-medium text-zinc-900 dark:text-zinc-100">
                                        {t('settings.language')}
                                    </label>
                                    <select
                                        value={settings.language}
                                        onChange={(e) => setSettings(s => ({ ...s, language: e.target.value as 'zh' | 'en' }))}

                                        className="w-full px-3 py-2 rounded-lg border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 text-sm focus:ring-2 focus:ring-blue-500/20 focus:border-blue-500 outline-none"
                                    >
                                        <option value="zh">简体中文 (Chinese)</option>
                                        <option value="en">English</option>
                                    </select>
                                </div>

                                <div className="space-y-3 rounded-lg border border-zinc-200 bg-zinc-50 p-4 dark:border-zinc-800 dark:bg-zinc-800/40">
                                    <div className="flex items-start justify-between gap-4">
                                        <div className="space-y-1">
                                            <div className="flex items-center gap-2 text-sm font-medium text-zinc-900 dark:text-zinc-100">
                                                <GitBranch className="h-4 w-4" />
                                                子 Agent 协作
                                            </div>
                                            <p className="text-xs leading-5 text-zinc-500 dark:text-zinc-400">
                                                关闭后仅使用当前主 Agent 执行。
                                            </p>
                                        </div>
                                        <button
                                            type="button"
                                            onClick={() => setSettings((current) => ({ ...current, enableSubagents: !current.enableSubagents }))}
                                            className={cn(
                                                'relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors',
                                                settings.enableSubagents
                                                    ? 'bg-emerald-500'
                                                    : 'bg-zinc-300 dark:bg-zinc-600'
                                            )}
                                            aria-pressed={settings.enableSubagents}
                                            title={settings.enableSubagents ? '已开启子 Agent 协作' : '已关闭子 Agent 协作'}
                                        >
                                            <span
                                                className={cn(
                                                    'inline-block h-5 w-5 rounded-full bg-white shadow-sm transition-transform',
                                                    settings.enableSubagents ? 'translate-x-5' : 'translate-x-1'
                                                )}
                                            />
                                        </button>
                                    </div>
                                    <div className="text-xs font-medium text-zinc-600 dark:text-zinc-300">
                                        当前状态：{settings.enableSubagents ? '开启' : '关闭'}
                                    </div>
                                </div>

                                <div className="pt-4 border-t border-zinc-200 dark:border-zinc-800">
                                    <h3 className="text-sm font-medium text-zinc-900 dark:text-zinc-100 mb-4 flex items-center gap-2">
                                        <User className="w-4 h-4" />
                                        账号信息
                                    </h3>
                                    
                                    <div className="bg-zinc-50 dark:bg-zinc-800/50 rounded-lg p-4 flex items-center justify-between border border-zinc-200 dark:border-zinc-700">
                                        <div className="flex items-center gap-3">
                                            <div className="h-10 w-10 rounded-full bg-purple-100 dark:bg-purple-900/30 flex items-center justify-center text-purple-600 dark:text-purple-400 font-semibold">
                                                {user?.username?.[0]?.toUpperCase() || 'U'}
                                            </div>
                                            <div>
                                                <div className="font-medium text-sm text-zinc-900 dark:text-zinc-100">
                                                    {user?.username || '未登录'}
                                                </div>
                                                <div className="text-xs text-zinc-500 dark:text-zinc-400">
                                                    {user?.email || ''}
                                                </div>
                                                <div className="mt-2 flex items-center gap-2">
                                                    <span className={cn('inline-flex rounded-full px-2 py-0.5 text-xs font-medium', membership.className)}>
                                                        {membership.label}
                                                    </span>
                                                    <span className="text-xs text-zinc-500 dark:text-zinc-400">
                                                        {membership.detail}
                                                    </span>
                                                </div>
                                            </div>
                                        </div>
                                        
                                        <button
                                            onClick={() => {
                                                logout();
                                                onClose();
                                            }}
                                            className="px-3 py-1.5 text-xs font-medium text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20 rounded-md transition-colors flex items-center gap-1.5"
                                        >
                                            <LogOut className="w-3.5 h-3.5" />
                                            退出登录
                                        </button>
                                    </div>
                                </div>
                            </div>
                        )}

                        {activeTab === 'memories' && (
                            <MemoriesView />
                        )}

                        {activeTab === 'tasks' && (
                            <TasksView />
                        )}

                        {activeTab === 'tools' && (
                            <SkillsView />
                        )}
                    </div>
                </div>

                {/* Footer */}
                {activeTab !== 'config' && activeTab !== 'mcp' && activeTab !== 'tools' && (
                <div className="p-4 border-t border-zinc-200 dark:border-zinc-800 bg-zinc-50 dark:bg-zinc-900 flex justify-end gap-3">
                    <button
                        onClick={onClose}
                        className="px-4 py-2 rounded-lg text-sm font-medium text-zinc-600 hover:bg-zinc-200 dark:text-zinc-300 dark:hover:bg-zinc-800 transition-colors"
                    >
                        {t('settings.cancel')}
                    </button>
                    <button
                        onClick={handleSave}
                        className="px-4 py-2 rounded-lg text-sm font-medium bg-blue-500 text-white hover:bg-blue-600 transition-colors flex items-center gap-2"
                    >
                        <Save className="w-4 h-4" />
                        {t('settings.save')}
                    </button>
                </div>
                )}
            </div>
        </div>
    );
};

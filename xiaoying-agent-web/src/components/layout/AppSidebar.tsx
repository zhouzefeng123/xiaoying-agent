import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
    FolderOpen,
    FolderPlus,
    Loader2,
    MessageSquare,
    MessageSquarePlus,
    RefreshCw,
    X,
    Blocks,
    Compass,
} from 'lucide-react';
import { toast } from 'sonner';
import { sessionService, type Session } from '../../services/sessionService';
import { PlanProgressCard } from '../chat/MessageBubble';
import type { PlanProgressSnapshot } from '../../lib/planProgress';
import { Logo } from '../common/Logo';

const MAX_VISIBLE_SESSIONS = 8;

interface AppSidebarProps {
    projectName: string;
    canRemoveProject: boolean;
    refreshKey: number;
    onNewConversation: () => void;
    onCreateBlankProject: () => void | Promise<void>;
    onOpenProjectFolder: () => void | Promise<void>;
    onRemoveProject: () => void | Promise<void>;
    onUseExistingFolder: () => void | Promise<void>;
    onSelectSession: (sessionId: string) => void | Promise<void>;
    onExploreWorkflows: () => void;
    onOpenSkills: () => void;
    planProgressSnapshot?: PlanProgressSnapshot | null;
}

const toMillis = (value?: number) => {
    if (!value) return 0;
    return value > 10_000_000_000 ? value : value * 1000;
};

const formatRelativeTime = (value?: number) => {
    const millis = toMillis(value);
    if (!millis) return '';

    const diff = Math.max(0, Date.now() - millis);
    const minute = 60_000;
    const hour = 60 * minute;
    const day = 24 * hour;

    if (diff < minute) return '刚刚';
    if (diff < hour) return `${Math.floor(diff / minute)} 分钟`;
    if (diff < day) return `${Math.floor(diff / hour)} 小时`;
    if (diff < 7 * day) return `${Math.floor(diff / day)} 天`;

    return new Date(millis).toLocaleDateString('zh-CN', {
        month: 'numeric',
        day: 'numeric',
    });
};

const getSessionTitle = (session: Session) => {
    const title = session.Title?.trim();
    if (title) return title;

    return '未命名对话';
};

export function AppSidebar({
    projectName,
    canRemoveProject,
    refreshKey,
    onNewConversation,
    onCreateBlankProject,
    onOpenProjectFolder,
    onRemoveProject,
    onUseExistingFolder,
    onSelectSession,
    onExploreWorkflows,
    onOpenSkills,
    planProgressSnapshot,
}: AppSidebarProps) {
    const [sessions, setSessions] = useState<Session[]>([]);
    const [loading, setLoading] = useState(false);
    const [selectedSessionId, setSelectedSessionId] = useState<string | null>(null);
    const [projectMenuOpen, setProjectMenuOpen] = useState(false);
    const projectMenuRef = useRef<HTMLDivElement | null>(null);

    const loadSessions = useCallback(async (silent = false) => {
        if (!silent) setLoading(true);

        try {
            const data = await sessionService.list(8000);
            data.sort((a, b) => (b.UpdatedAt || 0) - (a.UpdatedAt || 0));
            setSessions(data);
        } catch (error) {
            console.error('Failed to load sidebar sessions:', error);
            if (!silent) toast.error('无法加载历史对话');
        } finally {
            if (!silent) setLoading(false);
        }
    }, []);

    useEffect(() => {
        void loadSessions(true);
    }, [loadSessions, refreshKey]);

    useEffect(() => {
        if (!projectMenuOpen) return;

        const handlePointerDown = (event: MouseEvent) => {
            if (!projectMenuRef.current?.contains(event.target as Node)) {
                setProjectMenuOpen(false);
            }
        };

        document.addEventListener('mousedown', handlePointerDown);
        return () => document.removeEventListener('mousedown', handlePointerDown);
    }, [projectMenuOpen]);

    const visibleSessions = useMemo(
        () => sessions.slice(0, MAX_VISIBLE_SESSIONS),
        [sessions],
    );

    const handleSelectSession = async (sessionId: string) => {
        if (selectedSessionId) return;

        setSelectedSessionId(sessionId);
        try {
            await Promise.resolve(onSelectSession(sessionId));
        } catch (error) {
            console.error('Failed to open sidebar session:', error);
            toast.error('打开历史对话失败');
        } finally {
            setSelectedSessionId(null);
        }
    };

    const handleProjectAction = async (action: () => void | Promise<void>) => {
        setProjectMenuOpen(false);
        await Promise.resolve(action());
    };

    return (
        <aside className="flex h-full w-full shrink-0 flex-col overflow-hidden border-r border-zinc-200 bg-zinc-50/90 text-zinc-700 dark:border-zinc-800 dark:bg-zinc-950/95 dark:text-zinc-300">
            <div className="min-h-0 flex-1 overflow-hidden px-2 py-3">
                <div className="xiaoying-sidebar-brand">
                    <Logo className="h-8 w-8 rounded-[10px]" />
                    <div className="min-w-0">
                        <div className="text-[15px] font-bold tracking-[0.12em] text-zinc-900 dark:text-zinc-100">XiaoYing</div>
                    </div>
                </div>
                <button
                    onClick={onNewConversation}
                    className="group flex h-11 w-full items-center gap-3 rounded-lg px-3 text-left text-[14px] font-medium text-zinc-800 transition-colors hover:bg-zinc-200/70 dark:text-zinc-100 dark:hover:bg-zinc-800"
                    title="新建对话"
                >
                    <MessageSquarePlus className="h-5 w-5 shrink-0 text-zinc-600 dark:text-zinc-400" />
                    <span className="min-w-0 flex-1 truncate">新对话</span>
                    <kbd className="hidden shrink-0 rounded-md bg-zinc-200 px-1.5 py-0.5 text-[11px] font-medium text-zinc-600 group-hover:inline dark:bg-zinc-800 dark:text-zinc-400">
                        Ctrl+Alt+N
                    </kbd>
                </button>

                <div className="xiaoying-sidebar-nav">
                    <button type="button" className="xiaoying-sidebar-nav-item" onClick={onExploreWorkflows}>
                        <Compass className="h-4 w-4" />
                        <span>探索工作流</span>
                    </button>
                    <button type="button" className="xiaoying-sidebar-nav-item" onClick={onOpenSkills}>
                        <Blocks className="h-4 w-4" />
                        <span>专家 · 技能 · 连接器</span>
                    </button>
                </div>

                <div className="mt-7">
                    <div className="relative mb-2 flex items-center justify-between px-2" ref={projectMenuRef}>
                        <div className="text-[13px] font-medium text-zinc-400">项目</div>
                        <button
                            onClick={() => setProjectMenuOpen((open) => !open)}
                            className="rounded-md p-1.5 text-zinc-400 transition-colors hover:bg-zinc-200/70 hover:text-zinc-700 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
                            title="项目操作"
                        >
                            <FolderPlus className="h-4 w-4" />
                        </button>

                        {projectMenuOpen && (
                            <div className="absolute right-0 top-8 z-50 w-56 overflow-hidden rounded-xl border border-zinc-200 bg-white py-1.5 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
                                <button
                                    onClick={() => void handleProjectAction(onCreateBlankProject)}
                                    className="flex w-full items-center gap-3 px-3 py-2.5 text-left text-sm text-zinc-800 transition-colors hover:bg-zinc-100 dark:text-zinc-100 dark:hover:bg-zinc-800"
                                >
                                    <FolderPlus className="h-5 w-5 shrink-0 text-zinc-500" />
                                    <span>新建空白项目</span>
                                </button>
                                <button
                                    onClick={() => void handleProjectAction(onUseExistingFolder)}
                                    className="flex w-full items-center gap-3 px-3 py-2.5 text-left text-sm text-zinc-800 transition-colors hover:bg-zinc-100 dark:text-zinc-100 dark:hover:bg-zinc-800"
                                >
                                    <FolderOpen className="h-5 w-5 shrink-0 text-zinc-500" />
                                    <span>使用现有文件夹</span>
                                </button>
                            </div>
                        )}
                    </div>

                    <div className="flex h-10 w-full items-center gap-2 rounded-lg px-3 text-[14px] text-zinc-700 dark:text-zinc-300">
                        <button
                            onClick={(event) => {
                                event.stopPropagation();
                                void onOpenProjectFolder();
                            }}
                            disabled={!canRemoveProject}
                            className="rounded-md p-1 text-zinc-500 transition-colors hover:bg-zinc-200/70 hover:text-zinc-700 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
                            title={canRemoveProject ? '打开项目文件夹' : '当前没有项目'}
                        >
                            <FolderOpen className="h-4 w-4 shrink-0" />
                        </button>
                        <span className={`min-w-0 flex-1 truncate ${canRemoveProject ? '' : 'text-zinc-400 dark:text-zinc-500'}`}>
                            {projectName}
                        </span>
                        {canRemoveProject && (
                            <button
                                onClick={(event) => {
                                    event.stopPropagation();
                                    void onRemoveProject();
                                }}
                                className="rounded-md p-1 text-zinc-400 transition-colors hover:bg-zinc-200/70 hover:text-zinc-700 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
                                title="移除项目"
                            >
                                <X className="h-4 w-4" />
                            </button>
                        )}
                    </div>
                </div>

                <div className="mt-8">
                    <div className="mb-2 flex items-center justify-between px-2">
                        <div className="text-[13px] font-medium text-zinc-400">对话</div>
                        <button
                            onClick={() => void loadSessions(false)}
                            className="rounded-md p-1.5 text-zinc-400 transition-colors hover:bg-zinc-200/70 hover:text-zinc-700 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
                            title="刷新历史对话"
                        >
                            {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
                        </button>
                    </div>

                    <div className="space-y-1">
                        {visibleSessions.map((session) => {
                            const isOpening = selectedSessionId === session.ID;
                            return (
                                <button
                                    key={session.ID}
                                    onClick={() => void handleSelectSession(session.ID)}
                                    disabled={!!selectedSessionId}
                                    className="flex h-10 w-full items-center gap-2 rounded-lg px-3 text-left text-[13px] text-zinc-700 transition-colors hover:bg-zinc-200/60 disabled:cursor-wait disabled:opacity-70 dark:text-zinc-300 dark:hover:bg-zinc-800/70"
                                    title={getSessionTitle(session)}
                                >
                                    {isOpening ? (
                                        <Loader2 className="h-4 w-4 shrink-0 animate-spin text-zinc-400" />
                                    ) : (
                                        <MessageSquare className="h-4 w-4 shrink-0 text-zinc-400" />
                                    )}
                                    <span className="min-w-0 flex-1 truncate">{getSessionTitle(session)}</span>
                                    <span className="shrink-0 text-xs text-zinc-400">{formatRelativeTime(session.UpdatedAt)}</span>
                                </button>
                            );
                        })}

                        {!loading && visibleSessions.length === 0 && (
                            <div className="px-3 py-6 text-sm text-zinc-400">暂无历史对话</div>
                        )}
                    </div>
                </div>
            </div>
            {planProgressSnapshot && planProgressSnapshot.items.length > 0 && (
                <div className="shrink-0 border-t border-zinc-200/70 px-2 py-3 dark:border-zinc-800/80">
                    <PlanProgressCard snapshot={planProgressSnapshot} />
                </div>
            )}
        </aside>
    );
}

import React, { useState, useEffect, useCallback } from 'react';
import { type Session, sessionService } from '../../services/sessionService';
import { MessageSquare, Calendar, Trash2, FileText, Loader2, X, RotateCcw } from 'lucide-react';
import { toast } from 'sonner';

interface HistoryViewProps {
    isOpen: boolean;
    onClose: () => void;
    onSelectSession: (sessionId: string) => void | Promise<void>;
}

const INITIAL_VISIBLE_COUNT = 50;
const VISIBLE_STEP = 50;
const CACHE_TTL = 30_000;
let cachedSessions: Session[] = [];
let cachedAt = 0;

export function HistoryView({ isOpen, onClose, onSelectSession }: HistoryViewProps) {
    const [sessions, setSessions] = useState<Session[]>([]);
    const [loading, setLoading] = useState(true);
    const [processingId, setProcessingId] = useState<string | null>(null);
    const [selectedId, setSelectedId] = useState<string | null>(null);
    const [visibleCount, setVisibleCount] = useState(INITIAL_VISIBLE_COUNT);
    const [loadError, setLoadError] = useState<string | null>(null);

    const loadSessions = useCallback(async (silent = false) => {
        if (!silent) {
            setLoading(true);
        }
        setLoadError(null);
        try {
            const data = await sessionService.list(8000);
            data.sort((a, b) => b.UpdatedAt - a.UpdatedAt);
            setSessions(data);
            cachedSessions = data;
            cachedAt = Date.now();
        } catch (error) {
            console.error('Failed to load sessions:', error);
            setLoadError('历史会话加载失败，请重试');
            if (!silent) toast.error('无法加载历史记录');
        } finally {
            if (!silent) {
                setLoading(false);
            }
        }
    }, []);

    useEffect(() => {
        if (!isOpen) return;
        setVisibleCount(INITIAL_VISIBLE_COUNT);
        setSelectedId(null);

        const cacheValid = cachedSessions.length > 0 && (Date.now() - cachedAt < CACHE_TTL);
        if (cacheValid) {
            setSessions(cachedSessions);
            setLoading(false);
            void loadSessions(true);
        } else {
            void loadSessions(false);
        }
    }, [isOpen, loadSessions]);

    const handleDelete = async (e: React.MouseEvent, id: string) => {
        e.stopPropagation();
        if (!window.confirm('确定要删除这个会话吗？')) return;

        setProcessingId(id);
        try {
            await sessionService.delete(id);
            setSessions(prev => prev.filter(s => s.ID !== id));
            toast.success('会话已删除');
        } catch (error) {
            console.error('Failed to delete session:', error);
            toast.error('删除失败');
        } finally {
            setProcessingId(null);
        }
    };

    const handleGenerateSummary = async (e: React.MouseEvent, id: string) => {
        e.stopPropagation();
        setProcessingId(id);
        try {
            const { summary } = await sessionService.generateSummary(id);
            setSessions(prev => prev.map(s => 
                s.ID === id ? { ...s, summary } : s
            ));
            toast.success('总结已生成');
        } catch (error) {
            console.error('Failed to generate summary:', error);
            toast.error('生成总结失败');
        } finally {
            setProcessingId(null);
        }
    };

    const handleSelect = async (id: string) => {
        if (selectedId || processingId) return;
        setSelectedId(id);
        try {
            await Promise.resolve(onSelectSession(id));
        } catch (error) {
            console.error('Failed to select session:', error);
            toast.error('打开历史会话失败');
            setSelectedId(null);
        }
    };

    if (!isOpen) return null;

    const visibleSessions = sessions.slice(0, visibleCount);
    const hasMore = visibleCount < sessions.length;

    return (
        <div className="fixed inset-0 z-50 bg-black/50 flex items-center justify-center backdrop-blur-sm">
            <div className="bg-white dark:bg-zinc-900 w-full max-w-2xl h-[80vh] rounded-xl shadow-2xl flex flex-col border border-zinc-200 dark:border-zinc-800 overflow-hidden animate-in fade-in zoom-in-95 duration-200">
                <div className="flex items-center justify-between p-4 border-b border-zinc-200 dark:border-zinc-800">
                    <h2 className="text-lg font-semibold flex items-center gap-2">
                        <Calendar className="w-5 h-5 text-zinc-500" />
                        历史对话
                    </h2>
                    <button onClick={onClose} className="p-2 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded-full transition-colors">
                        <X className="w-5 h-5" />
                    </button>
                </div>

                <div className="flex-1 overflow-hidden">
                    {loading ? (
                        <div className="flex items-center justify-center h-full text-zinc-500 gap-2">
                            <Loader2 className="w-5 h-5 animate-spin" />
                            加载中...
                        </div>
                    ) : loadError && sessions.length === 0 ? (
                        <div className="flex flex-col items-center justify-center h-full text-zinc-500 gap-4">
                            <p>{loadError}</p>
                            <button
                                onClick={() => void loadSessions(false)}
                                className="px-3 py-1.5 text-sm rounded bg-blue-600 hover:bg-blue-500 text-white transition-colors"
                            >
                                重新加载
                            </button>
                        </div>
                    ) : sessions.length === 0 ? (
                        <div className="flex flex-col items-center justify-center h-full text-zinc-500 gap-2">
                            <MessageSquare className="w-10 h-10 opacity-20" />
                            <p>暂无历史记录</p>
                        </div>
                    ) : (
                        <div className="h-full overflow-y-auto p-4 space-y-3">
                            {visibleSessions.map((session) => (
                                <div
                                    key={session.ID}
                                    onClick={() => void handleSelect(session.ID)}
                                    className="group relative h-[160px] flex flex-col p-4 rounded-lg border border-zinc-200 dark:border-zinc-800 hover:border-zinc-300 dark:hover:border-zinc-700 bg-zinc-50/50 dark:bg-zinc-900/50 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-all cursor-pointer"
                                >
                                    <div className="flex items-start justify-between mb-2 shrink-0">
                                        <h3 className="font-medium text-zinc-900 dark:text-zinc-100 line-clamp-1 flex-1 mr-2">
                                            {session.Title || '未命名会话'}
                                        </h3>
                                        <div className="flex items-center gap-2 text-xs text-zinc-500 shrink-0">
                                            <span>{new Date(session.UpdatedAt * 1000).toLocaleString()}</span>
                                            <button
                                                onClick={(e) => handleDelete(e, session.ID)}
                                                className="p-1.5 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20 rounded opacity-0 group-hover:opacity-100 transition-opacity"
                                                title="删除"
                                                disabled={processingId === session.ID || selectedId === session.ID}
                                            >
                                                <Trash2 className="w-3.5 h-3.5" />
                                            </button>
                                        </div>
                                    </div>

                                    <div className="flex-1 min-h-0 mb-2">
                                        <p className="text-sm text-zinc-500 dark:text-zinc-400 line-clamp-2">
                                            {session.summary || (
                                                <span className="italic opacity-50">暂无总结...</span>
                                            )}
                                        </p>
                                    </div>

                                    <div className="flex items-center justify-between mt-auto shrink-0">
                                        <div className="flex items-center gap-2 text-xs text-zinc-400">
                                            <MessageSquare className="w-3 h-3" />
                                            {session.MessageCount} 条消息
                                        </div>

                                        <div className="flex items-center gap-2">
                                            {!session.summary && (
                                                <button
                                                    onClick={(e) => handleGenerateSummary(e, session.ID)}
                                                    disabled={processingId === session.ID || selectedId === session.ID}
                                                    className="text-xs flex items-center gap-1 text-emerald-600 hover:text-emerald-700 dark:text-emerald-500 dark:hover:text-emerald-400 bg-emerald-50 dark:bg-emerald-900/20 px-2 py-1 rounded transition-colors opacity-0 group-hover:opacity-100"
                                                >
                                                    {processingId === session.ID ? (
                                                        <Loader2 className="w-3 h-3 animate-spin" />
                                                    ) : (
                                                        <FileText className="w-3 h-3" />
                                                    )}
                                                    生成总结
                                                </button>
                                            )}

                                            {selectedId === session.ID ? (
                                                <div className="text-xs text-blue-500 flex items-center gap-1">
                                                    <Loader2 className="w-3 h-3 animate-spin" />
                                                    正在打开
                                                </div>
                                            ) : (
                                                <div className="text-xs text-blue-500 flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                                                    <RotateCcw className="w-3 h-3" />
                                                    点击恢复
                                                </div>
                                            )}
                                        </div>
                                    </div>
                                </div>
                            ))}
                            {hasMore && (
                                <div className="flex justify-center pt-2">
                                    <button
                                        onClick={() => setVisibleCount((prev) => prev + VISIBLE_STEP)}
                                        className="px-3 py-1.5 text-sm rounded bg-zinc-100 dark:bg-zinc-800 hover:bg-zinc-200 dark:hover:bg-zinc-700 text-zinc-700 dark:text-zinc-300 transition-colors"
                                    >
                                        加载更多 ({sessions.length - visibleCount})
                                    </button>
                                </div>
                            )}
                        </div>
                    )}
                </div>
            </div>
        </div>
    );
}

import React, { useState, useEffect, useMemo, useCallback } from 'react';
import { Trash2, Plus, Brain, Loader2, User, Briefcase, Search } from 'lucide-react';
import { memoryService, type Memory, type MemoryDedupStats } from '../../services/memoryService';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

export const MemoriesView: React.FC = () => {
    const { t } = useTranslation();
    const [memories, setMemories] = useState<Memory[]>([]);
    const [loading, setLoading] = useState(true);
    const [newContent, setNewContent] = useState('');
    const [newCategory, setNewCategory] = useState('fact');
    const [newScope, setNewScope] = useState('project');
    const [adding, setAdding] = useState(false);
    const [deduping, setDeduping] = useState(false);
    const [searchQuery, setSearchQuery] = useState('');

    const loadMemories = useCallback(async () => {
        try {
            setLoading(true);
            const data = searchQuery 
                ? await memoryService.search(searchQuery) 
                : await memoryService.list();
            setMemories(data);
        } catch (error) {
            console.error('Failed to load memories:', error);
        } finally {
            setLoading(false);
        }
    }, [searchQuery]);

    useEffect(() => {
        loadMemories();
    }, [loadMemories]);

    const handleAdd = async () => {
        if (!newContent.trim()) return;
        
        try {
            setAdding(true);
            // Prepend scope to content if it's user scope, or always to be consistent
            // The backend tool does this, but here we are calling the API directly.
            // We should simulate what the tool does or just prepend it manually.
            // API expects 'content'.
            let contentToSend = newContent;
            if (newScope) {
                contentToSend = `[Scope: ${newScope}] ${newContent}`;
            }

            await memoryService.add(contentToSend, newCategory);
            setNewContent('');
            await loadMemories();
        } catch (error) {
            console.error('Failed to add memory:', error);
        } finally {
            setAdding(false);
        }
    };

    const handleDelete = async (id: string) => {
        if (!confirm('Are you sure you want to delete this memory?')) return;
        
        try {
            await memoryService.delete(id);
            setMemories(memories.filter(m => m.id !== id));
        } catch (error) {
            console.error('Failed to delete memory:', error);
        }
    };

    const handleDeduplicate = async () => {
        try {
            setDeduping(true);
            const preview: MemoryDedupStats = await memoryService.deduplicate(true, 0.92);
            if (preview.deleted === 0) {
                toast.success('未发现需要去重的记忆');
                return;
            }
            if (!confirm(`预演结果：将删除 ${preview.deleted} 条记忆，其中精确重复 ${preview.duplicate_by_text} 条，语义重复 ${preview.duplicate_by_semantic} 条。确认继续吗？`)) {
                return;
            }
            const stats: MemoryDedupStats = await memoryService.deduplicate(false, 0.92);
            toast.success(`去重完成：删除 ${stats.deleted} 条，语义重复 ${stats.duplicate_by_semantic} 条`);
            await loadMemories();
        } catch (error) {
            console.error('Failed to deduplicate memories:', error);
            toast.error('记忆去重失败');
        } finally {
            setDeduping(false);
        }
    };

    // Parse and group memories
    type ParsedMemory = Memory & { scope: string };

    const { userMemories, projectMemories } = useMemo(() => {
        const user: ParsedMemory[] = [];
        const project: ParsedMemory[] = [];

        memories.forEach(m => {
            const scopeMatch = m.content.match(/^\[Scope: (.*?)\] /i);
            let scope = 'project';
            let cleanContent = m.content;

            if (scopeMatch) {
                scope = scopeMatch[1].toLowerCase();
                cleanContent = m.content.replace(/^\[Scope: .*?\] /i, '');
            } else if (m.layer) {
                // Map Layer to Scope
                if (m.layer === 1) scope = 'user'; // Core
                else if (m.layer === 2) scope = 'project'; // Domain
                else if (m.layer === 3) scope = 'knowledge'; // Knowledge
                else if (m.layer === 4) scope = 'episodic'; // Episodic
            }

            const parsedMemory: ParsedMemory = { ...m, content: cleanContent, scope };

            if (scope === 'user' || scope === 'core') {
                user.push(parsedMemory);
            } else {
                project.push(parsedMemory);
            }
        });

        return { userMemories: user, projectMemories: project };
    }, [memories]);

    const MemoryItem = ({ memory }: { memory: ParsedMemory }) => (
        <div className="flex items-center justify-between p-3 rounded-lg border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-950/50 hover:bg-zinc-50 dark:hover:bg-zinc-900 transition-colors group">
            <div className="flex-1 min-w-0 mr-4">
                <div className="flex items-center gap-2 mb-1">
                    <span className={`text-[10px] px-1.5 py-0.5 rounded-full font-medium uppercase tracking-wider ${
                        memory.scope === 'user' 
                            ? 'bg-indigo-100 text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-300' 
                            : 'bg-slate-100 text-slate-700 dark:bg-slate-900/30 dark:text-slate-300'
                    }`}>
                        {memory.scope}
                        {memory.layer ? ` (L${memory.layer})` : ''}
                    </span>
                    <span className="text-xs text-zinc-400 font-medium px-1.5 py-0.5 bg-zinc-100 dark:bg-zinc-800 rounded">
                        {memory.category}
                    </span>
                    <span className="text-[10px] text-zinc-400">
                        {new Date(memory.created_at * 1000).toLocaleDateString()}
                    </span>
                </div>
                <div className="text-sm text-zinc-700 dark:text-zinc-300 break-words">
                    {memory.summary?.Valid ? (
                        <div className="mb-1">
                            <span className="font-semibold text-xs text-amber-600 dark:text-amber-400 uppercase mr-1">Summary:</span>
                            {memory.summary.String}
                            <div className="mt-1 text-xs text-zinc-500 border-l-2 border-zinc-200 pl-2">
                                {memory.content}
                            </div>
                        </div>
                    ) : (
                        memory.content
                    )}
                </div>
            </div>
            <button 
                onClick={() => handleDelete(memory.id)}
                className="p-2 text-zinc-400 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20 rounded-lg opacity-0 group-hover:opacity-100 transition-all"
            >
                <Trash2 className="w-4 h-4" />
            </button>
        </div>
    );

    if (loading) {
        return <div className="flex items-center justify-center h-64"><Loader2 className="w-8 h-8 animate-spin text-blue-500" /></div>;
    }

    return (
        <div className="space-y-6">
            <div className="space-y-4">
                <h3 className="text-lg font-medium text-zinc-900 dark:text-zinc-100 flex items-center gap-2">
                    <Brain className="w-5 h-5 text-blue-500" />
                    {t('settings.memories_view.title', '记忆管理')}
                </h3>
                <p className="text-sm text-zinc-500 dark:text-zinc-400">
                    {t('settings.memories_view.description', '管理 Agent 对您的了解。记忆分为用户作用域（全局）和项目作用域（本地）。')}
                </p>
                
                <div className="space-y-2">
                    <div className="flex flex-wrap gap-2">
                        <select 
                            value={newScope}
                            onChange={(e) => setNewScope(e.target.value)}
                            className="w-full sm:w-36 px-3 py-2 rounded-lg border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 text-sm outline-none focus:ring-2 focus:ring-blue-500/20 focus:border-blue-500"
                        >
                            <option value="project">{t('settings.memories_view.project_scope') as string}</option>
                            <option value="user">{t('settings.memories_view.user_scope') as string}</option>
                        </select>
                        <select 
                            value={newCategory}
                            onChange={(e) => setNewCategory(e.target.value)}
                            className="w-full sm:w-36 px-3 py-2 rounded-lg border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 text-sm outline-none focus:ring-2 focus:ring-blue-500/20 focus:border-blue-500"
                        >
                            <option value="fact">{t('settings.memories_view.categories.fact') as string}</option>
                            <option value="habit">{t('settings.memories_view.categories.habit') as string}</option>
                            <option value="preference">{t('settings.memories_view.categories.preference') as string}</option>
                            <option value="instruction">{t('settings.memories_view.categories.instruction', '指令') as string}</option>
                        </select>
                        <input 
                            type="text" 
                            value={newContent}
                            onChange={(e) => setNewContent(e.target.value)}
                            placeholder={t('settings.memories_view.add_placeholder', '添加新记忆...')} 
                            className="min-w-0 flex-1 w-full sm:min-w-[260px] px-3 py-2 rounded-lg border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 placeholder:text-zinc-400 text-sm outline-none focus:ring-2 focus:ring-blue-500/20 focus:border-blue-500"
                            onKeyDown={(e) => e.key === 'Enter' && handleAdd()}
                        />
                    </div>
                    <div className="flex flex-wrap items-center gap-2">
                        <button 
                            onClick={handleAdd}
                            disabled={adding || !newContent.trim()}
                            className="px-3 py-2 rounded-lg bg-blue-500 text-white hover:bg-blue-600 disabled:opacity-50 disabled:cursor-not-allowed transition-colors flex items-center gap-2 text-sm font-medium whitespace-nowrap"
                        >
                            {adding ? <Loader2 className="w-4 h-4 animate-spin" /> : <Plus className="w-4 h-4" />}
                            {t('settings.memories_view.add', '添加')}
                        </button>
                        <button
                            onClick={handleDeduplicate}
                            disabled={deduping}
                            className="px-3 py-2 rounded-lg bg-amber-500 text-white hover:bg-amber-600 disabled:opacity-50 disabled:cursor-not-allowed transition-colors flex items-center gap-2 text-sm font-medium whitespace-nowrap"
                        >
                            {deduping ? <Loader2 className="w-4 h-4 animate-spin" /> : <Brain className="w-4 h-4" />}
                            手动去重
                        </button>
                    </div>
                </div>

                {/* Search Bar */}
                <div className="relative">
                    <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-zinc-400" />
                    <input
                        type="text"
                        value={searchQuery}
                        onChange={(e) => setSearchQuery(e.target.value)}
                        placeholder={t('settings.memories_view.search_placeholder', 'Search memories...') as string}
                        className="w-full pl-9 pr-3 py-2 rounded-lg border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 placeholder:text-zinc-400 text-sm outline-none focus:ring-2 focus:ring-blue-500/20 focus:border-blue-500"
                        onKeyDown={(e) => e.key === 'Enter' && loadMemories()}
                    />
                </div>
            </div>

            <div className="space-y-6">
                {/* User Scope Section */}
                <div className="space-y-2">
                    <h4 className="text-xs font-semibold text-zinc-500 uppercase tracking-wider flex items-center gap-2">
                        <User className="w-3 h-3" />
                        {t('settings.memories_view.user_memories_title', '用户记忆 (全局)')}
                    </h4>
                    {userMemories.length === 0 ? (
                        <div className="text-center py-4 text-xs text-zinc-400 border border-dashed border-zinc-200 dark:border-zinc-800 rounded-lg">
                            {t('settings.memories_view.no_user_memories', '暂无用户记忆')}
                        </div>
                    ) : (
                        userMemories.map((m) => <MemoryItem key={m.id} memory={m} />)
                    )}
                </div>

                {/* Project Scope Section */}
                <div className="space-y-2">
                    <h4 className="text-xs font-semibold text-zinc-500 uppercase tracking-wider flex items-center gap-2">
                        <Briefcase className="w-3 h-3" />
                        {t('settings.memories_view.project_memories_title', '项目记忆 (本地)')}
                    </h4>
                    {projectMemories.length === 0 ? (
                        <div className="text-center py-4 text-xs text-zinc-400 border border-dashed border-zinc-200 dark:border-zinc-800 rounded-lg">
                            {t('settings.memories_view.no_project_memories', '暂无项目记忆')}
                        </div>
                    ) : (
                        projectMemories.map((m) => <MemoryItem key={m.id} memory={m} />)
                    )}
                </div>
            </div>
        </div>
    );
};

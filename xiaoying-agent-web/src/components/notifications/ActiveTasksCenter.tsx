import { useState, useRef, useEffect, type CSSProperties } from 'react';
import { CalendarClock, Trash2, Play, Pause } from 'lucide-react';
import { cn } from '../../lib/utils';
import { type ScheduledTask, taskService } from '../../services/taskService';
import { toast } from 'sonner';
import { useTranslation } from 'react-i18next';

const SYSTEM_TASK_TYPES = ['cleanup_sessions', 'cleanup_memories', 'scan_desktop'];

function TaskItem({ task, onDelete, onToggle }: { 
    task: ScheduledTask, 
    onDelete: (id: string) => void,
    onToggle: (id: string) => void
}) {
    const { t } = useTranslation();
    // const [isHovered, setIsHovered] = useState(false); // Unused
    
    // Parse parameters to display something useful
    let paramsDisplay = task.parameters;
    try {
        const p = JSON.parse(task.parameters);
        if (p.prompt) paramsDisplay = p.prompt;
        else if (p.message) paramsDisplay = p.message;
        else if (Object.keys(p).length > 0) paramsDisplay = JSON.stringify(p);
        else paramsDisplay = '';
    } catch {
        paramsDisplay = task.parameters;
    }

    const taskTitle = t(`tasks.types.${task.task_type}`, task.task_type);

    return (
        <div
            className={cn(
                "p-3 rounded-lg text-left transition-colors relative group border border-transparent",
                task.is_active
                    ? "bg-blue-50/50 dark:bg-blue-900/10 text-zinc-900 dark:text-zinc-100 hover:border-blue-200 dark:hover:border-blue-800"
                    : "bg-zinc-50 dark:bg-zinc-800/50 text-zinc-500 dark:text-zinc-400 hover:border-zinc-200 dark:hover:border-zinc-700"
            )}
            // onMouseEnter={() => setIsHovered(true)}
            // onMouseLeave={() => setIsHovered(false)}
        >
            <div className="flex items-start justify-between gap-2 mb-1">
                <span className="text-xs font-medium leading-none flex items-center gap-1.5">
                    {taskTitle}
                    {task.is_active ? (
                        <span className="w-1.5 h-1.5 bg-green-500 rounded-full animate-pulse" title={t('tasks.active')} />
                    ) : (
                        <span className="w-1.5 h-1.5 bg-zinc-300 dark:bg-zinc-600 rounded-full" title={t('tasks.paused')} />
                    )}
                </span>
                <span className="text-[10px] text-zinc-400 whitespace-nowrap">
                    {task.schedule_expression ? (
                        (task.schedule_expression.startsWith('@every') || task.schedule_expression.includes('*/'))
                        ? `${t('tasks.recurring')}: ${task.schedule_expression}`
                        : `${t('tasks.timed')}: ${task.schedule_expression}`
                    ) : t('tasks.one_time')}
                </span>
            </div>
            
            <p className="text-xs text-zinc-600 dark:text-zinc-300 leading-relaxed line-clamp-2 mb-1">
                {paramsDisplay}
            </p>

            <div className="flex items-center justify-between text-[10px] text-zinc-400">
                <span>
                    {t('tasks.next_run')}: {task.next_run_at ? new Date(task.next_run_at).toLocaleString('zh-CN', {
                        month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit'
                    }) : 'N/A'}
                </span>
                
                <div className="flex gap-1">
                    <button
                        onClick={(e) => {
                            e.stopPropagation();
                            onToggle(task.id);
                        }}
                        className="p-1 hover:bg-zinc-200 dark:hover:bg-zinc-700 rounded text-zinc-500 hover:text-blue-500"
                        title={task.is_active ? t('tasks.paused') : t('tasks.active')}
                    >
                        {task.is_active ? <Pause className="w-3 h-3" /> : <Play className="w-3 h-3" />}
                    </button>
                    <button
                        onClick={(e) => {
                            e.stopPropagation();
                            onDelete(task.id);
                        }}
                        disabled={SYSTEM_TASK_TYPES.includes(task.task_type)}
                        className={cn(
                            "p-1 rounded transition-colors",
                            SYSTEM_TASK_TYPES.includes(task.task_type)
                                ? "text-zinc-300 dark:text-zinc-700 cursor-not-allowed"
                                : "hover:bg-zinc-200 dark:hover:bg-zinc-700 text-zinc-500 hover:text-red-500"
                        )}
                        title={SYSTEM_TASK_TYPES.includes(task.task_type) ? t('tasks.cannot_delete_system_task') : t('common.delete')}
                    >
                        <Trash2 className="w-3 h-3" />
                    </button>
                </div>
            </div>
        </div>
    );
}

export function ActiveTasksCenter({ onOpen }: { onOpen?: () => void }) {
    const { t } = useTranslation();
    const [isOpen, setIsOpen] = useState(false);
    const [tasks, setTasks] = useState<ScheduledTask[]>([]);
    const [isLoading, setIsLoading] = useState(false);
    const containerRef = useRef<HTMLDivElement>(null);

    const fetchTasks = async () => {
        try {
            setIsLoading(true);
            const data = await taskService.list();
            setTasks(data);
        } catch (error) {
            console.error('Failed to fetch tasks:', error);
            // Don't show toast on initial load to avoid annoyance, only on user action or if critical
        } finally {
            setIsLoading(false);
        }
    };

    useEffect(() => {
        if (isOpen) {
            fetchTasks();
        }
    }, [isOpen]);

    // Expose a method or just rely on a global event to refresh?
    // Since we want to update when a new task is added via UI (e.g. from Settings),
    // listening to a custom event is a good React pattern without Redux.
    useEffect(() => {
        const handleTaskUpdate = () => {
            fetchTasks();
        };
        window.addEventListener('task-updated', handleTaskUpdate);
        
        // Initial fetch
        fetchTasks();
        // Poll every minute
        const interval = setInterval(fetchTasks, 60000);
        
        return () => {
            window.removeEventListener('task-updated', handleTaskUpdate);
            clearInterval(interval);
        };
    }, []);

    // Close on click outside
    useEffect(() => {
        function handleClickOutside(event: MouseEvent) {
            if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
                setIsOpen(false);
            }
        }
        if (isOpen) {
            document.addEventListener('mousedown', handleClickOutside);
        }
        return () => {
            document.removeEventListener('mousedown', handleClickOutside);
        };
    }, [isOpen]);

    const handleToggle = async (id: string) => {
        try {
            await taskService.toggle(id);
            toast.success(t('tasks.toggle_success'));
            fetchTasks();
        } catch {
            toast.error(t('common.error'));
        }
    };

    const handleDelete = async (id: string) => {
        // Double check against SYSTEM_TASK_TYPES, though UI should hide the button
        const task = tasks.find(t => t.id === id);
        if (task && SYSTEM_TASK_TYPES.includes(task.task_type)) {
            toast.error(t('tasks.cannot_delete_system_task'));
            return;
        }

        if (!confirm(t('tasks.delete_confirm'))) return;
        
        try {
            await taskService.delete(id);
            toast.success(t('tasks.delete_success'));
            fetchTasks();
        } catch {
            toast.error(t('common.error'));
        }
    };

    const activeCount = tasks.filter(t => t.is_active && t.schedule_expression).length;
    const displayTasks = tasks.filter(t => t.schedule_expression);
    
    const timedTasks = displayTasks.filter(t => t.schedule_expression && !t.schedule_expression.startsWith('@every') && !t.schedule_expression.includes('*/'));
    const recurringTasks = displayTasks.filter(t => t.schedule_expression && (t.schedule_expression.startsWith('@every') || t.schedule_expression.includes('*/')));

    const noDragRegionStyle: CSSProperties & { WebkitAppRegion?: 'no-drag' } = { WebkitAppRegion: 'no-drag' };

    return (
        <div className="relative" ref={containerRef} style={noDragRegionStyle}>
            <button
                onClick={() => {
                    setIsOpen(!isOpen);
                    onOpen?.();
                }}
                className="relative p-1.5 text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded-md transition-colors"
                title={t('tasks.title')}
            >
                <CalendarClock className="w-4 h-4" />
                {activeCount > 0 && (
                    <span className="absolute top-1 right-1 w-2 h-2 bg-green-500 rounded-full ring-2 ring-white dark:ring-zinc-900" />
                )}
            </button>

            {isOpen && (
                <div className="absolute right-0 top-full mt-2 w-80 bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 rounded-xl shadow-lg z-50 overflow-hidden flex flex-col max-h-[500px]">
                    <div className="flex items-center justify-between px-4 py-3 border-b border-zinc-100 dark:border-zinc-800 bg-zinc-50/50 dark:bg-zinc-900/50">
                        <h3 className="text-sm font-medium text-zinc-900 dark:text-zinc-100">
                            {t('tasks.title')}
                        </h3>
                        <span className="text-xs text-zinc-400">
                            {activeCount} {t('tasks.active')}
                        </span>
                    </div>

                    <div className="flex-1 overflow-y-auto p-2 space-y-4">
                        {isLoading ? (
                            <div className="text-center py-8 text-zinc-400">
                                <p className="text-xs">{t('common.loading')}</p>
                            </div>
                        ) : displayTasks.length === 0 ? (
                            <div className="text-center py-8 text-zinc-400">
                                <CalendarClock className="w-8 h-8 mx-auto mb-2 opacity-20" />
                                <p className="text-xs">{t('tasks.no_active_tasks')}</p>
                            </div>
                        ) : (
                            <>
                                {timedTasks.length > 0 && (
                                    <div className="space-y-1">
                                        <div className="px-2 text-[10px] font-medium text-zinc-400 uppercase tracking-wider">
                                            {t('tasks.timed')}
                                        </div>
                                        {timedTasks.map(task => (
                                            <TaskItem 
                                                key={task.id} 
                                                task={task} 
                                                onToggle={handleToggle}
                                                onDelete={handleDelete}
                                            />
                                        ))}
                                    </div>
                                )}
                                
                                {recurringTasks.length > 0 && (
                                    <div className="space-y-1">
                                        <div className="px-2 text-[10px] font-medium text-zinc-400 uppercase tracking-wider">
                                            {t('tasks.recurring')}
                                        </div>
                                        {recurringTasks.map(task => (
                                            <TaskItem 
                                                key={task.id} 
                                                task={task} 
                                                onToggle={handleToggle}
                                                onDelete={handleDelete}
                                            />
                                        ))}
                                    </div>
                                )}
                            </>
                        )}
                    </div>
                </div>
            )}
        </div>
    );
}

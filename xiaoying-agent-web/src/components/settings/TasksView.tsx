import React, { useState, useEffect, useCallback } from 'react';
import { Trash2, CalendarClock, Loader2, Play, Pause, Edit2, Plus } from 'lucide-react';
import { taskService, type ScheduledTask } from '../../services/taskService';
import { toast } from 'sonner';
import { useTranslation } from 'react-i18next';
// import { cn } from '../../lib/utils'; // Unused

const SYSTEM_TASK_TYPES = ['cleanup_sessions', 'cleanup_memories', 'scan_desktop']; // 'news_briefing', 'user_profiling', 'daily_greeting' are now user-managed
const TASK_TYPE_OPTIONS = [
    { value: 'general_agent', label: '智能体任务' },
    { value: 'general_reminder', label: '通用提醒' },
];

export const TasksView: React.FC = () => {
    const { t } = useTranslation();
    const [tasks, setTasks] = useState<ScheduledTask[]>([]);
    const [loading, setLoading] = useState(true);
    const [editingTask, setEditingTask] = useState<ScheduledTask | null>(null);
    const [editForm, setEditForm] = useState({ schedule_expression: '', parameters: '' });
    const [scheduleType, setScheduleType] = useState<'daily' | 'raw'>('raw');
    const [dailyTime, setDailyTime] = useState('09:00');
    
    // New task state
    const [showCreateForm, setShowCreateForm] = useState(false);
    const [newTaskType, setNewTaskType] = useState('general_agent');
    const [newTaskScheduleType, setNewTaskScheduleType] = useState<'daily' | 'raw'>('daily');
    const [newTaskDailyTime, setNewTaskDailyTime] = useState('09:00');
    const [newTaskRawSchedule, setNewTaskRawSchedule] = useState('');
    const [newTaskContent, setNewTaskContent] = useState('');

    const loadTasks = useCallback(async () => {
        try {
            setLoading(true);
            const data = await taskService.list();
            setTasks(data);
        } catch (error) {
            console.error('Failed to load tasks:', error);
            toast.error(t('tasks.load_error'));
        } finally {
            setLoading(false);
        }
    }, [t]);

    useEffect(() => {
        loadTasks();
    }, [loadTasks]);

    const handleDelete = async (id: string) => {
        // Check for system task
        const task = tasks.find(t => t.id === id);
        if (task && SYSTEM_TASK_TYPES.includes(task.task_type)) {
            toast.error(t('tasks.cannot_delete_system_task'));
            return;
        }

        if (!confirm(t('tasks.delete_confirm'))) return;
        
        try {
            await taskService.delete(id);
            toast.success(t('tasks.delete_success'));
            loadTasks();
        } catch (error) {
            console.error('Failed to delete task:', error);
            toast.error(t('common.error'));
        }
    };

    const handleToggle = async (id: string) => {
        try {
            await taskService.toggle(id);
            toast.success(t('tasks.toggle_success'));
            loadTasks();
        } catch (error) {
            console.error('Failed to toggle task:', error);
            toast.error(t('common.error'));
        }
    };

    const handleCreate = async () => {
        if (!newTaskType.trim()) {
            toast.error('请选择任务类型');
            return;
        }
        
        // Prepare schedule expression based on type
        let scheduleExpression = '';
        if (newTaskScheduleType === 'daily') {
            scheduleExpression = `daily:${newTaskDailyTime}`;
        } else {
            scheduleExpression = newTaskRawSchedule;
        }
        
        try {
            const response = await fetch('/v1/tasks', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    task_type: newTaskType,
                    schedule_expression: scheduleExpression,
                    parameters: newTaskContent || '{}',
                })
            });
            
            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }
            
            toast.success('任务创建成功');
            setShowCreateForm(false);
            setNewTaskType('general_agent');
            setNewTaskScheduleType('daily');
            setNewTaskDailyTime('09:00');
            setNewTaskRawSchedule('');
            setNewTaskContent('');
            loadTasks();
        } catch (error) {
            console.error('Failed to create task:', error);
            toast.error(`创建失败: ${error instanceof Error ? error.message : '未知错误'}`);
        }
    };

    const handleEdit = (task: ScheduledTask) => {
        setEditingTask(task);
        const expr = task.schedule_expression || '';
        
        if (expr.startsWith('daily:')) {
            setScheduleType('daily');
            const parts = expr.split(':');
            if (parts.length >= 3) {
                // Ensure HH:MM format (pad with zeros if needed)
                const h = parts[1].padStart(2, '0');
                const m = parts[2].padStart(2, '0');
                setDailyTime(`${h}:${m}`);
            }
        } else {
            setScheduleType('raw');
        }

        setEditForm({
            schedule_expression: expr,
            parameters: task.parameters || ''
        });
    };

    const handleUpdate = async () => {
        if (!editingTask) return;
        
        // Prepare final expression based on type
        let finalExpression = editForm.schedule_expression;
        if (scheduleType === 'daily') {
            finalExpression = `daily:${dailyTime}`;
        }

        try {
            await fetch(`/v1/tasks/${editingTask.id}?action=update`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    ...editForm,
                    schedule_expression: finalExpression
                })
            });
            toast.success(t('tasks.update_success'));
            setEditingTask(null);
            loadTasks();
            // Dispatch event to notify other components (like ActiveTasksCenter)
            window.dispatchEvent(new Event('task-updated'));
        } catch (error) {
            console.error('Failed to update task:', error);
            toast.error(t('common.error'));
        }
    };

    if (loading) {
        return <div className="flex items-center justify-center h-64"><Loader2 className="w-8 h-8 animate-spin text-blue-500" /></div>;
    }

    return (
        <div className="space-y-6">
            <div className="space-y-4">
                <div className="flex items-center justify-between">
                    <h3 className="text-lg font-medium text-zinc-900 dark:text-zinc-100 flex items-center gap-2">
                        <CalendarClock className="w-5 h-5 text-blue-500" />
                        {t('tasks.title')}
                    </h3>
                    <button
                        onClick={() => setShowCreateForm(true)}
                        className="px-3 py-1.5 text-sm font-medium bg-blue-500 text-white hover:bg-blue-600 rounded-lg transition-colors flex items-center gap-1.5"
                    >
                        <Plus className="w-4 h-4" />
                        新建任务
                    </button>
                </div>
                <p className="text-sm text-zinc-500 dark:text-zinc-400">
                    {t('tasks.description')}
                </p>
            </div>

            {/* Edit Modal/Form */}
            {editingTask && (
                <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
                    <div className="bg-white dark:bg-zinc-900 p-6 rounded-lg w-full max-w-md space-y-4 shadow-xl">
                        <h4 className="text-lg font-medium text-zinc-900 dark:text-zinc-100">{t('tasks.edit_task')} - {t(`tasks.types.${editingTask.task_type}`, editingTask.task_type)}</h4>
                        
                        <div className="space-y-3">
                            <label className="text-sm font-medium text-zinc-900 dark:text-zinc-100">{t('tasks.schedule_type')}</label>
                            <div className="flex gap-4">
                                <label className="flex items-center gap-2 cursor-pointer">
                                    <input 
                                        type="radio" 
                                        name="scheduleType"
                                        checked={scheduleType === 'daily'}
                                        onChange={() => setScheduleType('daily')}
                                        className="text-blue-500"
                                    />
                                    <span className="text-sm text-zinc-900 dark:text-zinc-100">{t('tasks.type_daily')}</span>
                                </label>
                                <label className="flex items-center gap-2 cursor-pointer">
                                    <input 
                                        type="radio" 
                                        name="scheduleType"
                                        checked={scheduleType === 'raw'}
                                        onChange={() => setScheduleType('raw')}
                                        className="text-blue-500"
                                    />
                                    <span className="text-sm text-zinc-900 dark:text-zinc-100">{t('tasks.type_advanced')}</span>
                                </label>
                            </div>
                        </div>

                        {scheduleType === 'daily' ? (
                            <div className="space-y-2">
                                <label className="text-sm font-medium text-zinc-900 dark:text-zinc-100">{t('tasks.time_label')}</label>
                                <input 
                                    type="time"
                                    className="w-full p-2 border rounded bg-white dark:bg-zinc-800 border-zinc-300 dark:border-zinc-700 text-zinc-900 dark:text-zinc-100"
                                    value={dailyTime}
                                    onChange={e => setDailyTime(e.target.value)}
                                />
                            </div>
                        ) : (
                            <div className="space-y-2">
                                <label className="text-sm font-medium text-zinc-900 dark:text-zinc-100">{t('tasks.schedule_label')}</label>
                                <input 
                                    className="w-full p-2 border rounded bg-white dark:bg-zinc-800 border-zinc-300 dark:border-zinc-700 text-zinc-900 dark:text-zinc-100"
                                    value={editForm.schedule_expression}
                                    onChange={e => setEditForm({...editForm, schedule_expression: e.target.value})}
                                />
                            </div>
                        )}
                        
                        <div className="space-y-2">
                            <label className="text-sm font-medium text-zinc-900 dark:text-zinc-100">{t('tasks.parameters_label')}</label>
                            <input 
                                className="w-full p-2 border rounded bg-white dark:bg-zinc-800 border-zinc-300 dark:border-zinc-700 text-zinc-900 dark:text-zinc-100"
                                value={editForm.parameters}
                                onChange={e => setEditForm({...editForm, parameters: e.target.value})}
                            />
                        </div>

                        <div className="flex justify-end gap-2 pt-2">
                            <button 
                                onClick={() => setEditingTask(null)}
                                className="px-4 py-2 text-sm text-zinc-500 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded"
                            >
                                {t('common.cancel')}
                            </button>
                            <button 
                                onClick={handleUpdate}
                                className="px-4 py-2 text-sm bg-blue-500 text-white hover:bg-blue-600 rounded"
                            >
                                {t('common.save')}
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {/* Create Task Modal */}
            {showCreateForm && (
                <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
                    <div className="bg-white dark:bg-zinc-900 p-6 rounded-lg w-full max-w-md space-y-4 shadow-xl">
                        <h4 className="text-lg font-medium text-zinc-900 dark:text-zinc-100">新建计划任务</h4>
                        
                        <div className="space-y-2">
                            <label className="text-sm font-medium text-zinc-900 dark:text-zinc-100">任务类型</label>
                            <select
                                value={newTaskType}
                                onChange={e => setNewTaskType(e.target.value)}
                                className="w-full p-2 border rounded bg-white dark:bg-zinc-800 border-zinc-300 dark:border-zinc-700 text-zinc-900 dark:text-zinc-100"
                            >
                                {TASK_TYPE_OPTIONS.map(opt => (
                                    <option key={opt.value} value={opt.value}>{opt.label}</option>
                                ))}
                            </select>
                        </div>

                        <div className="space-y-3">
                            <label className="text-sm font-medium text-zinc-900 dark:text-zinc-100">调度类型</label>
                            <div className="flex gap-4">
                                <label className="flex items-center gap-2 cursor-pointer">
                                    <input 
                                        type="radio" 
                                        name="newScheduleType"
                                        checked={newTaskScheduleType === 'daily'}
                                        onChange={() => setNewTaskScheduleType('daily')}
                                        className="text-blue-500"
                                    />
                                    <span className="text-sm text-zinc-900 dark:text-zinc-100">每天</span>
                                </label>
                                <label className="flex items-center gap-2 cursor-pointer">
                                    <input 
                                        type="radio" 
                                        name="newScheduleType"
                                        checked={newTaskScheduleType === 'raw'}
                                        onChange={() => setNewTaskScheduleType('raw')}
                                        className="text-blue-500"
                                    />
                                    <span className="text-sm text-zinc-900 dark:text-zinc-100">高级</span>
                                </label>
                            </div>
                        </div>

                        {newTaskScheduleType === 'daily' ? (
                            <div className="space-y-2">
                                <label className="text-sm font-medium text-zinc-900 dark:text-zinc-100">执行时间</label>
                                <input 
                                    type="time"
                                    className="w-full p-2 border rounded bg-white dark:bg-zinc-800 border-zinc-300 dark:border-zinc-700 text-zinc-900 dark:text-zinc-100"
                                    value={newTaskDailyTime}
                                    onChange={e => setNewTaskDailyTime(e.target.value)}
                                />
                            </div>
                        ) : (
                            <div className="space-y-2">
                                <label className="text-sm font-medium text-zinc-900 dark:text-zinc-100">调度表达式</label>
                                <input 
                                    className="w-full p-2 border rounded bg-white dark:bg-zinc-800 border-zinc-300 dark:border-zinc-700 text-zinc-900 dark:text-zinc-100"
                                    value={newTaskRawSchedule}
                                    onChange={e => setNewTaskRawSchedule(e.target.value)}
                                    placeholder="例: 1h 或 30m"
                                />
                                <p className="text-xs text-zinc-500">
                                    格式: <code className="bg-zinc-100 dark:bg-zinc-800 px-1 rounded">1h</code> (每小时) 或 <code className="bg-zinc-100 dark:bg-zinc-800 px-1 rounded">30m</code> (每30分钟)
                                </p>
                            </div>
                        )}
                        
                        <div className="space-y-2">
                            <label className="text-sm font-medium text-zinc-900 dark:text-zinc-100">任务内容</label>
                            <textarea
                                className="w-full p-2 border rounded bg-white dark:bg-zinc-800 border-zinc-300 dark:border-zinc-700 text-zinc-900 dark:text-zinc-100 text-sm"
                                value={newTaskContent}
                                onChange={e => setNewTaskContent(e.target.value)}
                                placeholder="请输入任务内容或参数..."
                                rows={3}
                            />
                        </div>

                        <div className="flex justify-end gap-2 pt-2">
                            <button 
                                onClick={() => setShowCreateForm(false)}
                                className="px-4 py-2 text-sm text-zinc-500 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded"
                            >
                                {t('common.cancel')}
                            </button>
                            <button 
                                onClick={handleCreate}
                                className="px-4 py-2 text-sm bg-blue-500 text-white hover:bg-blue-600 rounded"
                            >
                                创建任务
                            </button>
                        </div>
                    </div>
                </div>
            )}

            <div className="space-y-2">
                {tasks.length === 0 ? (
                    <div className="text-center py-8 text-zinc-500 bg-zinc-50 dark:bg-zinc-900/50 rounded-lg border border-dashed border-zinc-200 dark:border-zinc-800">
                        {t('tasks.no_tasks')}
                    </div>
                ) : (
                    tasks.map(task => (
                        <div 
                            key={task.id}
                            className="p-4 rounded-lg border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-950 flex items-start justify-between gap-4 group hover:border-blue-200 dark:hover:border-blue-800 transition-colors"
                        >
                            <div className="flex-1 space-y-1">
                                <div className="flex items-center gap-2">
                                    <span className={`w-2 h-2 rounded-full ${task.is_active ? 'bg-green-500 animate-pulse' : 'bg-zinc-300'}`} />
                                    <span className="font-medium text-zinc-900 dark:text-zinc-100">
                                        {t(`tasks.types.${task.task_type}`, task.task_type)}
                                    </span>
                                    <span className="text-xs px-2 py-0.5 rounded-full bg-zinc-100 dark:bg-zinc-800 text-zinc-500 font-mono">
                                        {task.schedule_expression ? (
                                            (task.schedule_expression.startsWith('@every') || task.schedule_expression.includes('*/'))
                                            ? `${t('tasks.recurring')}: ${task.schedule_expression}`
                                            : `${t('tasks.timed')}: ${task.schedule_expression}`
                                        ) : t('tasks.one_time')}
                                    </span>
                                </div>
                                <p className="text-sm text-zinc-500 dark:text-zinc-400 font-mono bg-zinc-50 dark:bg-zinc-900/50 p-2 rounded mt-2">
                                    {task.parameters}
                                </p>
                                <div className="flex gap-4 text-xs text-zinc-400 mt-2">
                                    <span>{t('tasks.last_run')}: {task.last_run_at ? new Date(task.last_run_at).toLocaleString() : t('tasks.never')}</span>
                                    <span>{t('tasks.next_run')}: {task.next_run_at ? new Date(task.next_run_at).toLocaleString() : t('tasks.pending')}</span>
                                </div>
                            </div>
                            
                            <div className="flex items-center gap-2">
                                <button
                                    onClick={() => handleToggle(task.id)}
                                    className="p-2 text-zinc-400 hover:text-blue-500 hover:bg-blue-50 dark:hover:bg-blue-900/20 rounded-lg transition-colors"
                                    title={task.is_active ? t('tasks.paused') : t('tasks.active')}
                                >
                                    {task.is_active ? <Pause className="w-4 h-4" /> : <Play className="w-4 h-4" />}
                                </button>
                                <button
                                    onClick={() => handleEdit(task)}
                                    className="p-2 text-zinc-400 hover:text-blue-500 hover:bg-blue-50 dark:hover:bg-blue-900/20 rounded-lg transition-colors"
                                    title={t('common.edit')}
                                >
                                    <Edit2 className="w-4 h-4" />
                                </button>
                                <button
                                    onClick={() => handleDelete(task.id)}
                                    disabled={SYSTEM_TASK_TYPES.includes(task.task_type)}
                                    className={`p-2 rounded-lg transition-colors ${
                                        SYSTEM_TASK_TYPES.includes(task.task_type)
                                        ? "text-zinc-300 dark:text-zinc-700 cursor-not-allowed"
                                        : "text-zinc-400 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20"
                                    }`}
                                    title={SYSTEM_TASK_TYPES.includes(task.task_type) ? t('tasks.cannot_delete_system_task') : t('common.delete')}
                                >
                                    <Trash2 className="w-4 h-4" />
                                </button>
                            </div>
                        </div>
                    ))
                )}
            </div>
        </div>
    );
};

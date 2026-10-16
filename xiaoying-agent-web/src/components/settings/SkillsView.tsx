import React, { useState, useEffect } from 'react';
import { Terminal, Loader2 } from 'lucide-react';
import { toast } from 'sonner';
import { useTranslation } from 'react-i18next';
import { cn } from '../../lib/utils';

interface ToolInfo {
    name: string;
    description: string;
    parameters: Record<string, unknown>;
    active?: boolean;
}

export const SkillsView: React.FC = () => {
    const { t } = useTranslation();
    const [skills, setSkills] = useState<ToolInfo[]>([]);
    const [loading, setLoading] = useState(true);
    const skillCount = skills.length;

    const loadSkills = async () => {
        try {
            setLoading(true);
            const response = await fetch('/v1/skills/refresh', {
                method: 'POST'
            });
            if (!response.ok) {
                throw new Error('Failed to refresh skills');
            }
            const data = await response.json();
            const sortedData = data.sort((a: ToolInfo, b: ToolInfo) => a.name.localeCompare(b.name));
            setSkills(sortedData);
        } catch (error) {
            console.error('Failed to refresh skills:', error);
            toast.error('刷新技能列表失败');
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        void loadSkills();
    }, []);

    return (
        <div className="space-y-6 animate-in fade-in duration-300">
            <div className="flex items-start justify-between gap-4">
                <div className="space-y-1">
                    <h3 className="text-lg font-medium text-zinc-900 dark:text-zinc-100 flex items-center gap-2">
                        <Terminal className="w-5 h-5" />
                        {t('skills.title')}
                    </h3>
                    <p className="text-sm text-zinc-500 dark:text-zinc-400">
                        {t('skills.description')}
                    </p>
                </div>
            </div>

            <div className="rounded-xl border border-zinc-200 bg-zinc-50 p-6 dark:border-zinc-700 dark:bg-zinc-800/50 space-y-6">
                <div className="rounded-xl border border-zinc-200 bg-white px-4 py-3 dark:border-zinc-700 dark:bg-zinc-900/60">
                    <div className="space-y-1">
                        <div className="text-sm font-medium text-zinc-900 dark:text-zinc-100">已发现 {skillCount} 个技能</div>
                        <div className="text-xs text-zinc-500 dark:text-zinc-400"></div>
                    </div>
                </div>

                {loading ? (
                    <div className="flex min-h-48 items-center justify-center rounded-xl border border-dashed border-zinc-200 bg-white dark:border-zinc-700 dark:bg-zinc-900/50">
                        <div className="flex items-center gap-3 text-sm text-zinc-500 dark:text-zinc-400">
                            <Loader2 className="h-5 w-5 animate-spin text-blue-500" />
                            正在加载技能列表...
                        </div>
                    </div>
                ) : skillCount === 0 ? (
                    <div className="text-center py-12 text-zinc-500 dark:text-zinc-400 bg-white dark:bg-zinc-900/50 rounded-xl border border-dashed border-zinc-200 dark:border-zinc-700">
                        {t('skills.no_skills')}
                    </div>
                ) : (
                    <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                        {skills.map((skill) => {
                            const translatedDesc = t(`skills.descriptions.${skill.name}`, { defaultValue: skill.description });

                            return (
                                <div 
                                    key={skill.name}
                                    className="flex h-full flex-col gap-4 rounded-xl border border-zinc-200 bg-white p-5 transition-colors hover:border-blue-200 hover:bg-zinc-50 dark:border-zinc-700 dark:bg-zinc-900/60 dark:hover:border-blue-500/40 dark:hover:bg-zinc-900"
                                >
                                    <div className="flex items-start justify-between gap-3">
                                        <div className="flex items-start gap-3 min-w-0">
                                            <div className={cn(
                                                "rounded-lg p-2",
                                                skill.active !== false
                                                    ? "bg-blue-50 text-blue-600 dark:bg-blue-500/10 dark:text-blue-400"
                                                    : "bg-zinc-100 text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"
                                            )}>
                                                <Terminal className="w-4 h-4" />
                                            </div>
                                            <div className="min-w-0 space-y-1">
                                                <div className="truncate font-mono text-sm font-semibold text-zinc-900 dark:text-zinc-100">
                                                    {skill.name}
                                                </div>
                                                <span className={cn(
                                                    "inline-flex rounded-full px-2 py-0.5 text-xs font-medium",
                                                    skill.active !== false
                                                        ? "bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-400"
                                                        : "bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400"
                                                )}>
                                                    {skill.active !== false ? '已启用' : '未启用'}
                                                </span>
                                            </div>
                                        </div>
                                    </div>

                                    <p className="text-sm leading-6 text-zinc-600 dark:text-zinc-300 line-clamp-3" title={translatedDesc}>
                                        {translatedDesc}
                                    </p>
                                </div>
                            );
                        })}
                    </div>
                )}
            </div>
        </div>
    );
};

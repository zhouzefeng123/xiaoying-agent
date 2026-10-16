import React from 'react';
import { X, Activity, ArrowDown, ArrowUp, Zap } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '../../lib/utils';

interface TokenStatsProps {
    isOpen: boolean;
    onClose: () => void;
    currentUsage: {
        input_tokens: number;
        output_tokens: number;
        total_tokens: number;
    } | null;
    sessionUsage: {
        input_tokens: number;
        output_tokens: number;
        total_tokens: number;
    };
}

export const TokenStatsDialog: React.FC<TokenStatsProps> = ({ isOpen, onClose, currentUsage, sessionUsage }) => {
    const { t } = useTranslation();

    if (!isOpen) return null;

    return (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm p-4">
            <div className="bg-white dark:bg-zinc-900 rounded-xl shadow-2xl w-full max-w-md border border-zinc-200 dark:border-zinc-800 overflow-hidden animate-in fade-in zoom-in-95 duration-200">
                {/* Header */}
                <div className="flex items-center justify-between p-4 border-b border-zinc-200 dark:border-zinc-800 bg-zinc-50/50 dark:bg-zinc-900/50">
                    <div className="flex items-center gap-2">
                        <div className="p-2 bg-blue-100 dark:bg-blue-900/30 rounded-lg">
                            <Activity className="w-5 h-5 text-blue-600 dark:text-blue-400" />
                        </div>
                        <h2 className="text-lg font-semibold text-zinc-900 dark:text-zinc-100">
                            {t('token_stats.title', 'Token Statistics')}
                        </h2>
                    </div>
                    <button 
                        onClick={onClose}
                        className="p-2 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded-lg transition-colors"
                    >
                        <X className="w-5 h-5 text-zinc-500" />
                    </button>
                </div>

                {/* Content */}
                <div className="p-6 space-y-6">
                    {/* Current Request Stats */}
                    <div className="space-y-3">
                        <h3 className="text-sm font-medium text-zinc-500 dark:text-zinc-400 uppercase tracking-wider flex items-center gap-2">
                            <Zap className="w-4 h-4" />
                            {t('token_stats.current_request', 'Current Request')}
                        </h3>
                        
                        {currentUsage ? (
                            <div className="grid grid-cols-3 gap-3">
                                <StatCard 
                                    label={t('token_stats.input', 'Input')} 
                                    value={currentUsage.input_tokens} 
                                    icon={<ArrowDown className="w-4 h-4" />}
                                    color="blue"
                                />
                                <StatCard 
                                    label={t('token_stats.output', 'Output')} 
                                    value={currentUsage.output_tokens} 
                                    icon={<ArrowUp className="w-4 h-4" />}
                                    color="green"
                                />
                                <StatCard 
                                    label={t('token_stats.total', 'Total')} 
                                    value={currentUsage.total_tokens} 
                                    icon={<Activity className="w-4 h-4" />}
                                    color="purple"
                                />
                            </div>
                        ) : (
                            <div className="text-center py-6 bg-zinc-50 dark:bg-zinc-800/50 rounded-lg border border-dashed border-zinc-200 dark:border-zinc-700">
                                <span className="text-zinc-400 text-sm">{t('token_stats.no_data', 'No data for current request')}</span>
                            </div>
                        )}
                    </div>

                    <div className="h-px bg-zinc-100 dark:bg-zinc-800" />

                    {/* Session Stats */}
                    <div className="space-y-3">
                        <h3 className="text-sm font-medium text-zinc-500 dark:text-zinc-400 uppercase tracking-wider flex items-center gap-2">
                            <Activity className="w-4 h-4" />
                            {t('token_stats.session_total', 'History Total')}
                        </h3>
                        
                        <div className="grid grid-cols-3 gap-3">
                            <StatCard 
                                label={t('token_stats.input', 'Input')} 
                                value={sessionUsage.input_tokens} 
                                icon={<ArrowDown className="w-4 h-4" />}
                                color="blue"
                                variant="secondary"
                            />
                            <StatCard 
                                label={t('token_stats.output', 'Output')} 
                                value={sessionUsage.output_tokens} 
                                icon={<ArrowUp className="w-4 h-4" />}
                                color="green"
                                variant="secondary"
                            />
                            <StatCard 
                                label={t('token_stats.total', 'Total')} 
                                value={sessionUsage.total_tokens} 
                                icon={<Activity className="w-4 h-4" />}
                                color="purple"
                                variant="secondary"
                            />
                        </div>
                    </div>
                </div>

                {/* Footer */}
                <div className="p-4 border-t border-zinc-200 dark:border-zinc-800 bg-zinc-50/50 dark:bg-zinc-900/50 flex justify-end">
                    <button 
                        onClick={onClose}
                        className="px-4 py-2 bg-white dark:bg-zinc-800 border border-zinc-200 dark:border-zinc-700 rounded-lg text-sm font-medium hover:bg-zinc-50 dark:hover:bg-zinc-700 transition-colors shadow-sm"
                    >
                        {t('common.close', 'Close')}
                    </button>
                </div>
            </div>
        </div>
    );
};

interface StatCardProps {
    label: string;
    value: number;
    icon: React.ReactNode;
    color: 'blue' | 'green' | 'purple';
    variant?: 'primary' | 'secondary';
}

const StatCard: React.FC<StatCardProps> = ({ label, value, icon, color, variant = 'primary' }) => {
    const colorStyles = {
        blue: 'text-blue-600 dark:text-blue-400 bg-blue-50 dark:bg-blue-900/20 border-blue-100 dark:border-blue-900/30',
        green: 'text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-900/20 border-emerald-100 dark:border-emerald-900/30',
        purple: 'text-purple-600 dark:text-purple-400 bg-purple-50 dark:bg-purple-900/20 border-purple-100 dark:border-purple-900/30',
    };

    return (
        <div className={cn(
            "flex flex-col p-3 rounded-lg border transition-all",
            variant === 'primary' 
                ? colorStyles[color] 
                : "bg-zinc-50 dark:bg-zinc-800/50 border-zinc-200 dark:border-zinc-700"
        )}>
            <div className="flex items-center gap-1.5 mb-1 opacity-80">
                {React.isValidElement<{ className?: string }>(icon) ? React.cloneElement(icon, { className: "w-3.5 h-3.5" }) : icon}
                <span className="text-xs font-medium">{label}</span>
            </div>
            <span className={cn(
                "text-lg font-bold tabular-nums tracking-tight",
                variant === 'secondary' && "text-zinc-700 dark:text-zinc-300"
            )}>
                {value.toLocaleString()}
            </span>
        </div>
    );
};

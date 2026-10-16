import React from 'react';
import { X } from 'lucide-react';

interface AgentOptimizerProps {
    isOpen: boolean;
    onClose: () => void;
}

export const AgentOptimizer: React.FC<AgentOptimizerProps> = ({ isOpen, onClose }) => {
    if (!isOpen) return null;

    return (
        <div className="fixed inset-0 z-50 bg-black/50 flex items-center justify-center pointer-events-auto">
            <div className="w-[90vw] h-[90vh] bg-white dark:bg-zinc-900 rounded-xl shadow-2xl flex flex-col overflow-hidden border border-zinc-200 dark:border-zinc-800">
                {/* Header */}
                <div className="flex items-center justify-between px-6 py-4 border-b border-zinc-200 dark:border-zinc-800 bg-zinc-50 dark:bg-zinc-900/50">
                    <div>
                        <h2 className="text-xl font-bold text-zinc-900 dark:text-zinc-100">Agent Optimizer</h2>
                        <p className="text-sm text-zinc-500 dark:text-zinc-400">Optimize and debug agent behavior</p>
                    </div>
                    <button 
                        onClick={onClose}
                        className="p-2 text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-200 dark:hover:bg-zinc-800 rounded-lg transition-colors"
                    >
                        <X className="w-5 h-5" />
                    </button>
                </div>

                {/* Content */}
                <div className="flex-1 p-6 overflow-auto">
                    <div className="flex items-center justify-center h-full text-zinc-500">
                        Agent Optimizer Content Coming Soon...
                    </div>
                </div>
            </div>
        </div>
    );
};

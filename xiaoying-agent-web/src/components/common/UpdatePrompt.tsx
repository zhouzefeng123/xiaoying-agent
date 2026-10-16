import React from 'react';
import { RefreshCw } from 'lucide-react';

interface UpdatePromptProps {
    onInstall: () => void;
    onDismiss: () => void;
    version?: string;
}

export const UpdatePrompt: React.FC<UpdatePromptProps> = ({ onInstall }) => {
    return (
        <button
            onClick={onInstall}
            className="fixed bottom-4 left-4 z-[100] animate-in slide-in-from-bottom-4 duration-300 flex items-center gap-1.5 px-3 py-1.5 bg-white/90 dark:bg-zinc-800/90 backdrop-blur-sm border border-zinc-200 dark:border-zinc-700 text-zinc-800 dark:text-zinc-200 hover:bg-zinc-100 dark:hover:bg-zinc-700 rounded-lg shadow-sm transition-colors text-xs font-medium"
        >
            <RefreshCw className="w-3 h-3" />
            重启应用以应用更新
        </button>
    );
};

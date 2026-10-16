import { useEffect, useRef } from 'react';
import { Check, Loader2, X } from 'lucide-react';

interface TextPromptDialogProps {
    isOpen: boolean;
    title: string;
    description: string;
    value: string;
    placeholder?: string;
    confirmLabel: string;
    cancelLabel: string;
    isSubmitting?: boolean;
    onChange: (value: string) => void;
    onConfirm: () => void | Promise<void>;
    onCancel: () => void;
}

export function TextPromptDialog({
    isOpen,
    title,
    description,
    value,
    placeholder,
    confirmLabel,
    cancelLabel,
    isSubmitting = false,
    onChange,
    onConfirm,
    onCancel,
}: TextPromptDialogProps) {
    const inputRef = useRef<HTMLInputElement | null>(null);

    useEffect(() => {
        if (!isOpen || !inputRef.current) {
            return;
        }

        const frameId = window.requestAnimationFrame(() => {
            inputRef.current?.focus();
            inputRef.current?.select();
        });

        return () => window.cancelAnimationFrame(frameId);
    }, [isOpen]);

    if (!isOpen) {
        return null;
    }

    return (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm animate-in fade-in duration-200">
            <div className="w-full max-w-md rounded-xl border border-zinc-200 bg-white p-6 shadow-2xl dark:border-zinc-800 dark:bg-zinc-900">
                <div>
                    <h3 className="text-lg font-semibold text-zinc-900 dark:text-zinc-100">
                        {title}
                    </h3>
                    <p className="mt-1 text-sm text-zinc-600 dark:text-zinc-400">
                        {description}
                    </p>
                </div>

                <input
                    ref={inputRef}
                    value={value}
                    placeholder={placeholder}
                    disabled={isSubmitting}
                    onChange={(event) => onChange(event.target.value)}
                    onKeyDown={(event) => {
                        if (event.key === 'Enter') {
                            event.preventDefault();
                            void onConfirm();
                        }
                        if (event.key === 'Escape') {
                            event.preventDefault();
                            onCancel();
                        }
                    }}
                    className="mt-4 w-full rounded-lg border border-zinc-300 bg-white px-3 py-2 text-sm text-zinc-900 outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 disabled:cursor-not-allowed disabled:opacity-60 dark:border-zinc-700 dark:bg-zinc-950 dark:text-zinc-100"
                />

                <div className="mt-6 flex justify-end gap-3">
                    <button
                        onClick={onCancel}
                        disabled={isSubmitting}
                        className="flex items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium text-zinc-600 transition-colors hover:bg-zinc-200 disabled:cursor-not-allowed disabled:opacity-60 dark:text-zinc-300 dark:hover:bg-zinc-800"
                    >
                        <X className="h-4 w-4" />
                        {cancelLabel}
                    </button>
                    <button
                        onClick={() => void onConfirm()}
                        disabled={isSubmitting}
                        className="flex items-center gap-2 rounded-lg bg-blue-500 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-600 disabled:cursor-not-allowed disabled:opacity-60"
                    >
                        {isSubmitting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Check className="h-4 w-4" />}
                        {confirmLabel}
                    </button>
                </div>
            </div>
        </div>
    );
}
import React, { useRef, useEffect, useState } from 'react';
import { Send, Paperclip, X, Square, Mic, MicOff, Loader2, ChevronDown, FolderOpen, FileText, File, TerminalSquare, Code2, Cpu, ShieldCheck } from 'lucide-react';
import { cn } from '../../lib/utils';
import { ChatCard } from './MessageBubble';
import { CHAT_MODEL_OPTIONS, type ChatModelSelection, getChatModelOption } from '../../lib/chat-models';

interface ChatInputProps {
    variant?: 'chat' | 'welcome';
    value: string;
    onChange: (value: string) => void;
    selectedModel: ChatModelSelection;
    onSelectedModelChange: (model: ChatModelSelection) => void;
    attachments: File[];
    onAttachmentsChange: (files: File[]) => void;
    onSendMessage: (content: string, attachments?: File[]) => void;
    onStop?: () => void;
    isGenerating?: boolean;
    disabled?: boolean;
    
    // Transcription props
    isRecording: boolean;
    isTranscribing: boolean;
    onStartRecording: () => void;
    onStopRecording: () => void;
    
    // Tool Status
    activeOperations?: Map<string, { activity: string; file?: string }>;
    contextWindowAccessory?: React.ReactNode;
    mode: 'normal' | 'coding';
    onModeChange: (mode: 'normal' | 'coding') => void;
    isReadOnlyMode: boolean;
    onReadOnlyModeChange: (enabled: boolean) => void;
    onToggleCodingPanel?: () => void;
}

const ChatInputComponent: React.FC<ChatInputProps> = ({ 
    variant = 'chat',
    value,
    onChange,
    selectedModel,
    onSelectedModelChange,
    attachments,
    onAttachmentsChange,
    onSendMessage, 
    onStop, 
    isGenerating, 
    disabled,
    isRecording,
    isTranscribing,
    onStartRecording,
    onStopRecording,
    activeOperations,
    contextWindowAccessory,
    mode,
    onModeChange,
    isReadOnlyMode,
    onReadOnlyModeChange,
    onToggleCodingPanel
}) => {
    const textareaRef = useRef<HTMLTextAreaElement>(null);
    const prevIsRecordingRef = useRef(isRecording);
    const prevIsTranscribingRef = useRef(isTranscribing);
    const [isDragOver, setIsDragOver] = useState(false);
    const [isModelMenuOpen, setIsModelMenuOpen] = useState(false);
    const modelMenuRef = useRef<HTMLDivElement>(null);
    const [customModelName, setCustomModelName] = useState<string>('');

    useEffect(() => {
        let cancelled = false;
        fetch('/v1/config')
            .then((res) => res.ok ? res.json() : null)
            .then((data) => {
                if (!cancelled && data?.main_model?.model) {
                    setCustomModelName(data.main_model.model.trim());
                }
            })
            .catch(() => {});
        return () => { cancelled = true; };
    }, []);

    // Store file paths received from preload's drop interceptor
    const filePathMapRef = useRef<Map<string, string>>(new Map());
    const getDropFileKey = (file: { name: string; size: number; type?: string }) =>
        `${file.name}::${file.size}::${file.type || ''}`;

    // Register preload's file drop callback to capture absolute paths
    useEffect(() => {
        if (window.electron && (window.electron as any).onFileDrop) {
            (window.electron as any).onFileDrop((files: Array<{ name: string; path: string; type: string; size: number }>) => {
                console.log('[ChatInput] onFileDrop received from preload:', files);
                filePathMapRef.current.clear();
                files.forEach(f => {
                    filePathMapRef.current.set(getDropFileKey(f), f.path);
                    filePathMapRef.current.set(`name:${f.name}`, f.path);
                });
            });
        }
    }, []);

    // Auto-resize textarea
    useEffect(() => {
        if (textareaRef.current) {
            textareaRef.current.style.height = 'auto';
            textareaRef.current.style.height = `${Math.min(textareaRef.current.scrollHeight, 200)}px`;
        }
    }, [value]);

    useEffect(() => {
        if (!isModelMenuOpen) {
            return;
        }

        const handlePointerDown = (event: MouseEvent) => {
            if (modelMenuRef.current && !modelMenuRef.current.contains(event.target as Node)) {
                setIsModelMenuOpen(false);
            }
        };

        document.addEventListener('mousedown', handlePointerDown);
        return () => document.removeEventListener('mousedown', handlePointerDown);
    }, [isModelMenuOpen]);

    // Keep cursor in input after voice input/transcription finishes
    useEffect(() => {
        const recordingJustStopped = prevIsRecordingRef.current && !isRecording;
        const transcribingJustFinished = prevIsTranscribingRef.current && !isTranscribing;

        if ((recordingJustStopped || transcribingJustFinished) && textareaRef.current && !disabled) {
            // Delay to ensure parent state update (transcribed text) has been applied first
            requestAnimationFrame(() => {
                if (textareaRef.current && !disabled) {
                    textareaRef.current.focus();
                    const length = textareaRef.current.value.length;
                    textareaRef.current.setSelectionRange(length, length);
                }
            });
        }

        prevIsRecordingRef.current = isRecording;
        prevIsTranscribingRef.current = isTranscribing;
    }, [isRecording, isTranscribing, disabled]);

    const handleKeyDown = (e: React.KeyboardEvent) => {
        if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            handleSend();
        }
    };

    const handleSend = () => {
        if ((!value.trim() && attachments.length === 0) || disabled || isTranscribing) return;
        
        onSendMessage(value, attachments);
        // Clearing is handled by parent
        
        // Reset height
        if (textareaRef.current) {
            textareaRef.current.style.height = 'auto';
        }
    };

    const handlePaste = (e: React.ClipboardEvent) => {
        const items = e.clipboardData.items;
        for (let i = 0; i < items.length; i++) {
            if (items[i].type.indexOf('image') !== -1) {
                const blob = items[i].getAsFile();
                if (blob) {
                    onAttachmentsChange([...attachments, blob]);
                }
            }
        }
    };

    const removeAttachment = (index: number) => {
        onAttachmentsChange(attachments.filter((_, i) => i !== index));
    };

    // Get file icon and color based on file type
    const getFileIcon = (file: File) => {
        const fileName = file.name.toLowerCase();
        const ext = fileName.split('.').pop() || '';
        const hasDot = file.name.includes('.');
        
        // Check if it's a directory/folder (no dot in name AND type is empty, or specific directory types)
        const isDirectory = (!hasDot && !file.type) || file.type === 'inode/directory' || file.type === 'application/x-directory';
        if (isDirectory) {
            return { icon: FolderOpen, color: 'text-yellow-600', bgColor: 'bg-yellow-50 dark:bg-yellow-900/20' };
        }
        
        // Document types
        const docTypes = ['pdf', 'doc', 'docx', 'txt', 'rtf', 'odt', 'wps'];
        const spreadsheetTypes = ['xls', 'xlsx', 'csv', 'ods', 'et'];
        const presentationTypes = ['ppt', 'pptx', 'odp', 'dps'];
        const codeTypes = ['js', 'ts', 'jsx', 'tsx', 'py', 'java', 'go', 'cpp', 'c', 'h', 'html', 'css', 'json', 'xml', 'md', 'vue', 'svelte', 'rb', 'php', 'rs', 'swift', 'kt', 'sh', 'bat', 'ps1'];
        const archiveTypes = ['zip', 'rar', '7z', 'tar', 'gz', 'bz2', 'xz'];
        const imageTypes = ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'svg', 'webp', 'ico', 'tiff'];
        const videoTypes = ['mp4', 'avi', 'mkv', 'mov', 'wmv', 'flv', 'webm'];
        const audioTypes = ['mp3', 'wav', 'flac', 'aac', 'ogg', 'wma', 'm4a'];
        
        if (docTypes.includes(ext)) {
            return { icon: FileText, color: 'text-blue-500', bgColor: 'bg-blue-50 dark:bg-blue-900/20' };
        }
        if (spreadsheetTypes.includes(ext)) {
            return { icon: FileText, color: 'text-green-500', bgColor: 'bg-green-50 dark:bg-green-900/20' };
        }
        if (presentationTypes.includes(ext)) {
            return { icon: FileText, color: 'text-orange-500', bgColor: 'bg-orange-50 dark:bg-orange-900/20' };
        }
        if (codeTypes.includes(ext)) {
            return { icon: TerminalSquare, color: 'text-purple-500', bgColor: 'bg-purple-50 dark:bg-purple-900/20' };
        }
        if (archiveTypes.includes(ext)) {
            return { icon: File, color: 'text-yellow-500', bgColor: 'bg-yellow-50 dark:bg-yellow-900/20' };
        }
        if (imageTypes.includes(ext)) {
            return { icon: FileText, color: 'text-pink-500', bgColor: 'bg-pink-50 dark:bg-pink-900/20' };
        }
        if (videoTypes.includes(ext)) {
            return { icon: FileText, color: 'text-red-500', bgColor: 'bg-red-50 dark:bg-red-900/20' };
        }
        if (audioTypes.includes(ext)) {
            return { icon: FileText, color: 'text-indigo-500', bgColor: 'bg-indigo-50 dark:bg-indigo-900/20' };
        }
        
        // Default file icon
        return { icon: File, color: 'text-zinc-400', bgColor: 'bg-zinc-100 dark:bg-zinc-800' };
    };

    // Drag and Drop handlers
    const handleDragOver = (e: React.DragEvent) => {
        e.preventDefault();
        e.stopPropagation();
        setIsDragOver(true);
    };

    const handleDragLeave = (e: React.DragEvent) => {
        e.preventDefault();
        e.stopPropagation();
        setIsDragOver(false);
    };

    const handleDrop = (e: React.DragEvent) => {
        e.preventDefault();
        e.stopPropagation();
        setIsDragOver(false);

        // 1. Gather all possible File objects
        let files: File[] = [];
        if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
            files = Array.from(e.dataTransfer.files);
        } else if (e.dataTransfer.items && e.dataTransfer.items.length > 0) {
            files = Array.from(e.dataTransfer.items)
                .map((item) => (item.kind === 'file' ? item.getAsFile() : null))
                .filter((file): file is File => file !== null);
        }

        // 2. Consume preload's drop info cache
        const dropInfos = (window.electron && (window.electron as any).consumeDroppedFiles)
            ? ((window.electron as any).consumeDroppedFiles() as Array<{ name: string; path: string; type: string; size: number }>)
            : [];
        
        const dropInfoMap = new Map<string, string>();
        dropInfos.forEach((info) => {
            dropInfoMap.set(getDropFileKey(info), info.path);
            dropInfoMap.set(`name:${info.name}`, info.path);
        });

        // 3. If we got File objects from React event, attach paths
        if (files.length > 0) {
            files.forEach(file => {
                const storedPath =
                    ((file as any).path as string | undefined) ||
                    dropInfoMap.get(getDropFileKey(file)) ||
                    dropInfoMap.get(`name:${file.name}`) ||
                    filePathMapRef.current.get(getDropFileKey(file)) ||
                    filePathMapRef.current.get(`name:${file.name}`);
                if (storedPath) {
                    (file as any)._absolutePath = storedPath;
                    console.log('[Drop] Attached path to file:', file.name, '->', storedPath);
                } else {
                    console.warn('[Drop] No path found for:', file.name);
                }
            });
            onAttachmentsChange([...attachments, ...files]);
            return;
        }

        // 4. Fallback: If React event gave NO files, rebuild them from preload cache
        if (dropInfos.length > 0) {
            console.log('[Drop] React event gave no files, rebuilding from preload cache');
            const rebuiltFiles = dropInfos.map((info) => {
                const file = new (window as any).File([new Blob([], { type: info.type })], info.name, { type: info.type }) as File;
                (file as any)._absolutePath = info.path;
                return file;
            });
            onAttachmentsChange([...attachments, ...rebuiltFiles]);
        }
    };

    const handleMicClick = (e: React.MouseEvent) => {
        e.stopPropagation();
        if (isRecording) {
            onStopRecording();
            return;
        } else {
            if (isGenerating && onStop) {
                onStop();
            }
            onStartRecording();
        }
    };

    const showStopButton = isGenerating && !value.trim() && attachments.length === 0;
    const selectedModelOption = getChatModelOption(selectedModel);

    return (
        <div 
            className={cn(
                "relative z-30 bg-transparent",
                variant === 'welcome' ? 'xiaoying-chat-input' : 'border-t border-zinc-200/50 p-4 dark:border-zinc-800/50',
                mode === 'coding' && "coding-input-surface"
            )}
            onDragOver={handleDragOver}
            onDragLeave={handleDragLeave}
            onDrop={handleDrop}
        >
            {/* Tool Status Bar */}
            {activeOperations && activeOperations.size > 0 && (
                <div className="max-w-3xl mx-auto mb-2 flex flex-wrap gap-2">
                    {Array.from(activeOperations.entries()).map(([id, op]) => (
                        <div key={id} className="flex items-center gap-2 rounded-full border border-blue-100 bg-blue-50 px-3 py-1.5 text-xs text-blue-600 dark:border-blue-800 dark:bg-blue-900/20 dark:text-blue-300">
                            <Loader2 className="w-3 h-3" />
                            <span className="font-medium">{op.activity}</span>
                            {op.file && <span className="opacity-70 truncate max-w-[150px]">{op.file}</span>}
                        </div>
                    ))}
                </div>
            )}

            <div className="chat-composer-rail w-full relative">
                {/* Drag Overlay */}
                {isDragOver && (
                    <div className="fixed inset-0 z-50 flex items-center justify-center border-2 border-dashed border-blue-500 bg-blue-500/10 dark:bg-blue-400/10 pointer-events-none">
                        <div className="flex flex-col items-center gap-2 text-blue-600 dark:text-blue-400">
                            <Paperclip className="w-12 h-12" />
                            <span className="text-lg font-medium">释放文件添加附件</span>
                        </div>
                    </div>
                )}
                {/* Attachments Preview */}
                {attachments.length > 0 && (
                    <div className="flex gap-2 mb-2 overflow-x-auto pb-2">
                        {attachments.map((file, index) => {
                            const isImage = file.type.startsWith('image/');
                            const fileIconInfo = getFileIcon(file);
                            const FileIcon = fileIconInfo.icon;
                            
                            return (
                                <div key={index} className="relative group flex-shrink-0">
                                    <div className={cn(
                                        "w-20 h-20 rounded-lg border overflow-hidden flex flex-col",
                                        isImage ? "border-zinc-200 dark:border-zinc-700" : `${fileIconInfo.bgColor} border-zinc-200 dark:border-zinc-700`
                                    )}>
                                        {isImage ? (
                                            <>
                                                <img 
                                                    src={URL.createObjectURL(file)} 
                                                    alt={file.name} 
                                                    className="w-full h-16 object-cover" 
                                                />
                                                <div className="h-4 bg-white/80 dark:bg-zinc-900/80 flex items-center justify-center px-1">
                                                    <span className="text-[8px] text-zinc-600 dark:text-zinc-400 truncate w-full text-center">
                                                        {file.name}
                                                    </span>
                                                </div>
                                            </>
                                        ) : (
                                            <div className="w-full h-full flex flex-col items-center justify-center gap-1 p-1">
                                                <FileIcon className={cn("w-6 h-6", fileIconInfo.color)} />
                                                <span className="text-[9px] text-zinc-600 dark:text-zinc-400 truncate w-full text-center leading-tight">
                                                    {file.name}
                                                </span>
                                            </div>
                                        )}
                                    </div>
                                    <button
                                        onClick={() => removeAttachment(index)}
                                        className="absolute -top-1 -right-1 rounded-full bg-red-500 p-0.5 text-white opacity-0 group-hover:opacity-100"
                                    >
                                        <X className="w-3 h-3" />
                                    </button>
                                </div>
                            );
                        })}
                    </div>
                )}

                <ChatCard 
                    allowOverflowVisible={true}
                    className={cn(
                        "relative z-30 overflow-visible cursor-text transition-all duration-300",
                        variant === 'welcome' && 'xiaoying-composer-card',
                        mode === 'coding' && "border-cyan-300/40 bg-white/90 shadow-[0_18px_45px_rgba(8,145,178,0.12)] dark:border-cyan-400/20 dark:bg-zinc-950/85",
                        disabled && "opacity-50 cursor-not-allowed",
                        isRecording && "ring-2 ring-red-500/20 border-red-500/50"
                    )}
                    contentClassName={cn(
                        "w-full flex items-center gap-2 px-3 py-2",
                        variant === 'welcome' && 'xiaoying-composer-content'
                    )}
                    onClick={(e) => {
                        // Prevent focusing if we clicked a button inside the card
                        if ((e.target as HTMLElement).closest('button')) {
                            return;
                        }
                        if (textareaRef.current && !disabled && !isTranscribing) {
                            textareaRef.current.focus();
                        }
                    }}
                >
                    <div className="relative shrink-0 self-center" ref={modelMenuRef}>
                        <button
                            onClick={() => setIsModelMenuOpen((open) => !open)}
                            className={cn(
                                "flex items-center gap-2 rounded-lg border px-3 py-1.5 text-xs font-medium",
                                isModelMenuOpen
                                    ? "border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-500/40 dark:bg-blue-500/10 dark:text-blue-300"
                                    : "border-zinc-200 bg-white text-zinc-600 hover:border-zinc-300 hover:bg-zinc-50 dark:border-zinc-700 dark:bg-zinc-900/80 dark:text-zinc-300 dark:hover:border-zinc-600 dark:hover:bg-zinc-800"
                            )}
                            title={`当前模型：${selectedModelOption.label}`}
                        >
                            <span className="max-w-[96px] truncate">{selectedModelOption.label}</span>
                            <ChevronDown className={cn("h-4 w-4", isModelMenuOpen && "rotate-180")} />
                        </button>

                        {isModelMenuOpen && (
                            <div className="absolute bottom-full left-0 z-[120] mb-2 w-72 overflow-hidden rounded-xl border border-zinc-200 bg-white dark:border-zinc-700 dark:bg-zinc-900">
                                <div className="border-b border-zinc-100 px-3 py-2 text-xs font-medium text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
                                    选择对话模型
                                </div>
                                <div className="p-1.5">
                                    {CHAT_MODEL_OPTIONS.map((option) => {
                                        const isActive = option.id === selectedModel;
                                        return (
                                            <button
                                                key={option.id}
                                                onClick={() => {
                                                    onSelectedModelChange(option.id);
                                                    setIsModelMenuOpen(false);
                                                }}
                                                className={cn(
                                                    "flex w-full flex-col items-start gap-1 rounded-lg px-3 py-2 text-left",
                                                    isActive
                                                        ? "bg-blue-50 text-blue-700 dark:bg-blue-500/10 dark:text-blue-300"
                                                        : "text-zinc-700 hover:bg-zinc-50 dark:text-zinc-200 dark:hover:bg-zinc-800"
                                                )}
                                            >
                                                <span className="text-sm font-medium">{option.label}</span>
                                                {option.id === 'custom-main-model' ? (
                                                    customModelName ? (
                                                        <span className="text-xs text-zinc-500 dark:text-zinc-400">{customModelName}</span>
                                                    ) : null
                                                ) : option.description ? (
                                                    <span className="text-xs text-zinc-500 dark:text-zinc-400">{option.description}</span>
                                                ) : null}
                                            </button>
                                        );
                                    })}
                                </div>
                            </div>
                        )}
                    </div>

                    {/* Attachment Button */}
                    <input 
                        type="file" 
                        multiple 
                        className="hidden" 
                        ref={() => {}}
                        id="file-upload-input"
                        onChange={(e) => {
                            if (e.target.files) {
                                const files = Array.from(e.target.files);
                                onAttachmentsChange([...attachments, ...files]);
                                e.target.value = ''; // Reset
                            }
                        }}
                    />
                    <button 
                        className="shrink-0 self-center p-1.5 text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-300"
                        title="添加附件"
                        onClick={async () => {
                            // Try Electron dialog first (provides full absolute paths)
                            if (window.electron && (window.electron as any).selectFiles) {
                                const fileInfos = await (window.electron as any).selectFiles();
                                if (fileInfos && fileInfos.length > 0) {
                                    // Create File-like objects with paths
                                    const files = fileInfos.map((fi: any) => {
                                        const blob = new Blob([], { type: fi.type });
                                        const file = new (window as any).File([blob], fi.name, { type: fi.type }) as File;
                                        (file as any)._absolutePath = fi.path;
                                        return file;
                                    });
                                    onAttachmentsChange([...attachments, ...files]);
                                    return;
                                }
                            }
                            // Fallback to native file input
                            document.getElementById('file-upload-input')?.click();
                        }}
                    >
                        <Paperclip className="w-5 h-5" />
                    </button>

                    {/* Voice Input Button */}
                    <button 
                        onClick={handleMicClick}
                        className={cn(
                            "shrink-0 self-center p-1.5",
                            isRecording 
                                ? "text-red-500 hover:text-red-600" 
                                : "text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-300"
                        )}
                        title={isRecording ? "点击结束语音输入" : "开始语音输入"}
                        disabled={isTranscribing}
                    >
                        {isTranscribing ? (
                            <Loader2 className="w-5 h-5 text-blue-500" />
                        ) : isRecording ? (
                            <MicOff className="w-5 h-5" />
                        ) : (
                            <Mic className="w-5 h-5" />
                        )}
                    </button>

                    {/* Text Area */}
                    <textarea
                        id="chat-composer-input"
                        ref={textareaRef}
                        value={value}
                        onChange={(e) => onChange(e.target.value)}
                        onKeyDown={handleKeyDown}
                        onPaste={handlePaste}
                        placeholder={
                            isTranscribing ? "正在转换文字..." : 
                            isRecording ? "正在聆听..." : 
                            isGenerating ? "正在思考..." : 
                            variant === 'welcome'
                                ? (mode === 'coding' ? '描述你想开发的功能，或选择一个项目开始协作…' : '今天帮你做些什么？@ 添加上下文，/ 调用技能与指令')
                                : "输入消息..."
                        }
                        className="min-h-[24px] flex-1 bg-transparent border-none py-1 text-sm leading-5 text-zinc-900 placeholder:text-zinc-400 outline-none resize-none focus:ring-0 dark:text-zinc-100 disabled:opacity-50 max-h-[200px]"
                        rows={1}
                        disabled={disabled || isTranscribing}
                    />

                    {/* Send / Stop Button */}
                    <button
                        onClick={showStopButton ? onStop : handleSend}
                        disabled={(!value.trim() && attachments.length === 0 && !isGenerating) || disabled}
                        aria-label={showStopButton ? "停止生成" : "发送消息"}
                        className={cn(
                            "shrink-0 self-center rounded-lg p-1.5",
                            showStopButton
                                ? "bg-red-500 text-white hover:bg-red-600"
                                : ((value.trim() || attachments.length > 0) && !disabled
                                    ? "bg-blue-500 text-white hover:bg-blue-600"
                                    : "bg-transparent text-zinc-300 dark:text-zinc-600 cursor-not-allowed")
                        )}
                        title={showStopButton ? "停止生成" : "发送消息"}
                    >
                        {showStopButton ? (
                            <Square className="w-5 h-5 fill-current" />
                        ) : (
                            <Send className="w-5 h-5" />
                        )}
                    </button>
                </ChatCard>

                {/* Bottom Bar for Mode & Accessories */}
                <div className={cn("mt-2.5 flex items-center justify-between px-1", variant === 'welcome' && 'xiaoying-composer-options')}>
                    <div className="flex items-center gap-3">
                        <div
                            className={cn(
                                "group coding-mode-button flex cursor-pointer items-center gap-2 px-2.5 py-1.5 rounded-lg transition-all duration-300 border",
                                mode === 'coding'
                                    ? "is-active border-cyan-200/80 text-slate-800 shadow-[0_10px_28px_rgba(8,145,178,0.20)] dark:border-cyan-400/25 dark:text-cyan-50"
                                    : "is-idle border-zinc-200/80 bg-white/70 text-zinc-600 shadow-sm hover:-translate-y-px hover:border-cyan-200 hover:bg-cyan-50/70 hover:text-cyan-800 hover:shadow-[0_8px_22px_rgba(8,145,178,0.12)] active:translate-y-0 dark:border-zinc-700/70 dark:bg-zinc-900/70 dark:text-zinc-300 dark:hover:border-cyan-400/25 dark:hover:bg-cyan-950/25 dark:hover:text-cyan-100"
                            )}
                            title={mode === 'coding' ? "Coding 模式已开启 (语义理解 & LSP 状态机增强)" : "开启 Coding 模式"}
                        >
                            <button
                                type="button"
                                aria-pressed={mode === 'coding'}
                                onClick={() => onModeChange(mode === 'coding' ? 'normal' : 'coding')}
                                className="relative z-10 flex items-center gap-2 text-inherit"
                            >
                                <div className={cn(
                                    "coding-icon-shell flex items-center justify-center w-6 h-6 rounded transition-all duration-300",
                                    mode === 'coding' ? "is-active" : "is-idle bg-zinc-100 text-zinc-500 dark:bg-zinc-800"
                                )}>
                                    <Code2 className={cn("relative z-10 w-3.5 h-3.5", mode === 'coding' ? "text-white drop-shadow-[0_1px_2px_rgba(0,0,0,0.35)]" : "text-zinc-500")} />
                                </div>
                                <span className="text-[11px] font-bold uppercase tracking-[0]">Coding</span>
                            </button>
                            {mode === 'coding' && (
                                <div className="relative z-10 ml-1 flex items-center gap-1.5 border-l border-cyan-200/80 pl-2 dark:border-cyan-400/20">
                                    <div className="h-1.5 w-1.5 rounded-full bg-emerald-400 shadow-[0_0_10px_rgba(52,211,153,0.85)] animate-[pulse_2s_infinite]" />
                                    <button
                                        type="button"
                                        onClick={(e) => {
                                            e.stopPropagation();
                                            onToggleCodingPanel?.();
                                        }}
                                        className="rounded p-0.5 text-cyan-700 transition-colors hover:bg-cyan-100/80 dark:text-cyan-200 dark:hover:bg-cyan-400/10 group/panel"
                                        title="切换代码面板显示"
                                    >
                                        <Cpu className="h-3.5 w-3.5 transition-colors" />
                                    </button>
                                </div>
                            )}
                        </div>
                        <button
                            type="button"
                            aria-pressed={isReadOnlyMode}
                            onClick={() => onReadOnlyModeChange(!isReadOnlyMode)}
                            className={cn(
                                "group flex items-center gap-2 rounded-lg border px-2.5 py-1.5 transition-all duration-300",
                                isReadOnlyMode
                                    ? "border-emerald-200/80 bg-emerald-50 text-emerald-800 shadow-[0_10px_24px_rgba(16,185,129,0.16)] dark:border-emerald-400/25 dark:bg-emerald-500/10 dark:text-emerald-100"
                                    : "border-zinc-200/80 bg-white/70 text-zinc-600 shadow-sm hover:-translate-y-px hover:border-emerald-200 hover:bg-emerald-50/70 hover:text-emerald-800 hover:shadow-[0_8px_22px_rgba(16,185,129,0.12)] active:translate-y-0 dark:border-zinc-700/70 dark:bg-zinc-900/70 dark:text-zinc-300 dark:hover:border-emerald-400/25 dark:hover:bg-emerald-950/25 dark:hover:text-emerald-100"
                            )}
                            title={isReadOnlyMode ? "只读模式已开启" : "开启只读模式"}
                        >
                            <span className={cn(
                                "flex h-6 w-6 items-center justify-center rounded transition-colors",
                                isReadOnlyMode
                                    ? "bg-emerald-500 text-white shadow-[0_0_14px_rgba(16,185,129,0.45)]"
                                    : "bg-zinc-100 text-zinc-500 dark:bg-zinc-800"
                            )}>
                                <ShieldCheck className="h-3.5 w-3.5" />
                            </span>
                            <span className="text-[11px] font-bold uppercase tracking-[0]">Read Only</span>
                        </button>
                    </div>

                    <div className="flex items-center gap-2">
                        {isTranscribing && (
                            <div className="flex items-center gap-1.5 px-2 py-1 rounded-md bg-blue-50 dark:bg-blue-900/20 text-blue-600 dark:text-blue-400 border border-blue-100 dark:border-blue-800">
                                <Loader2 className="w-3 h-3 animate-spin" />
                                <span className="text-[10px] font-medium">正在识别...</span>
                            </div>
                        )}
                        <div className="flex items-center">
                            {contextWindowAccessory}
                        </div>
                    </div>
                </div>

                <div className={cn("mt-2 grid grid-cols-[1fr_auto_1fr] items-center gap-2 text-xs text-zinc-400 dark:text-zinc-500", variant === 'welcome' && 'xiaoying-composer-hint')}>
                    <span aria-hidden="true" />
                    <span className="text-center">{isRecording ? "点击麦克风即可结束录音，静音约0.8秒也会自动结束" : null}</span>
                    <div className="flex min-w-0 justify-end pr-3" />
                </div>
            </div>
        </div>
    );
};

export const ChatInput = React.memo(ChatInputComponent);

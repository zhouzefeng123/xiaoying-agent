import { useState, useEffect, useCallback, useRef, type CSSProperties, type MouseEvent as ReactMouseEvent } from 'react'
import 'regenerator-runtime/runtime'
import './App.css'
import { ChatArea } from './components/chat/ChatArea'
import { ChatInput } from './components/chat/ChatInput'
import { SettingsModal } from './components/settings/SettingsModal'
import { TokenStatsDialog } from './components/settings/TokenStatsDialog'
import { ConfirmationDialog } from './components/layout/ConfirmationDialog'
import { TextPromptDialog } from './components/layout/TextPromptDialog'
import { Toaster, toast } from 'sonner'
import { useChat } from './hooks/use-chat'
import { Settings, X, Ear, Activity, Sparkles, Brain, Minus, Bot, FolderOpen, TerminalSquare, Square, Copy, Code2, BriefcaseBusiness, ArrowUpRight } from 'lucide-react'
import { cn } from './lib/utils'
import { NotificationCenter } from './components/notifications/NotificationCenter'
import { ActiveTasksCenter } from './components/notifications/ActiveTasksCenter'
import { useTranscription } from './hooks/use-transcription'
import { useTTS } from './hooks/use-tts'
import { useWakeWord } from './hooks/use-wake-word'
import { AudioGlow } from './components/effects/AudioGlow'
import { AuraStatusButton } from './components/effects/AuraStatusButton'
import { MemoryVizModal } from './components/settings/MemoryVizModal'
import { CognitiveMonitor } from './components/cognitive/CognitiveMonitor'
import { CodeBrowserOverlay } from './components/coding/CodeBrowserOverlay'
import { AgentOptimizer } from './components/optimizer/AgentOptimizer'
import { WorkerSnapshotsPanel } from './components/worker/WorkerSnapshotsPanel'
import { AppSidebar } from './components/layout/AppSidebar'
import { TerminalPanel } from './components/terminal/TerminalPanel'
import { Login } from './components/auth/Login'
import { UpdatePrompt } from './components/common/UpdatePrompt'
import { ContextWindowButton } from './components/context/ContextWindowButton'
import { CHAT_MODEL_OPTIONS, DEFAULT_CHAT_MODEL_SELECTION, recordResolvedChatModelObservation, resolveChatModelAttemptSequence, shouldRetryAutoModelAttempt, type ChatModelSelection } from './lib/chat-models'
import { WordEditorPanel, type WordEditorPanelHandle } from './components/word-editor/WordEditorPanel'
import { closeWordEditor, openWordEditor, useWordEditorSelector } from './components/word-editor/word-editor-bus'
import {
    MAX_WORD_AUTO_RELOCATE_ATTEMPTS,
    buildWordAutoRelocateFingerprint,
    buildWordAutoRelocateIssueKey,
    buildWordAutoRelocatePrompt,
    isWordApplyIssueAutoRelocatable,
} from './components/word-editor/word-auto-relocate'
import {
    APP_SIDEBAR_WIDTH,
    COLLAPSED_WINDOW_HEIGHT,
    COLLAPSED_WINDOW_WIDTH,
    CODER_PANEL_WIDTH,
    EXPANDED_WINDOW_HEIGHT,
    EXPANDED_WINDOW_WIDTH,
    EXPANDED_WINDOW_WIDTH_WITH_PANEL,
    WORD_PANEL_WIDTH,
} from './shared/window-size'

import { useAuth } from './contexts/useAuth'
import { Loader2 } from 'lucide-react'
import { Logo } from './components/common/Logo'

const CHAT_MODEL_STORAGE_KEY = 'auramate.chat.selected_model';
const SUBAGENT_TOGGLE_STORAGE_KEY = 'auramate.chat.enable_subagents';
const LEGACY_READ_ONLY_MODE_STORAGE_KEY = 'auramate.chat.read_only_mode';
const READ_ONLY_MODE_STORAGE_KEY = 'auramate.chat.manual_read_only_mode';
const INTERNAL_HIDDEN_USER_PROMPT_MARKER = '[AURAMATE_INTERNAL_USER_PROMPT:WORD_AUTO_RELOCATE]';
const DEFAULT_PROJECT_NAME = 'XiaoYing';
const NO_PROJECT_NAME = '未选择项目';
const DEFAULT_APP_SIDEBAR_WIDTH = APP_SIDEBAR_WIDTH;
const DEFAULT_CHAT_PANEL_WIDTH = EXPANDED_WINDOW_WIDTH_WITH_PANEL;
const MIN_APP_SIDEBAR_WIDTH = 220;
const MAX_APP_SIDEBAR_WIDTH = 420;
const MIN_CHAT_PANEL_WIDTH = 380;
const MAX_CHAT_PANEL_WIDTH = 900;

type LayoutResizeTarget = 'sidebar' | 'chat';

type WorkspaceMode = 'normal' | 'coding';

interface XiaoyingWelcomeProps {
    mode: WorkspaceMode;
    onModeChange: (mode: WorkspaceMode) => void;
    onQuickAction: (value: string) => void;
    onOpenCodingWorkspace: () => void;
}

const WELCOME_ACTIONS = [
    { label: '日常开发', icon: BriefcaseBusiness, mode: 'normal' as WorkspaceMode },
    { label: '网站开发', icon: Code2, mode: 'coding' as WorkspaceMode },
    { label: 'Agent 应用', icon: Sparkles, mode: 'coding' as WorkspaceMode },
    { label: '技能开发', icon: ArrowUpRight, mode: 'coding' as WorkspaceMode },
    { label: 'CI/CD', icon: TerminalSquare, mode: 'coding' as WorkspaceMode },
    { label: '文档整理', icon: FolderOpen, mode: 'normal' as WorkspaceMode },
];

function XiaoyingWelcome({ mode, onModeChange, onQuickAction, onOpenCodingWorkspace }: XiaoyingWelcomeProps) {
    return (
        <section className="xiaoying-welcome" aria-label="小英工作区">
            <div className="xiaoying-welcome-aura" aria-hidden="true" />
            <div className="xiaoying-welcome-mark">
                <Logo className="h-full w-full rounded-[18px]" />
            </div>
            <p className="xiaoying-welcome-kicker">你的智能工作伙伴</p>
            <h1><span>小英</span>，今天帮你做什么？</h1>
            <p className="xiaoying-welcome-subtitle">把想法交给我，我会陪你从第一步做到最后一步。</p>

            <div className="xiaoying-mode-switch" role="group" aria-label="工作模式">
                <button
                    type="button"
                    className={cn(mode === 'normal' && 'is-active')}
                    onClick={() => onModeChange('normal')}
                    aria-pressed={mode === 'normal'}
                >
                    <BriefcaseBusiness className="h-4 w-4" />
                    日常办公
                </button>
                <button
                    type="button"
                    className={cn(mode === 'coding' && 'is-active')}
                    onClick={() => onModeChange('coding')}
                    aria-pressed={mode === 'coding'}
                >
                    <Code2 className="h-4 w-4" />
                    代码开发
                </button>
            </div>

            <div className="xiaoying-quick-actions">
                {WELCOME_ACTIONS.map(({ label, icon: Icon, mode: actionMode }) => (
                    <button
                        key={label}
                        type="button"
                        onClick={() => {
                            onModeChange(actionMode);
                            onQuickAction(label);
                        }}
                    >
                        <Icon className="h-4 w-4" />
                        {label}
                    </button>
                ))}
            </div>

            {mode === 'coding' && (
                <button type="button" className="xiaoying-code-workspace-link" onClick={onOpenCodingWorkspace}>
                    <Code2 className="h-4 w-4" />
                    打开代码工作区
                    <ArrowUpRight className="h-3.5 w-3.5" />
                </button>
            )}

        </section>
    );
}

interface WorkspaceStateSnapshot {
    currentPath: string;
    workspacePath: string;
    projectPath: string | null;
}

const getProjectFolderName = (workspacePath?: string | null): string => {
    if (!workspacePath) {
        return DEFAULT_PROJECT_NAME;
    }

    const segments = workspacePath.split(/[\\/]+/).filter(Boolean);
    return segments[segments.length - 1] || workspacePath;
};

const joinPath = (basePath: string, childName: string): string => {
    const normalizedBasePath = basePath.replace(/[\\/]+$/, '');
    if (!normalizedBasePath) {
        return childName;
    }

    return normalizedBasePath.includes('\\')
        ? `${normalizedBasePath}\\${childName}`
        : `${normalizedBasePath}/${childName}`;
};

const normalizeWorkspaceStateSnapshot = (data: { path?: string; workspace_path?: string }): WorkspaceStateSnapshot | null => {
    const currentPath = data.path?.trim();
    const workspacePath = data.workspace_path?.trim() || currentPath;
    if (!currentPath || !workspacePath) {
        return null;
    }

    return {
        currentPath,
        workspacePath,
        projectPath: currentPath === workspacePath ? null : currentPath,
    };
};

const readStoredChatModelSelection = (): ChatModelSelection => {
    if (typeof window === 'undefined') {
        return DEFAULT_CHAT_MODEL_SELECTION;
    }

    const storedValue = window.localStorage.getItem(CHAT_MODEL_STORAGE_KEY);
    if (!storedValue) {
        return DEFAULT_CHAT_MODEL_SELECTION;
    }

    const matchedOption = CHAT_MODEL_OPTIONS.find((option) => option.id === storedValue);
    return matchedOption?.id ?? DEFAULT_CHAT_MODEL_SELECTION;
};

const readStoredSubagentToggle = (): boolean => {
    if (typeof window === 'undefined') {
        return true;
    }

    const savedSettings = window.localStorage.getItem('opencode_settings');
    if (savedSettings) {
        try {
            const parsed = JSON.parse(savedSettings) as { enableSubagents?: boolean };
            if (typeof parsed.enableSubagents === 'boolean') {
                return parsed.enableSubagents;
            }
        } catch {
            // Fall back to the legacy dedicated storage key below.
        }
    }

    const storedValue = window.localStorage.getItem(SUBAGENT_TOGGLE_STORAGE_KEY);
    if (storedValue === 'false') {
        return false;
    }

    return true;
};

const readStoredReadOnlyMode = (): boolean => {
    if (typeof window === 'undefined') {
        return false;
    }
    window.localStorage.removeItem(LEGACY_READ_ONLY_MODE_STORAGE_KEY);
    return window.localStorage.getItem(READ_ONLY_MODE_STORAGE_KEY) === 'true';
};

function App() {
    const getInitialExpandedState = () => {
        if (typeof window === 'undefined') {
            return true;
        }

        return !(window.innerWidth <= 620 && window.innerHeight <= 260);
    };

  // XiaoYing opens in the calm light workspace shown in the product reference.
  useEffect(() => {
    document.documentElement.classList.remove('dark');
  }, []);

  const { isLoading: isAuthLoading, user } = useAuth();
  const [isSettingsOpen, setIsSettingsOpen] = useState(false);
  const [isTokenStatsOpen, setIsTokenStatsOpen] = useState(false);
  const [isMemoryVizOpen, setIsMemoryVizOpen] = useState(false);
  const [isMonitorOpen, setIsMonitorOpen] = useState(false);
    const [isWorkerSnapshotsOpen, setIsWorkerSnapshotsOpen] = useState(false);
  const [isCodingOverlayOpen, setIsCodingOverlayOpen] = useState(false);
  const [isAgentOptimizerOpen, setIsAgentOptimizerOpen] = useState(false);
  const [isTerminalOpen, setIsTerminalOpen] = useState(false);
  const [updateAvailable, setUpdateAvailable] = useState<any>(null);
    // 只订阅 isOpen：避免 Word 内部高频动作（publishAction / session / unsaved 等）
    // 冒泡到根组件，造成聊天区等同级子树被迫重渲染与 Word 抢主线程。
    const isWordEditorOpen = useWordEditorSelector((state) => state.isOpen);
    const lastWordApplyIssue = useWordEditorSelector((state) => state.lastApplyIssue);

        const [settingsTab, setSettingsTab] = useState<'general' | 'memories' | 'tools' | 'tasks'>('general');
    const [isManualExpanded, setIsManualExpanded] = useState(getInitialExpandedState);
    const [workingDirectoryPath, setWorkingDirectoryPath] = useState<string | null>(null);
    const [projectPath, setProjectPath] = useState<string | null>(null);
        const [isCreateBlankProjectDialogOpen, setIsCreateBlankProjectDialogOpen] = useState(false);
        const [blankProjectName, setBlankProjectName] = useState('新建项目');
        const [isCreatingBlankProject, setIsCreatingBlankProject] = useState(false);

  // Handle Electron updates
  useEffect(() => {
    if (window.electron) {
      window.electron.onUpdateDownloaded((info) => {
        console.log('Update downloaded:', info);
        setUpdateAvailable(info);
      });
      
      // Optional: Handle errors silently or log them
      window.electron.onUpdateError((err) => {
        console.error('Update error:', err);
      });
    }
  }, []);

  const handleInstallUpdate = () => {
    if (window.electron && window.electron.installUpdate) {
      window.electron.installUpdate();
    }
  };

    const applyWorkspaceStateSnapshot = useCallback((snapshot: WorkspaceStateSnapshot) => {
        setWorkingDirectoryPath(snapshot.workspacePath);
        setProjectPath(snapshot.projectPath);
    }, []);

    const syncWorkspaceState = useCallback(async (): Promise<WorkspaceStateSnapshot | null> => {
        try {
            const res = await fetch('/v1/system/cwd');
            if (!res.ok) {
                return null;
            }

            const data = await res.json() as { path?: string; workspace_path?: string };
            const snapshot = normalizeWorkspaceStateSnapshot(data);
            if (!snapshot) {
                return null;
            }

            applyWorkspaceStateSnapshot(snapshot);
            return snapshot;
        } catch (error) {
            console.error('Failed to sync workspace state:', error);
            return null;
        }
    }, [applyWorkspaceStateSnapshot]);

    const updateCurrentWorkingDirectory = useCallback(async (nextPath: string) => {
        const res = await fetch('/v1/system/cwd', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify({ path: nextPath })
        });

        if (!res.ok) {
            throw new Error('Failed to update working directory');
        }
        const snapshot = await syncWorkspaceState();
        if (!snapshot) {
            const fallbackWorkspacePath = workingDirectoryPath ?? nextPath;
            applyWorkspaceStateSnapshot({
                currentPath: nextPath,
                workspacePath: fallbackWorkspacePath,
                projectPath: nextPath === fallbackWorkspacePath ? null : nextPath,
            });
        }
    }, [applyWorkspaceStateSnapshot, syncWorkspaceState, workingDirectoryPath]);

    useEffect(() => {
        void syncWorkspaceState();
    }, [syncWorkspaceState]);

    const effectiveProjectCwd = projectPath || workingDirectoryPath;

    useEffect(() => {
        if (!effectiveProjectCwd || !window.electronAPI?.setWorkspace) {
            return;
        }

        void window.electronAPI.setWorkspace(effectiveProjectCwd);
    }, [effectiveProjectCwd]);

    const applyProjectDirectory = useCallback(async (selectedPath: string) => {
        await updateCurrentWorkingDirectory(selectedPath);
    }, [updateCurrentWorkingDirectory]);

    const openSystemPath = useCallback(async (path: string, successMessage: string, failureMessage: string) => {
        try {
            const res = await fetch('/v1/system/open', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({ path })
            });

            if (!res.ok) {
                throw new Error('Failed to open path');
            }

            toast.success(successMessage);
        } catch (error) {
            console.error(failureMessage, error);
            toast.error(failureMessage);
        }
    }, []);

    const resolveWorkspaceDirectory = useCallback(async (): Promise<string | null> => {
        if (workingDirectoryPath) {
            return workingDirectoryPath;
        }

        const snapshot = await syncWorkspaceState();
        return snapshot?.workspacePath ?? null;
    }, [syncWorkspaceState, workingDirectoryPath]);

    const handleOpenWorkspace = useCallback(async () => {
        await openSystemPath('workspace', '已打开工作目录', '无法打开工作目录');
    }, [openSystemPath]);

    const handleOpenProjectFolder = useCallback(async () => {
        if (!projectPath) {
            toast.error('当前没有可打开的项目');
            return;
        }

        await openSystemPath(projectPath, '已打开项目文件夹', '无法打开项目文件夹');
    }, [openSystemPath, projectPath]);

    const handleUseExistingFolder = useCallback(async () => {
        try {
            const selectedPath = await window.electronAPI?.selectDirectory?.();
            if (!selectedPath) {
                return;
            }

            await applyProjectDirectory(selectedPath);
        } catch (error) {
            console.error('Failed to use existing folder:', error);
            toast.error('切换文件夹失败');
        }
    }, [applyProjectDirectory]);

    const handleRemoveProject = useCallback(async () => {
        if (!projectPath) {
            return;
        }

        try {
            const workspacePath = await resolveWorkspaceDirectory();
            if (!workspacePath) {
                toast.error('无法确定工作目录');
                return;
            }

            await updateCurrentWorkingDirectory(workspacePath);
            toast.success('已移除当前项目');
        } catch (error) {
            console.error('Failed to remove project:', error);
            toast.error('移除项目失败');
        }
    }, [projectPath, resolveWorkspaceDirectory, updateCurrentWorkingDirectory]);

    const handleCreateBlankProject = useCallback(() => {
        setBlankProjectName('新建项目');
        setIsCreateBlankProjectDialogOpen(true);
    }, []);

    const handleCancelCreateBlankProject = useCallback(() => {
        if (isCreatingBlankProject) {
            return;
        }

        setIsCreateBlankProjectDialogOpen(false);
        setBlankProjectName('新建项目');
    }, [isCreatingBlankProject]);

    const handleConfirmCreateBlankProject = useCallback(async () => {
        const workspacePath = await resolveWorkspaceDirectory();
        if (!workspacePath) {
            toast.error('当前工作目录不可用');
            return;
        }

        const nextProjectName = blankProjectName.trim();
        if (!nextProjectName) {
            toast.error('项目名称不能为空');
            return;
        }

        if (/[\\/]/.test(nextProjectName)) {
            toast.error('项目名称不能包含斜杠');
            return;
        }

        setIsCreatingBlankProject(true);
        try {
            if (projectPath) {
                await updateCurrentWorkingDirectory(workspacePath);
            }

            const res = await fetch('/v1/files/mkdir', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({ path: nextProjectName })
            });

            if (!res.ok) {
                throw new Error(await res.text());
            }

            const nextProjectPath = joinPath(workspacePath, nextProjectName);
            await applyProjectDirectory(nextProjectPath);
            setIsCreateBlankProjectDialogOpen(false);
            setBlankProjectName('新建项目');
            toast.success('已创建空白项目');
        } catch (error) {
            if (projectPath) {
                try {
                    await updateCurrentWorkingDirectory(projectPath);
                } catch (restoreError) {
                    console.error('Failed to restore project working directory:', restoreError);
                }
            }

            console.error('Failed to create blank project:', error);
            toast.error('创建空白项目失败');
        } finally {
            setIsCreatingBlankProject(false);
        }
    }, [applyProjectDirectory, blankProjectName, projectPath, resolveWorkspaceDirectory, updateCurrentWorkingDirectory]);
  // Default to false to prevent 'chunked_data_pipe_upload_data_stream' errors on startup due to network/Google connectivity issues
  const [isVoiceWakeupEnabled, setIsVoiceWakeupEnabled] = useState(false);
  const [isTTSEnabled, setIsTTSEnabled] = useState(false);
  const { 
    messages, 
    fixedErrorMessage,
    sendMessage, 
    stop, 
    cancelTool, 
    resetSession, 
    loadSession, 
    isGenerating, 
    permissionRequest, 
    respondPermission, 
    addLocalMessage, 
    tokenUsage, 
    sessionTokenUsage,
    contextStats,
    serverReadOnlyMode,
    planProgressSnapshot,
    thoughts, 
    currentOperation, 
    gitStatusUpdates, 
    fileWatchPaths,
    activeOperations, 
    realtimeToolExecutions,
    interruptGeneration,
    setSessionReadOnlyMode
  } = useChat();

  // Chat Input State (Lifted)
    const [input, setInput] = useState('');
  const inputSnapshotRef = useRef('');
  const [attachments, setAttachments] = useState<File[]>([]);
  const [mode, setMode] = useState<WorkspaceMode>('normal');
  const [isReadOnlyMode, setIsReadOnlyMode] = useState<boolean>(() => readStoredReadOnlyMode());
  const [isWindowMaximized, setIsWindowMaximized] = useState(false);
  const [sidebarRefreshKey, setSidebarRefreshKey] = useState(0);
    const hasCodingPanel = mode === 'coding' && isCodingOverlayOpen;
    const hasWordPanel = isWordEditorOpen;
    const hasSidePanel = hasCodingPanel || hasWordPanel;
        const [selectedModel, setSelectedModel] = useState<ChatModelSelection>(() => readStoredChatModelSelection());
        const [enableSubagents, setEnableSubagents] = useState<boolean>(() => readStoredSubagentToggle());
        const processedWordIssueKeyRef = useRef<string | null>(null);
        const wordAutoRelocateFingerprintRef = useRef<string | null>(null);
        const wordAutoRelocateAttemptCountRef = useRef(0);
            const projectName = projectPath ? getProjectFolderName(projectPath) : NO_PROJECT_NAME;
            const canRemoveProject = Boolean(projectPath);

    useEffect(() => {
            window.localStorage.setItem(CHAT_MODEL_STORAGE_KEY, selectedModel);
    }, [selectedModel]);

    useEffect(() => {
            window.localStorage.setItem(SUBAGENT_TOGGLE_STORAGE_KEY, String(enableSubagents));
    }, [enableSubagents]);

    useEffect(() => {
            if (serverReadOnlyMode !== null) {
                    setIsReadOnlyMode(serverReadOnlyMode);
            }
    }, [serverReadOnlyMode]);

    useEffect(() => {
            const unsubscribe = window.electron?.onWindowMaximizeStateChanged?.((state) => {
                    setIsWindowMaximized(state.maximized);
            });
            return () => {
                    unsubscribe?.();
            };
    }, []);

  // Handle TTS Hook
  const { speak, stop: stopTTS, isSpeaking } = useTTS();

  const normalizeVoiceText = useCallback((text: string) => {
      let normalized = text;
      const wakeWords = ['你好小英', '小英', '你好 XiaoYing', 'XiaoYing', 'Hello XiaoYing', 'Hi XiaoYing', 'Wake up'];
      for (const word of wakeWords) {
          if (normalized.toLowerCase().startsWith(word.toLowerCase())) {
              const regex = new RegExp(`^${word}[\\s,，.。!！?？]*`, 'i');
              normalized = normalized.replace(regex, '');
              break;
          }
      }
      return normalized.trim();
  }, []);

  const mergeSnapshotAndVoice = useCallback((voiceText: string) => {
      const normalized = normalizeVoiceText(voiceText);
      const snapshot = inputSnapshotRef.current;
      
      // Keep existing input if nothing to merge
      if (!normalized) return snapshot;
      
      // If we don't have a snapshot, just use the voice text
      if (!snapshot) return normalized;
      
      // Ensure we don't duplicate existing text.
      // E.g. if inputSnapshot is "Hello", and normalized is "Hello World"
      // we don't want "Hello Hello World"
      return `${snapshot} ${normalized}`.trim();
  }, [normalizeVoiceText]);

  const handleTranscriptionComplete = useCallback((text: string) => {
      // Even if text is empty, we need to restore the snapshot
      // so the user doesn't lose what they typed before clicking mic
      if (!text) {
          setInput(inputSnapshotRef.current);
          inputSnapshotRef.current = '';
          return;
      }
      setInput(mergeSnapshotAndVoice(text));
      inputSnapshotRef.current = '';
  }, [mergeSnapshotAndVoice]);

  const handleInterimResult = useCallback((text: string) => {
      if (!text) {
          setInput(inputSnapshotRef.current);
          return;
      }
      setInput(mergeSnapshotAndVoice(text));
  }, [mergeSnapshotAndVoice]);

  const { isRecording, isTranscribing, startRecording, stopRecording, analyser } = useTranscription({
      onTranscriptionComplete: handleTranscriptionComplete,
      onInterimResult: handleInterimResult
  });

  // Snapshot input when manual recording starts
  const handleStartManualRecording = async () => {
      stopTTS(); // Stop TTS immediately when recording starts
      inputSnapshotRef.current = input;
      const success = await startRecording();
      if (!success) {
          // Reset snapshot if failed to start
          inputSnapshotRef.current = '';
      }
  };

  // State to track if we are currently processing a wake word trigger (transitioning to recording)
  const [isProcessingWakeWord, setIsProcessingWakeWord] = useState(false);

  // Stop TTS immediately when disabled
  useEffect(() => {
    if (!isTTSEnabled) {
      stopTTS();
    }
  }, [isTTSEnabled, stopTTS]);

    // Voice Wake-up Logic
  const handleWakeWord = useCallback(async () => {
    // Double check to prevent self-wakeup if state update was slightly delayed
    if (isSpeaking) {
        console.log('[VoiceWakeup] Ignored because TTS is speaking');
        return;
    }

    console.log('[VoiceWakeup] Triggered via built-in keyword spotting');
    setIsProcessingWakeWord(true); // Prevent Wake Word service from restarting immediately
    setIsManualExpanded(true);
    
    // Play random offline wake response
    const wakeSounds = [
        'wo_zai.mp3', 
        'wo_zai_ne.mp3', 
        'lai_le.mp3', 
        'qing_shuo.mp3', 
        'xiao_a_zai.mp3'
    ];
    const randomSound = wakeSounds[Math.floor(Math.random() * wakeSounds.length)];
    
    // Start recording IMMEDIATELY before playing sound to capture user input
    // The echo cancellation in startRecording (getUserMedia) should filter out the system audio
    console.log('[VoiceWakeup] Starting recording immediately...');
    inputSnapshotRef.current = input;
    startRecording().then((success) => {
        if (!success) {
            inputSnapshotRef.current = '';
            setIsProcessingWakeWord(false); // Reset if failed
        }
    });

    try {
        // Use relative path for Electron compatibility (file:// protocol)
        // In production, base is usually './', so 'sounds/...' works relative to index.html
        const audioPath = import.meta.env.BASE_URL.endsWith('/') 
            ? `${import.meta.env.BASE_URL}sounds/wakeup/${randomSound}`
            : `${import.meta.env.BASE_URL}/sounds/wakeup/${randomSound}`;
            
        // Fallback for simple relative path if BASE_URL is just './' or '/'
        // Actually, just removing the leading slash is safer for both dev and prod if base is set correctly
        // But let's use a cleaner approach
        const audio = new Audio(audioPath.replace(/^\/\//, '/')); 
        
        await audio.play();
        // We don't wait for audio to end anymore, to avoid latency
    } catch (e) {
        console.error('Wake response failed:', e);
    }
  }, [startRecording, isSpeaking, input]);

  const { 
        isLoaded: isWakeWordLoaded,
    isListening: isWakeWordListening, 
        hasModel: hasWakeWordModel,
        wakeWordError,
  } = useWakeWord({
    // Disable wake word listener if:
    // 1. We are recording
    // 2. We are speaking (TTS)
    // 3. We are generating AI response
    // 4. We are currently processing a wake word trigger (transition phase)
    // 5. Global switch is off
    isEnabled: isVoiceWakeupEnabled && !isRecording && !isSpeaking && !isGenerating && !isProcessingWakeWord,
    onWakeWord: handleWakeWord
  });

    const wakeWordButtonTitle = !isVoiceWakeupEnabled
        ? '开启语音唤醒'
        : !isWakeWordLoaded
            ? '语音唤醒初始化中...'
            : !hasWakeWordModel
                ? (wakeWordError || '语音唤醒模块初始化失败')
                : '语音唤醒已开启，固定唤醒词：你好小英';

  // Reset processing state when recording actually starts or stops
  useEffect(() => {
      if (isRecording) {
          setIsProcessingWakeWord(false);
      }
  }, [isRecording]);

  // Auto-TTS Streaming Logic
  const processedLengthRef = useRef(0);
  const currentMessageIdRef = useRef<string | null>(null);
    const lastAssistantMessage = messages[messages.length - 1]?.role === 'assistant'
        ? messages[messages.length - 1]
        : null;

  useEffect(() => {
    if (!isTTSEnabled) return;

        const lastMsg = lastAssistantMessage;
    if (!lastMsg || lastMsg.role !== 'assistant') return;

    // Detect new message start or reset
    if (lastMsg.id !== currentMessageIdRef.current) {
        currentMessageIdRef.current = lastMsg.id;
        processedLengthRef.current = 0;
        // Stop previous speech if it was from a previous turn (optional)
        // if (isGenerating) stop(); 
    }

    // Get content and clean thinking tags
    const content = lastMsg.content;
    let speakableContent = content;

    // Remove complete thinking blocks
    speakableContent = speakableContent.replace(/<thinking>[\s\S]*?<\/thinking>/g, '');
    
    // Handle incomplete thinking block at the end (or start)
    const thinkingStart = speakableContent.indexOf('<thinking>');
    if (thinkingStart !== -1) {
        // If there is an unclosed thinking tag, we truncate everything after it
        speakableContent = speakableContent.substring(0, thinkingStart);
    }
    
    // Also remove </thinking> if it appears alone (cleanup artifact)
    speakableContent = speakableContent.replace(/<\/thinking>/g, '');
    
    speakableContent = speakableContent.trimStart();

    // Determine new part to process
    // Note: We use the length of the *cleaned* content to track progress
    const newPart = speakableContent.slice(processedLengthRef.current);
    if (!newPart) return;

    // If generating, look for delimiters to chunk
    if (isGenerating) {
        // Delimiters: . ? ! 。 ？ ！ ; ； : ： \n
        // Removed comma to reduce chunking frequency and improve continuity (let TTS handle comma pauses)
        const regex = /[。！？；：!?;:\n]+/g;
        let lastIndex = 0;
        let match;

        while ((match = regex.exec(newPart)) !== null) {
            const end = match.index + match[0].length;
            const sentence = newPart.slice(lastIndex, end);
            
            if (sentence.trim()) {
                speak(sentence, undefined, 'append');
            }
            lastIndex = end;
        }
        
        // Update processed length
        processedLengthRef.current += lastIndex;
    } else {
        // Not generating (finished), speak the remainder
        if (newPart.trim()) {
            speak(newPart, undefined, 'append');
            processedLengthRef.current += newPart.length;
        }
    }

    }, [isGenerating, isTTSEnabled, lastAssistantMessage, speak]);

  // Determine if we should show the full chat history
    const [isExpanded, setIsExpanded] = useState(getInitialExpandedState);

  useEffect(() => {
    // Priority 1: Coding Mode always expands
        if (hasCodingPanel || hasWordPanel) {
        setIsExpanded(true);
        return;
    }

    // Outside coding mode, always respect the user's manual preference,
    // including while the assistant is generating/thinking.
    setIsExpanded(isManualExpanded);
    
    }, [hasCodingPanel, hasWordPanel, isManualExpanded]);

  type TokenStats = {
    input_tokens: number;
    output_tokens: number;
    total_tokens: number;
  };

  const fetchTokenStats = useCallback(() => {
    const isTokenStats = (data: unknown): data is TokenStats => {
      if (!data || typeof data !== 'object') return false;
      const value = data as Record<string, unknown>;
      return typeof value.input_tokens === 'number' &&
        typeof value.output_tokens === 'number' &&
        typeof value.total_tokens === 'number';
    };

    fetch('/v1/stats/tokens')
        .then(res => res.json() as Promise<unknown>)
        .then(data => {
            if (isTokenStats(data)) {
                setHistoryTokenUsage(data);
            }
        })
        .catch(err => console.error('Failed to fetch token stats:', err));
  }, []);

  // Initial fetch
  useEffect(() => {
      fetchTokenStats();
  }, [fetchTokenStats]);

  const [historyTokenUsage, setHistoryTokenUsage] = useState<{
      input_tokens: number;
      output_tokens: number;
      total_tokens: number;
  }>({ input_tokens: 0, output_tokens: 0, total_tokens: 0 });
    const [appSidebarWidth, setAppSidebarWidth] = useState(DEFAULT_APP_SIDEBAR_WIDTH);
    const [chatPanelWidth, setChatPanelWidth] = useState(DEFAULT_CHAT_PANEL_WIDTH);
    const [layoutResizeTarget, setLayoutResizeTarget] = useState<LayoutResizeTarget | null>(null);
    const layoutResizeStartRef = useRef({
        clientX: 0,
        appSidebarWidth: DEFAULT_APP_SIDEBAR_WIDTH,
        chatPanelWidth: DEFAULT_CHAT_PANEL_WIDTH,
    });
    const prevHasWordPanelRef = useRef(hasWordPanel);
    const prevHasCodingPanelRef = useRef(hasCodingPanel);
    const wordEditorPanelRef = useRef<WordEditorPanelHandle>(null);

  const startLayoutResize = useCallback((target: LayoutResizeTarget, event: ReactMouseEvent<HTMLDivElement>) => {
      event.preventDefault();
      layoutResizeStartRef.current = {
          clientX: event.clientX,
          appSidebarWidth,
          chatPanelWidth,
      };
      setLayoutResizeTarget(target);
  }, [appSidebarWidth, chatPanelWidth]);

  useEffect(() => {
      if (!layoutResizeTarget) {
          return;
      }

      const handleMouseMove = (event: MouseEvent) => {
          const deltaX = event.clientX - layoutResizeStartRef.current.clientX;

          if (layoutResizeTarget === 'sidebar') {
              const nextWidth = Math.min(
                  MAX_APP_SIDEBAR_WIDTH,
                  Math.max(MIN_APP_SIDEBAR_WIDTH, layoutResizeStartRef.current.appSidebarWidth + deltaX),
              );
              setAppSidebarWidth(nextWidth);
              return;
          }

          const nextWidth = Math.min(
              MAX_CHAT_PANEL_WIDTH,
              Math.max(MIN_CHAT_PANEL_WIDTH, layoutResizeStartRef.current.chatPanelWidth + deltaX),
          );
          setChatPanelWidth(nextWidth);
      };

      const handleMouseUp = () => setLayoutResizeTarget(null);

      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
      window.addEventListener('mousemove', handleMouseMove);
      window.addEventListener('mouseup', handleMouseUp);

      return () => {
          document.body.style.cursor = '';
          document.body.style.userSelect = '';
          window.removeEventListener('mousemove', handleMouseMove);
          window.removeEventListener('mouseup', handleMouseUp);
      };
  }, [layoutResizeTarget]);

  const resizeWindowForLayout = useCallback((expanded: boolean, codingPanelOpen: boolean, wordPanelOpen: boolean) => {
      if (!window.electron?.resizeWindow) {
          return;
      }

      const sidebarWidth = expanded && user ? appSidebarWidth : 0;
      let width = (expanded ? EXPANDED_WINDOW_WIDTH : COLLAPSED_WINDOW_WIDTH) + sidebarWidth;
      const height = expanded ? EXPANDED_WINDOW_HEIGHT : COLLAPSED_WINDOW_HEIGHT;

      if (codingPanelOpen || wordPanelOpen) {
          width = chatPanelWidth + sidebarWidth + (codingPanelOpen ? CODER_PANEL_WIDTH : 0) + (wordPanelOpen ? WORD_PANEL_WIDTH : 0);
      }

      window.electron.resizeWindow(width, height);
  }, [appSidebarWidth, chatPanelWidth, user]);

  const handleToggleWordPanel = async () => {
      const nextWordPanelOpen = !isWordEditorOpen;

      if (nextWordPanelOpen) {
          openWordEditor();
          setIsManualExpanded(true);
          return;
      }

      if (wordEditorPanelRef.current) {
          await wordEditorPanelRef.current.requestClose();
          return;
      }

      closeWordEditor();
  };

  useEffect(() => {
      const wordPanelChanged = prevHasWordPanelRef.current !== hasWordPanel;
      const codingPanelChanged = prevHasCodingPanelRef.current !== hasCodingPanel;

      if (!wordPanelChanged && !codingPanelChanged) {
          return;
      }

      const expanded = hasCodingPanel || hasWordPanel ? true : isManualExpanded;
      resizeWindowForLayout(expanded, hasCodingPanel, hasWordPanel);

      prevHasWordPanelRef.current = hasWordPanel;
      prevHasCodingPanelRef.current = hasCodingPanel;
  }, [hasCodingPanel, hasWordPanel, isManualExpanded, resizeWindowForLayout]);

  const handleClose = () => {
        if (window.electron?.closeWindow) {
            window.electron.closeWindow();
            return;
        }

        toast.error('桌面桥接未就绪，无法关闭窗口，请重启应用');
  };

    const handleMinimize = () => {
                if (window.electron?.minimizeWindow) {
                        window.electron.minimizeWindow();
                        return;
                }

                toast.error('桌面桥接未就绪，无法最小化窗口，请重启应用');
    };

    const handleToggleMaximize = () => {
                if (window.electron?.toggleMaximizeWindow) {
                        window.electron.toggleMaximizeWindow();
                        return;
                }

                toast.error('桌面桥接未就绪，无法最大化窗口，请重启应用');
    };

    const sendMessageWithModelFallback = useCallback(async (
        content: string,
        files: File[] = [],
        options: {
            appendUserMessage?: boolean;
            suppressAutoModelErrors?: boolean;
            replaceLatestAssistantTurnOnRetry?: boolean;
        } = {},
    ) => {
        const nextAttachments = files;
        const appendUserMessage = options.appendUserMessage ?? true;
        const replaceLatestAssistantTurnOnRetry = options.replaceLatestAssistantTurnOnRetry ?? appendUserMessage;
        const modelAttempts = resolveChatModelAttemptSequence(selectedModel, content, nextAttachments);

        for (let index = 0; index < modelAttempts.length; index += 1) {
            const targetModel = modelAttempts[index];
            const isLastAttempt = index === modelAttempts.length - 1;
            const result = await sendMessage(content, nextAttachments, mode, targetModel, {
                appendUserMessage: index === 0 ? appendUserMessage : false,
                suppressErrorUi: options.suppressAutoModelErrors ?? (selectedModel === 'auto' && !isLastAttempt),
                enableSubagents,
                replaceLatestAssistantTurn: replaceLatestAssistantTurnOnRetry && index > 0,
            });

            recordResolvedChatModelObservation(targetModel, {
                status: result.status,
                ttftMs: result.ttftMs,
                totalMs: result.totalMs,
                outputChars: result.outputChars,
            });

            if (result.status === 'success' || result.status === 'canceled') {
                return result;
            }

            if (selectedModel !== 'auto' || isLastAttempt || !shouldRetryAutoModelAttempt({
                errorCode: result.errorCode,
                ttftMs: result.ttftMs,
            })) {
                return result;
            }

            const nextModel = modelAttempts[index + 1];
            if (!nextModel) {
                return result;
            }
        }

        return { status: 'error' as const };
    }, [enableSubagents, mode, selectedModel, sendMessage]);

    const handleSendMessage = useCallback(async (content: string, files?: File[]) => {
        setIsManualExpanded(true);
        setInput('');
        setAttachments([]);
        processedWordIssueKeyRef.current = null;
        wordAutoRelocateFingerprintRef.current = null;
        wordAutoRelocateAttemptCountRef.current = 0;
        await sendMessageWithModelFallback(content, files ?? []);
    }, [sendMessageWithModelFallback]);

    const handleReadOnlyModeChange = useCallback((enabled: boolean) => {
        setIsReadOnlyMode(enabled);
        window.localStorage.setItem(READ_ONLY_MODE_STORAGE_KEY, String(enabled));
        setSessionReadOnlyMode(enabled);
    }, [setSessionReadOnlyMode]);

    useEffect(() => {
        if (isGenerating || !lastWordApplyIssue || !isWordApplyIssueAutoRelocatable(lastWordApplyIssue)) {
            return;
        }

        const issueKey = buildWordAutoRelocateIssueKey(lastWordApplyIssue);
        if (processedWordIssueKeyRef.current === issueKey) {
            return;
        }

        const fingerprint = buildWordAutoRelocateFingerprint(lastWordApplyIssue);
        const nextAttempt = wordAutoRelocateFingerprintRef.current === fingerprint
            ? wordAutoRelocateAttemptCountRef.current + 1
            : 1;

        processedWordIssueKeyRef.current = issueKey;
        wordAutoRelocateFingerprintRef.current = fingerprint;
        wordAutoRelocateAttemptCountRef.current = nextAttempt;

        if (nextAttempt > MAX_WORD_AUTO_RELOCATE_ATTEMPTS) {
            toast.error('Word 目标仍存在歧义，已停止自动重定位，请补充更精确的锚点信息');
            return;
        }

        void sendMessageWithModelFallback(
			`${INTERNAL_HIDDEN_USER_PROMPT_MARKER}\n${buildWordAutoRelocatePrompt(lastWordApplyIssue)}`,
            [],
            {
                appendUserMessage: false,
                replaceLatestAssistantTurnOnRetry: false,
            },
        );
    }, [isGenerating, lastWordApplyIssue, sendMessageWithModelFallback]);

    const handleResumeUser = useCallback(() => {
        handleSendMessage("继续执行未完成的任务");
    }, [handleSendMessage]);

  type NotificationItem = {
    title: string;
    content: string;
  };

  const handleNotificationClick = (notification: NotificationItem) => {
    const content = `**${notification.title}**\n\n${notification.content}`;
    // Use 'assistant' role so it appears on the left side like a system message
    addLocalMessage('assistant', content);
  };

  const handleNewConversation = useCallback(() => {
      resetSession();
      setInput('');
      setAttachments([]);
      setIsManualExpanded(true);
      setSidebarRefreshKey((value) => value + 1);
      fetchTokenStats();
  }, [fetchTokenStats, resetSession]);

  const handleSelectSidebarSession = useCallback(async (id: string) => {
      await loadSession(id, { model: selectedModel, mode, enableSubagents });
      setIsManualExpanded(true);
      setSidebarRefreshKey((value) => value + 1);
      fetchTokenStats();
  }, [enableSubagents, fetchTokenStats, loadSession, mode, selectedModel]);

  const combinedSessionTokenUsage = {
      input_tokens: historyTokenUsage.input_tokens + sessionTokenUsage.input_tokens,
      output_tokens: historyTokenUsage.output_tokens + sessionTokenUsage.output_tokens,
      total_tokens: historyTokenUsage.total_tokens + sessionTokenUsage.total_tokens
  };

  const isWelcome = messages.length === 0 && !isGenerating && !fixedErrorMessage;

  const renderChatInput = (variant: 'chat' | 'welcome') => (
      <ChatInput
          variant={variant}
          value={input}
          onChange={setInput}
          selectedModel={selectedModel}
          onSelectedModelChange={setSelectedModel}
          attachments={attachments}
          onAttachmentsChange={setAttachments}
          onSendMessage={handleSendMessage}
          onStop={interruptGeneration || stop}
          isGenerating={isGenerating}
          disabled={false}
          isRecording={isRecording}
          isTranscribing={isTranscribing}
          onStartRecording={handleStartManualRecording}
          onStopRecording={() => { void stopRecording(); }}
          activeOperations={activeOperations}
          mode={mode}
          onModeChange={setMode}
          isReadOnlyMode={isReadOnlyMode}
          onReadOnlyModeChange={handleReadOnlyModeChange}
          onToggleCodingPanel={() => {
              if (mode === 'coding') {
                  setIsCodingOverlayOpen(!isCodingOverlayOpen);
              }
          }}
          contextWindowAccessory={user ? <ContextWindowButton contextStats={contextStats} /> : null}
      />
  );

  if (isAuthLoading) {
    return (
      <div className="flex h-screen w-full items-center justify-center bg-zinc-50 dark:bg-zinc-950">
        <Loader2 className="h-8 w-8 animate-spin text-blue-500" />
      </div>
    );
  }

  return (
      <div className={cn(
        "xiaoying-app-shell flex flex-col items-center justify-start h-screen w-full bg-white dark:bg-zinc-950 border border-zinc-200 dark:border-zinc-800 shadow-xl overflow-hidden transition-colors duration-200",
        mode === 'coding' && "coding-mode-shell",
      isGenerating && "animate-rainbow"
    )}>
      
      {!user && <Login />}

      <AudioGlow isRecording={isRecording || isProcessingWakeWord || isGenerating} analyser={analyser} />

      {/* Settings Modal */}
      {isSettingsOpen && (
        <div className="fixed inset-0 z-50 bg-black/50 flex items-center justify-center pointer-events-auto">
             <SettingsModal 
                isOpen={isSettingsOpen} 
                onClose={() => setIsSettingsOpen(false)} 
                initialTab={settingsTab}
                     enableSubagents={enableSubagents}
                     onEnableSubagentsChange={setEnableSubagents}
             />
        </div>
      )}

      {/* Update Prompt */}
      {updateAvailable && (
        <UpdatePrompt 
          version={updateAvailable.version}
          onInstall={handleInstallUpdate}
          onDismiss={() => setUpdateAvailable(null)}
        />
      )}

      {/* Token Stats Dialog */}
      <TokenStatsDialog 
        isOpen={isTokenStatsOpen}
        onClose={() => setIsTokenStatsOpen(false)}
        currentUsage={tokenUsage}
        sessionUsage={combinedSessionTokenUsage}
      />

      {/* Memory Visualization Modal */}
      {isMemoryVizOpen && (
        <MemoryVizModal 
            isOpen={isMemoryVizOpen}
            onClose={() => setIsMemoryVizOpen(false)}
        />
      )}

      {/* Cognitive Monitor */}
      <CognitiveMonitor 
          isOpen={isMonitorOpen}
          onClose={() => setIsMonitorOpen(false)}
          thoughts={thoughts}
      />

      <WorkerSnapshotsPanel
          isOpen={isWorkerSnapshotsOpen}
          onClose={() => setIsWorkerSnapshotsOpen(false)}
      />

      {/* Agent Optimizer */}
      <AgentOptimizer 
          isOpen={isAgentOptimizerOpen}
          onClose={() => setIsAgentOptimizerOpen(false)}
      />



      {/* Permission Dialog */}
      <ConfirmationDialog 
          isOpen={!!permissionRequest}
          title={`Allow ${permissionRequest?.tool_name}?`}
          description={permissionRequest?.description || 'The assistant requests permission to execute a tool.'}
          details={permissionRequest?.path ? `Path: ${permissionRequest.path}` : JSON.stringify(permissionRequest?.params)}
          onConfirm={() => respondPermission('grant')}
          onCancel={() => respondPermission('deny')}
      />

      <TextPromptDialog
          isOpen={isCreateBlankProjectDialogOpen}
          title="新建空白项目"
          description="请输入新项目名称，项目将创建在固定工作目录下。"
          value={blankProjectName}
          placeholder="请输入项目名称"
          confirmLabel="创建项目"
          cancelLabel="取消"
          isSubmitting={isCreatingBlankProject}
          onChange={setBlankProjectName}
          onConfirm={handleConfirmCreateBlankProject}
          onCancel={handleCancelCreateBlankProject}
      />

      {/* Main Layout Container */}
      <div className="flex w-full h-full overflow-hidden">
          {user && isExpanded && (
              <>
                  <div className="h-full shrink-0" style={{ width: appSidebarWidth }}>
                      <AppSidebar
                          projectName={projectName}
                          canRemoveProject={canRemoveProject}
                          refreshKey={sidebarRefreshKey}
                          onNewConversation={handleNewConversation}
                          onCreateBlankProject={handleCreateBlankProject}
                          onOpenProjectFolder={handleOpenProjectFolder}
                          onRemoveProject={handleRemoveProject}
                          onUseExistingFolder={handleUseExistingFolder}
                          onSelectSession={handleSelectSidebarSession}
                          onExploreWorkflows={() => setIsAgentOptimizerOpen(true)}
                          onOpenSkills={() => {
                              setSettingsTab('tools');
                              setIsSettingsOpen(true);
                          }}
                          planProgressSnapshot={planProgressSnapshot}
                      />
                  </div>
                  <div
                      className="relative z-50 -mx-1 w-2 shrink-0 cursor-col-resize bg-transparent after:absolute after:inset-y-0 after:left-1/2 after:w-px after:-translate-x-1/2 after:bg-transparent after:transition-colors hover:after:bg-cyan-300/35 dark:hover:after:bg-cyan-500/25"
                      onMouseDown={(event) => startLayoutResize('sidebar', event)}
                      role="separator"
                      aria-orientation="vertical"
                      aria-label="调整侧边栏宽度"
                  />
              </>
          )}

          {/* Main Chat Area */}
          <div className={cn(
                        "xiaoying-main-shell flex-1 h-full app-solid-bg shadow-none overflow-hidden flex flex-col border border-zinc-200 dark:border-zinc-800 transition-opacity duration-200 min-w-[380px]",
                        mode === 'coding' && "coding-main-surface"
          )}>
            
            {/* Top Toolbar */}
            {(() => {
              const dragRegionStyle: CSSProperties & { WebkitAppRegion?: 'drag' | 'no-drag' } = { WebkitAppRegion: 'drag' };
              const noDragRegionStyle: CSSProperties & { WebkitAppRegion?: 'drag' | 'no-drag' } = { WebkitAppRegion: 'no-drag' };

              return (
                <div className="xiaoying-topbar relative z-50 flex items-center justify-between gap-3 px-3 py-2 border-b border-zinc-100 dark:border-zinc-800 bg-zinc-50 dark:bg-zinc-900" style={dragRegionStyle}>
            <div className="flex min-w-0 items-center gap-2 overflow-hidden">
                <div style={noDragRegionStyle}>
                    <AuraStatusButton thoughts={thoughts} />
                </div>
                <span className="mr-2 shrink-0 text-[13px] font-bold tracking-[0.18em] text-zinc-800 select-none">XIAOYING</span>
                {/* Status buttons */}
                <div className="flex min-w-0 items-center gap-1" style={noDragRegionStyle}>
                    <button 
                        onClick={() => {
                            setIsMonitorOpen(true);
                            setIsManualExpanded(true);
                        }}
                        className="p-1.5 text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded-md transition-colors"
                        title="意识监控"
                    >
                        <Sparkles className="w-4 h-4" />
                    </button>
                    <button 
                        onClick={() => {
                            setIsWorkerSnapshotsOpen(true);
                            setIsManualExpanded(true);
                        }}
                        className="p-1.5 text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded-md transition-colors"
                        title="子智能体监控"
                    >
                        <Bot className="w-4 h-4" />
                    </button>
                </div>
            </div>
            
            {/* Right Side Buttons (No-drag region) */}
            <div className="flex items-center gap-0.5 shrink-0" style={noDragRegionStyle}>
                <button
                    onClick={handleToggleWordPanel}
                    className={cn(
                        "group relative p-1 mr-2 rounded-md transition-all border",
                        isWordEditorOpen
                            ? "text-blue-800 bg-blue-100 border-blue-300 dark:text-blue-100 dark:bg-blue-900/40 dark:border-blue-700 shadow-sm"
                            : "text-blue-700 bg-blue-50 border-blue-200 hover:bg-blue-100 hover:border-blue-300 dark:text-blue-200 dark:bg-blue-900/20 dark:border-blue-800 dark:hover:bg-blue-900/35"
                    )}
                    title="打开Word编辑面板"
                >
                    <span className="flex items-center justify-center w-5 h-5 rounded-[3px] bg-[#185ABD] text-white text-[11px] font-black leading-none select-none">
                        W
                    </span>
                </button>
                <button
                    onClick={() => {
                        setIsVoiceWakeupEnabled(!isVoiceWakeupEnabled);
                        setIsManualExpanded(true);
                    }}
                    className={cn(
                        "p-1.5 rounded-md transition-colors relative",
                        isVoiceWakeupEnabled 
                            ? (hasWakeWordModel ? "text-emerald-500 hover:text-emerald-600 bg-emerald-50 dark:bg-emerald-900/20" : "text-amber-500 hover:text-amber-600 bg-amber-50 dark:bg-amber-900/20")
                            : "text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800"
                    )}
                    title={wakeWordButtonTitle}
                >
                    {isVoiceWakeupEnabled ? <Ear className="w-4 h-4" /> : <Ear className="w-4 h-4 opacity-50" />}
                    {isVoiceWakeupEnabled && isWakeWordListening && (
                        <span className="absolute top-1 right-1 w-1.5 h-1.5 bg-emerald-500 rounded-full animate-pulse" />
                    )}
                    {isVoiceWakeupEnabled && !hasWakeWordModel && (
                        <span className="absolute top-1 right-1 w-1.5 h-1.5 bg-amber-500 rounded-full animate-bounce" />
                    )}
                </button>
                <button 
                    onClick={() => {
                        setIsTokenStatsOpen(true);
                        setIsManualExpanded(true);
                    }}
                    className="p-1.5 text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded-md transition-colors"
                    title="Token 统计"
                >
                    <Activity className="w-4 h-4" />
                </button>
                <button 
                    onClick={() => {
                        setIsMemoryVizOpen(true);
                        setIsManualExpanded(true);
                    }}
                    className="p-1.5 text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded-md transition-colors"
                    title="记忆图谱"
                >
                    <Brain className="w-4 h-4" />
                </button>
                <button
                    onClick={() => {
                        void handleOpenWorkspace();
                        setIsManualExpanded(true);
                    }}
                    className="p-1.5 text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded-md transition-colors"
                    title="打开工作目录"
                >
                    <FolderOpen className="w-4 h-4" />
                </button>

                <ActiveTasksCenter onOpen={() => setIsManualExpanded(true)} />
                <NotificationCenter 
                    onNotificationClick={(n) => { handleNotificationClick(n); setIsManualExpanded(true); }} 
                    onOpen={() => setIsManualExpanded(true)}
                />
                <button 
                    onClick={() => {
                        setSettingsTab('general');
                        setIsSettingsOpen(true);
                        setIsManualExpanded(true);
                    }}
                    className="p-1.5 text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded-md transition-colors"
                    title="设置"
                >
                    <Settings className="w-4 h-4" />
                </button>
                <button
                    onClick={() => {
                        setIsTerminalOpen((open) => !open);
                    }}
                    className={cn(
                        "p-1.5 rounded-md transition-colors",
                        isTerminalOpen
                            ? "text-zinc-900 bg-zinc-100 dark:text-zinc-100 dark:bg-zinc-800"
                            : "text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800"
                    )}
                    title="打开命令行"
                >
                    <TerminalSquare className="w-4 h-4" />
                </button>
                <div className="w-px h-4 bg-zinc-200 dark:bg-zinc-700 mx-1" />
                <button
                    onClick={handleMinimize}
                    className="p-1.5 text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded-md transition-colors"
                    title="最小化"
                >
                    <Minus className="w-4 h-4" />
                </button>
                <button
                    onClick={handleToggleMaximize}
                    className="p-1.5 text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 rounded-md transition-colors"
                    title={isWindowMaximized ? "还原" : "最大化"}
                >
                    {isWindowMaximized ? <Copy className="w-4 h-4" /> : <Square className="w-4 h-4" />}
                </button>
                <button 
                    onClick={handleClose}
                    className="p-1.5 text-zinc-500 hover:text-red-600 dark:hover:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20 rounded-md transition-colors"
                    title="关闭"
                >
                    <X className="w-4 h-4" />
                </button>
            </div>
        </div>
              );
            })()}

        {/* Main Content Area (Split View for Coding Mode) */}
        <div className="xiaoying-main-content flex-1 flex flex-row overflow-hidden relative">
            {/* Left Column: Chat & Input */}
            <div className={cn(
                "flex flex-col h-full",
                hasSidePanel ? "shrink-0 border-r border-zinc-200 dark:border-zinc-800" : "w-full"
            )}
            style={hasSidePanel ? { width: chatPanelWidth } : undefined}>
                {/* Chat Area - Collapsible */}
                {isWelcome && !hasSidePanel ? (
                    <div className="xiaoying-welcome-layout">
                        <XiaoyingWelcome
                            mode={mode}
                            onModeChange={(nextMode) => {
                                setMode(nextMode);
                                if (nextMode !== 'coding') {
                                    setIsCodingOverlayOpen(false);
                                }
                            }}
                            onQuickAction={(label) => {
                                setInput(`${label}：`);
                                setIsManualExpanded(true);
                                requestAnimationFrame(() => document.getElementById('chat-composer-input')?.focus());
                            }}
                            onOpenCodingWorkspace={() => {
                                setIsCodingOverlayOpen(true);
                                setIsManualExpanded(true);
                            }}
                        />
                        <div className="xiaoying-welcome-composer">
                            {renderChatInput('welcome')}
                            <button type="button" className="xiaoying-project-picker" onClick={() => void handleUseExistingFolder()}>
                                <FolderOpen className="h-4 w-4" />
                                <span>{projectName === NO_PROJECT_NAME ? '选择工作项目' : projectName}</span>
                                <ArrowUpRight className="ml-auto h-4 w-4" />
                            </button>
                        </div>
                    </div>
                ) : (
                    <>
                        <div className={cn(
                            "flex-1 overflow-hidden transition-all duration-300 flex flex-col",
                            isExpanded ? "opacity-100" : "opacity-0 h-0 hidden"
                        )}>
                            <ChatArea
                                messages={messages}
                                fixedErrorMessage={fixedErrorMessage}
                                className="flex-1"
                                isGenerating={isGenerating}
                                onCancelTool={cancelTool}
                                onResumeUser={handleResumeUser}
                                realtimeToolExecutions={realtimeToolExecutions}
                            />
                        </div>
                        <div className={cn(
                            "relative z-30 bg-zinc-50/50 p-0 backdrop-blur-sm border-t border-zinc-200/50 dark:bg-zinc-900/50 dark:border-zinc-800/50",
                            mode === 'coding' && "border-cyan-200/60 bg-cyan-50/25 dark:border-cyan-400/10 dark:bg-cyan-950/10"
                        )}>
                            {renderChatInput('chat')}
                        </div>
                    </>
                )}
                <TerminalPanel
                    isOpen={isTerminalOpen}
                    defaultCwd={effectiveProjectCwd}
                    onClose={() => setIsTerminalOpen(false)}
                />
            </div>

            {hasSidePanel && (
                <div
                    className="relative z-50 -mx-1 w-2 shrink-0 cursor-col-resize bg-transparent after:absolute after:inset-y-0 after:left-1/2 after:w-px after:-translate-x-1/2 after:bg-transparent after:transition-colors hover:after:bg-cyan-300/35 dark:hover:after:bg-cyan-500/25"
                    onMouseDown={(event) => startLayoutResize('chat', event)}
                    role="separator"
                    aria-orientation="vertical"
                    aria-label="调整聊天栏宽度"
                />
            )}

            {/* Right Column: Code Browser */}
            {hasCodingPanel && (
                <div className="coding-panel-frame h-full min-w-[800px] flex-1 bg-zinc-900 transition-all duration-300 ease-in-out shadow-xl z-40 overflow-hidden">
                    <CodeBrowserOverlay
                        isOpen={isCodingOverlayOpen} 
                        onClose={() => setMode('normal')}
                        onWorkingDirectoryChange={updateCurrentWorkingDirectory}
                        currentActivity={currentOperation?.activity}
                        activeFile={currentOperation?.file}
                        activeOperations={activeOperations}
                        gitStatusUpdates={gitStatusUpdates}
                        fileWatchPaths={fileWatchPaths}
                    />
                </div>
            )}

            {hasWordPanel && (
                <div className="h-full min-w-[680px] flex-1 overflow-hidden shadow-[-12px_0_32px_rgba(70,44,18,0.08)]">
                    <WordEditorPanel ref={wordEditorPanelRef} />
				</div>
			)}
        </div>
      </div>
      </div>

      <Toaster position="top-center" toastOptions={{
        style: { zIndex: 999999 },
        className: 'z-[999999]',
        duration: 1000
      }} style={{ zIndex: 999999 }} />
    </div>
  )
}

export default App

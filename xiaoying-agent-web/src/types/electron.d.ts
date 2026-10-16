type WakeWordRuntimeState = {
  available: boolean;
  ready: boolean;
  listening: boolean;
  keyword: string;
  error: string | null;
};

type WakeWordDetectedPayload = {
  keyword: string;
  tokens?: string[];
  timestamp: number;
};

type ElectronWakeWordAPI = {
  init: () => Promise<WakeWordRuntimeState>;
  start: () => Promise<WakeWordRuntimeState>;
  stop: () => Promise<WakeWordRuntimeState>;
  getState: () => WakeWordRuntimeState;
  onDetected: (callback: (payload: WakeWordDetectedPayload) => void) => void;
  onStateChanged: (callback: (state: WakeWordRuntimeState) => void) => void;
};

type ElectronSaveDialogOptions = {
  title?: string;
  defaultPath?: string;
  filters?: Array<{ name: string; extensions: string[] }>;
};

type ElectronMessageBoxOptions = {
  type?: 'none' | 'info' | 'error' | 'question' | 'warning';
  buttons?: string[];
  defaultId?: number;
  cancelId?: number;
  title?: string;
  message: string;
  detail?: string;
  noLink?: boolean;
};

type ElectronMessageBoxResult = {
  response: number;
  checkboxChecked?: boolean;
};

type TerminalCreateOptions = {
  cwd?: string;
  cols?: number;
  rows?: number;
};

type TerminalCreateResult = {
  id: string;
  title: string;
  cwd: string;
  shell: string;
  mode: 'pty';
};

type TerminalDataPayload = {
  id: string;
  data: string;
};

type TerminalExitPayload = {
  id: string;
  code: number | null;
  signal: string | null;
};

interface Window {
  electronAPI?: {
    expandWindow: () => void;
    collapseWindow: () => void;
    listFiles?: (path: string) => Promise<any[]>;
    readFile?: (path: string) => Promise<{ content: string; language?: string; error?: string }>;
    gitStatus?: () => Promise<any>;
    gitDiff?: (path: string) => Promise<any>;
    gitUndo?: (path: string) => Promise<any>;
    gitUndoAll?: () => Promise<any>;
    gitKeep?: (path: string) => Promise<any>;
    gitKeepAll?: () => Promise<any>;
    selectDirectory?: () => Promise<string | null>;
    setWorkspace?: (path: string) => Promise<string>;
    restartBackend?: () => Promise<{ ok: boolean; error?: string }>;
    getBackendStatus?: () => Promise<{ running: boolean; port: number; restartAttempts: number; pid: number | null }>;
    onBackendStatusChanged?: (callback: (payload: { status: string }) => void) => () => void;
  };
  terminalAPI?: {
    create: (options?: TerminalCreateOptions) => Promise<TerminalCreateResult>;
    write: (id: string, data: string) => Promise<{ ok: boolean }>;
    resize: (id: string, cols: number, rows: number) => Promise<{ ok: boolean; mode?: 'pty' }>;
    close: (id: string) => Promise<{ ok: boolean }>;
    onData: (callback: (payload: TerminalDataPayload) => void) => () => void;
    onExit: (callback: (payload: TerminalExitPayload) => void) => () => void;
  };
  electron?: {
    resizeWindow: (width: number, height: number) => void;
    minimizeWindow: () => void;
    toggleMaximizeWindow?: () => void;
    onWindowMaximizeStateChanged?: (callback: (state: { maximized: boolean }) => void) => () => void;
    closeWindow: () => void;
    hideWindow?: () => void;
    showSaveDialog?: (options?: ElectronSaveDialogOptions) => Promise<string | null>;
    showMessageBox?: (options: ElectronMessageBoxOptions) => Promise<ElectronMessageBoxResult>;
    showNotification?: (options?: { title?: string; body?: string; silent?: boolean }) => void;
    onUpdateAvailable: (callback: (info: any) => void) => void;
    onUpdateDownloaded: (callback: (info: any) => void) => void;
    onDownloadProgress: (callback: (progress: any) => void) => void;
    onUpdateError: (callback: (err: any) => void) => void;
    installUpdate: () => void;
    openExternal: (url: string) => void;
    openSystemSkillsDir: () => Promise<{ ok: boolean; path?: string; error?: string }>;
    onFileDrop?: (callback: (files: Array<{ name: string; path: string; type: string; size: number }>) => void) => void;
    consumeDroppedFiles?: () => Array<{ name: string; path: string; type: string; size: number }>;
    selectFiles?: () => Promise<Array<{ name: string; path: string; type: string; size: number }>>;
    wakeWord?: ElectronWakeWordAPI;
  };
}

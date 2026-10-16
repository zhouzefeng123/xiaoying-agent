import React, { useState, useEffect, useCallback, useRef } from 'react';
import { 
    Folder, ChevronRight, ChevronDown, X, 
    RotateCcw, List, FileCode, Terminal, Activity,
    GitCommit, AlertCircle, FolderOpen, Save, Loader2,
    FilePlus, FolderPlus, Edit, Trash2
} from 'lucide-react';
import { diffLines } from 'diff';
import { cn } from '../../lib/utils';
import { toast } from 'sonner';
import { CodeEditor } from './CodeEditor';
import { 
    normalizePath, 
    normalizeLineEndings, 
    readFile, 
    clearFileCache,
    getLanguageFromFilename,
    isBinaryFile
} from './fileService';

interface FileNode {
    name: string;
    path: string;
    type: 'file' | 'dir';
    children?: FileNode[];
}

interface GitStatusUpdate {
    path: string;
    status: string;
}

type FileChangeStatus = 'modified' | 'new' | 'deleted';

interface DirectoryChangeSummary {
    status: FileChangeStatus;
    count: number;
}

interface ExternalChangeNotice {
    path: string;
    type: 'modified' | 'deleted';
}

interface CodeBrowserOverlayProps {
    isOpen: boolean;
    onClose: () => void;
    onWorkingDirectoryChange?: (path: string) => void | Promise<void>;
    currentActivity?: string;
    activeFile?: string;
    activeOperations?: Map<string, { activity: string; file?: string }>;
    gitStatusUpdates?: GitStatusUpdate[];
    fileWatchPaths?: string[];
}

type AppRegionStyle = React.CSSProperties & { WebkitAppRegion?: 'drag' | 'no-drag' };

const API_BASE = '';

type DiffCellKind = 'context' | 'added' | 'removed' | 'empty';

interface DiffRow {
    leftLineNumber: number | null;
    rightLineNumber: number | null;
    leftContent: string;
    rightContent: string;
    leftKind: DiffCellKind;
    rightKind: DiffCellKind;
}

const changeStatusPriority: Record<FileChangeStatus, number> = {
    deleted: 3,
    modified: 2,
    new: 1
};

const getDominantStatus = (current: FileChangeStatus | undefined, next: FileChangeStatus): FileChangeStatus => {
    if (!current) {
        return next;
    }
    return changeStatusPriority[next] > changeStatusPriority[current] ? next : current;
};

const buildFileStatusMapFromBackend = (statusMap: Record<string, string>): Map<string, FileChangeStatus> => {
    const nextStatus = new Map<string, FileChangeStatus>();

    for (const [path, code] of Object.entries(statusMap)) {
        if (code === 'M') {
            nextStatus.set(path, 'modified');
        } else if (code === 'A' || code === '??') {
            nextStatus.set(path, 'new');
        } else if (code === 'D') {
            nextStatus.set(path, 'deleted');
        }
    }

    return nextStatus;
};

const areFileNodesEqual = (previous: FileNode[], next: FileNode[]): boolean => {
    if (previous === next) {
        return true;
    }

    if (previous.length !== next.length) {
        return false;
    }

    for (let index = 0; index < previous.length; index++) {
        const previousNode = previous[index];
        const nextNode = next[index];

        if (
            previousNode.path !== nextNode.path ||
            previousNode.name !== nextNode.name ||
            previousNode.type !== nextNode.type
        ) {
            return false;
        }
    }

    return true;
};

const applyLocalSelectedFileStatus = (
    statusMap: Map<string, FileChangeStatus>,
    selectedFile: string | null,
    originalContentMap: Map<string, string>,
    currentContent: string
) => {
    if (!selectedFile) {
        return statusMap;
    }

    const originalContent = originalContentMap.get(selectedFile);
    // 即使没有快照，也要保留后端返回的状态（不短路）
    if (originalContent === undefined) {
        return statusMap;
    }

    const nextStatusMap = new Map(statusMap);
    if (normalizeLineEndings(originalContent) === normalizeLineEndings(currentContent)) {
        nextStatusMap.delete(selectedFile);
        return nextStatusMap;
    }

    nextStatusMap.set(selectedFile, originalContent === '' ? 'new' : 'modified');
    return nextStatusMap;
};

const mergeCurrentLocalStatus = (
    statusMap: Map<string, FileChangeStatus>,
    selectedFile: string | null,
    originalContentMap: Map<string, string>,
    currentContent: string
) => applyLocalSelectedFileStatus(statusMap, selectedFile, originalContentMap, currentContent);

const splitPreserveLines = (content: string): string[] => {
    const normalized = normalizeLineEndings(content);
    if (!normalized) return [];

    const lines = normalized.split('\n');
    if (normalized.endsWith('\n')) {
        lines.pop();
    }
    return lines;
};

const buildDiffRows = (originalContent: string, currentContent: string): DiffRow[] => {
    const before = normalizeLineEndings(originalContent);
    const after = normalizeLineEndings(currentContent);

    if (before === after) {
        return [];
    }

    const changes = diffLines(before, after);
    const rows: DiffRow[] = [];
    let oldLineNumber = 1;
    let newLineNumber = 1;

    for (let i = 0; i < changes.length; i++) {
        const change = changes[i];

        if (change.removed && changes[i + 1]?.added) {
            const removedLines = splitPreserveLines(change.value);
            const addedLines = splitPreserveLines(changes[i + 1].value);
            const pairCount = Math.max(removedLines.length, addedLines.length);

            for (let pairIndex = 0; pairIndex < pairCount; pairIndex++) {
                const removedLine = removedLines[pairIndex];
                const addedLine = addedLines[pairIndex];

                rows.push({
                    leftLineNumber: removedLine !== undefined ? oldLineNumber++ : null,
                    rightLineNumber: addedLine !== undefined ? newLineNumber++ : null,
                    leftContent: removedLine ?? '',
                    rightContent: addedLine ?? '',
                    leftKind: removedLine !== undefined ? 'removed' : 'empty',
                    rightKind: addedLine !== undefined ? 'added' : 'empty'
                });
            }

            i++;
            continue;
        }

        const lines = splitPreserveLines(change.value);

        if (change.added) {
            lines.forEach(line => {
                rows.push({
                    leftLineNumber: null,
                    rightLineNumber: newLineNumber++,
                    leftContent: '',
                    rightContent: line,
                    leftKind: 'empty',
                    rightKind: 'added'
                });
            });
            continue;
        }

        if (change.removed) {
            lines.forEach(line => {
                rows.push({
                    leftLineNumber: oldLineNumber++,
                    rightLineNumber: null,
                    leftContent: line,
                    rightContent: '',
                    leftKind: 'removed',
                    rightKind: 'empty'
                });
            });
            continue;
        }

        lines.forEach(line => {
            rows.push({
                leftLineNumber: oldLineNumber,
                rightLineNumber: newLineNumber,
                leftContent: line,
                rightContent: line,
                leftKind: 'context',
                rightKind: 'context'
            });
            oldLineNumber++;
            newLineNumber++;
        });
    }

    return rows;
};

// Global Cache Maps
const fileTreeCache = new Map<string, { data: FileNode[]; timestamp: number }>();
const fileContentCache = new Map<string, { content: string; timestamp: number }>();
const CACHE_TTL = 5000; // 5 seconds，缩短缓存时间以更快响应文件变更

// Define electron API interface
interface ElectronAPI {
    listFiles: (path: string) => Promise<FileNode[]>;
    readFile: (path: string) => Promise<{ content: string; language: string; error?: string }>;
    selectDirectory: () => Promise<string | null>;
    setWorkspace: (path: string) => Promise<string>;
}

// Helper to access electron API safely
const getElectronAPI = () => {
    const apiWindow = window as typeof window & { electronAPI?: ElectronAPI };
    return apiWindow.electronAPI;
};

const FileTreeItem: React.FC<{ 
    node: FileNode; 
    depth: number; 
    onSelect: (path: string) => void;
    selectedPath?: string;
    refreshTrigger?: number;
    onContextMenu: (e: React.MouseEvent, path: string, type: 'file' | 'dir') => void;
    fileStatusMap: Map<string, FileChangeStatus>;
    directoryStatusMap: Map<string, DirectoryChangeSummary>;
}> = ({ node, depth, onSelect, selectedPath, refreshTrigger, onContextMenu, fileStatusMap, directoryStatusMap }) => {
    const [expanded, setExpanded] = useState(false);
    const [children, setChildren] = useState<FileNode[]>([]);
    const [loading, setLoading] = useState(false); // For file tree expansion
    const prevRefreshTriggerRef = React.useRef(refreshTrigger);

    const loadChildren = useCallback(async () => {
        // Prevent multiple simultaneous loads
        if (loading) return;
        
        // Check cache
        const cached = fileTreeCache.get(node.path);
        const now = Date.now();
        
        if (cached && (now - cached.timestamp) < CACHE_TTL && (!refreshTrigger || refreshTrigger === 0)) {
            setChildren(cached.data);
            return;
        }

        setLoading(true);
        try {
            const api = getElectronAPI();
            let data: FileNode[] = [];
            const timeoutMs = 8000;
            const withTimeout = <T,>(promise: Promise<T>) =>
                Promise.race<T>([
                    promise,
                    new Promise<T>((_, reject) => setTimeout(() => reject(new Error('timeout')), timeoutMs))
                ]);
            
            if (api && api.listFiles) {
                try {
                    data = await withTimeout(api.listFiles(node.path));
                } catch (ipcError) {
                    console.error("IPC listFiles failed", ipcError);
                    const controller = new AbortController();
                    const timer = setTimeout(() => controller.abort(), timeoutMs);
                    try {
                        const res = await fetch(`${API_BASE}/v1/files/list?path=${encodeURIComponent(node.path)}&t=${Date.now()}`, {
                            signal: controller.signal
                        });
                        if (!res.ok) throw new Error("Failed to fetch");
                        data = await res.json();
                    } finally {
                        clearTimeout(timer);
                    }
                }
            } else {
                const controller = new AbortController();
                const timer = setTimeout(() => controller.abort(), timeoutMs);
                const res = await fetch(`${API_BASE}/v1/files/list?path=${encodeURIComponent(node.path)}&t=${Date.now()}`, {
                    signal: controller.signal
                });
                clearTimeout(timer);
                if (!res.ok) throw new Error("Failed to fetch");
                data = await res.json();
            }
            
            const nodes = Array.isArray(data) ? data : [];
            setChildren(nodes);
            
            // Update cache
            fileTreeCache.set(node.path, { data: nodes, timestamp: now });
        } catch (err) {
            console.error("Failed to load directory", err);
            // Only toast if it's a real error, not just a cancellation
            // toast.error("无法打开文件夹"); 
        } finally {
            setLoading(false);
        }
    }, [node.path, refreshTrigger, loading]);

    // Auto-refresh when expanded and trigger changes
    useEffect(() => {
        if (expanded && node.type === 'dir') {
            if (refreshTrigger && refreshTrigger > 0 && refreshTrigger !== prevRefreshTriggerRef.current) {
                 prevRefreshTriggerRef.current = refreshTrigger;
                 loadChildren();
            }
        }
    }, [refreshTrigger, expanded, node.type, loadChildren]);

    // Initial load when expanded
    useEffect(() => {
        // Only load if expanded, is directory, has no children, not currently loading
        if (expanded && node.type === 'dir' && children.length === 0 && !loading) {
            // Check cache again to avoid double loading in strict mode or rapid re-renders
            const cached = fileTreeCache.get(node.path);
            const now = Date.now();
            if (cached && (now - cached.timestamp) < CACHE_TTL && (!refreshTrigger || refreshTrigger === 0)) {
                setChildren(cached.data);
            } else {
                loadChildren();
            }
        }
    }, [expanded, node.type, children.length, loading, loadChildren, node.path, refreshTrigger]);

    const normalizedNodePath = normalizePath(node.path);
    const fileStatus = node.type === 'file' ? fileStatusMap.get(normalizedNodePath) : undefined;
    const directorySummary = node.type === 'dir' ? directoryStatusMap.get(normalizedNodePath) : undefined;

    const fileBadgeClassName = fileStatus === 'modified'
        ? 'bg-blue-500/20 text-blue-300 border border-blue-500/20'
        : fileStatus === 'new'
            ? 'bg-green-500/20 text-green-300 border border-green-500/20'
            : 'bg-red-500/20 text-red-300 border border-red-500/20';

    const directoryDotClassName = directorySummary?.status === 'modified'
        ? 'bg-blue-400'
        : directorySummary?.status === 'new'
            ? 'bg-green-400'
            : 'bg-red-400';

    return (
        <div className="select-none">
            <div 
                className={cn(
                    "flex items-center gap-1.5 px-2 py-1 cursor-pointer hover:bg-white/5 transition-colors text-sm",
                    selectedPath === node.path 
                        ? "bg-blue-600/20 text-blue-400 hover:bg-blue-600/30" 
                        : "text-zinc-300 hover:text-white",
                    depth > 0 && "border-l border-white/5"
                )}
                style={{ paddingLeft: `${depth * 12 + 8}px` }}
                onClick={() => {
                    if (node.type === 'dir') {
                        setExpanded(!expanded);
                    } else {
                        onSelect(node.path);
                    }
                }}
                onContextMenu={(e) => {
                    e.preventDefault();
                    e.stopPropagation();
                    onContextMenu(e, node.path, node.type);
                }}
            >
                {node.type === 'dir' ? (
                    expanded ? <ChevronDown className="w-3.5 h-3.5 text-zinc-500" /> : <ChevronRight className="w-3.5 h-3.5 text-zinc-500" />
                ) : (
                    <div className="w-3.5" />
                )}
                
                {node.type === 'dir' ? (
                    <Folder className={cn(
                        "w-4 h-4",
                        expanded ? "text-blue-400" : "text-blue-500/70"
                    )} />
                ) : (
                    <FileCode className="w-4 h-4 text-zinc-500" />
                )}
                
                <span className={cn(
                    "truncate min-w-0",
                    node.name.startsWith('.') && "text-zinc-500"
                )}>
                    {node.name}
                </span>

                <div className="ml-auto flex items-center gap-1 pl-2">
                    {directorySummary && (
                        <span
                            className={cn(
                                "inline-flex items-center gap-1 rounded-full px-1.5 py-0.5 text-[10px] font-medium text-zinc-200 bg-white/5",
                                selectedPath === node.path && "bg-white/10"
                            )}
                            title={`此目录下有 ${directorySummary.count} 个变更项`}
                        >
                            <span className={cn("h-1.5 w-1.5 rounded-full", directoryDotClassName)} />
                            {directorySummary.count}
                        </span>
                    )}

                    {fileStatus && (
                        <span
                            className={cn("rounded px-1.5 py-0.5 text-[10px] font-bold uppercase", fileBadgeClassName)}
                            title={fileStatus === 'modified' ? '已修改' : fileStatus === 'new' ? '新增' : '已删除'}
                        >
                            {fileStatus === 'modified' ? 'M' : fileStatus === 'new' ? 'A' : 'D'}
                        </span>
                    )}
                </div>
            </div>
            
            {expanded && (
                <div>
                    {loading ? (
                        <div className="pl-6 py-1 text-xs text-zinc-600 animate-pulse">Loading...</div>
                    ) : (
                        children.map(child => (
                            <FileTreeItem 
                                key={child.path} 
                                node={child} 
                                depth={depth + 1} 
                                onSelect={onSelect}
                                selectedPath={selectedPath}
                                refreshTrigger={refreshTrigger}
                                onContextMenu={onContextMenu}
                                fileStatusMap={fileStatusMap}
                                directoryStatusMap={directoryStatusMap}
                            />
                        ))
                    )}
                </div>
            )}
        </div>
    );
};

export const CodeBrowserOverlay: React.FC<CodeBrowserOverlayProps> = ({ 
    isOpen, 
    onClose, 
    onWorkingDirectoryChange,
    currentActivity, 
    activeFile,
    activeOperations,
    gitStatusUpdates,
    fileWatchPaths
}) => {
    const [rootFiles, setRootFiles] = useState<FileNode[]>([]);
    const [selectedFile, setSelectedFile] = useState<string | null>(null);
    const [fileLang, setFileLang] = useState<string>('text');
    const [diffContent, setDiffContent] = useState<string | null>(null);
    const [isContentLoading, setIsContentLoading] = useState(false);
    const [showChangesModal, setShowChangesModal] = useState(false); 
    const [currentPath, setCurrentPath] = useState<string>('.');

    const [editContent, setEditContent] = useState(''); // Buffer for CodeEditor
    const [externalChangeNotice, setExternalChangeNotice] = useState<ExternalChangeNotice | null>(null);
    const [editorViewMode, setEditorViewMode] = useState<'editor' | 'diff'>('editor');

    // --- Frontend Snapshot & Diff State ---
    // Stores the "original" content of files before user/AI edits in this session
    const [originalContentMap, setOriginalContentMap] = useState<Map<string, string>>(new Map());
    // Tracks which files are modified/new/deleted compared to originalContentMap
    const [fileStatusMap, setFileStatusMap] = useState<Map<string, FileChangeStatus>>(new Map());
    
    // Derived changed files list for UI
    const changedFiles = Array.from(fileStatusMap.entries()).map(([path, status]) => ({
        path,
        status: status === 'modified' ? 'M' : status === 'new' ? 'A' : 'D'
    }));

    const normalizedFileStatusMap = React.useMemo(() => {
        const next = new Map<string, FileChangeStatus>();
        fileStatusMap.forEach((status, path) => {
            next.set(normalizePath(path), status);
        });
        return next;
    }, [fileStatusMap]);

    const directoryStatusMap = React.useMemo(() => {
        const next = new Map<string, DirectoryChangeSummary>();

        normalizedFileStatusMap.forEach((status, filePath) => {
            const segments = filePath.split('/').filter(Boolean);
            let currentDir = '';

            for (let index = 0; index < segments.length - 1; index++) {
                currentDir = currentDir ? `${currentDir}/${segments[index]}` : segments[index];
                const existing = next.get(currentDir);
                next.set(currentDir, {
                    status: getDominantStatus(existing?.status, status),
                    count: (existing?.count ?? 0) + 1
                });
            }
        });

        return next;
    }, [normalizedFileStatusMap]);

    const [sidebarWidth, setSidebarWidth] = useState(256); // Default 256px
    const [isResizing, setIsResizing] = useState(false);
    
    // Refresh trigger for file tree
    const [refreshTrigger, setRefreshTrigger] = useState(0);
    const refreshDebounceRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);
    const currentPathRef = React.useRef(currentPath);
    const wasOpenRef = React.useRef(false);
    const selectedFileRef = React.useRef<string | null>(null);
    const editContentRef = React.useRef(editContent);
    const originalContentMapRef = React.useRef(originalContentMap);

    // File Operations State
    const [contextMenu, setContextMenu] = useState<{ x: number; y: number; path: string; type: 'file' | 'dir' } | null>(null);
    const [modal, setModal] = useState<{ type: 'new-file' | 'new-folder' | 'rename' | 'delete'; path: string; name?: string } | null>(null);
    const [inputName, setInputName] = useState('');
    const sidebarRef = React.useRef<HTMLDivElement | null>(null);

    const handleContextMenu = useCallback((e: React.MouseEvent, path: string, type: 'file' | 'dir') => {
        const rowRect = (e.currentTarget as HTMLElement).getBoundingClientRect();
        const sidebarRect = sidebarRef.current?.getBoundingClientRect();
        const menuWidth = 170;
        const menuHeight = 180;
        const padding = 8;
        let x = padding;
        let y = rowRect.bottom + 2;

        if (sidebarRect) {
            y = rowRect.bottom - sidebarRect.top + 2;
            const maxY = Math.max(padding, sidebarRect.height - menuHeight - padding);
            y = Math.min(Math.max(y, padding), maxY);
            const maxX = Math.max(padding, sidebarRect.width - menuWidth - padding);
            x = Math.min(x, maxX);
        } else {
            if (x + menuWidth > window.innerWidth - padding) {
                x = Math.max(padding, window.innerWidth - menuWidth - padding);
            }
            if (y + menuHeight > window.innerHeight - padding) {
                y = Math.max(padding, rowRect.top - menuHeight - 2);
            }
        }

        setContextMenu({ x, y, path, type });
    }, []);

    // Close menu on click elsewhere
    useEffect(() => {
        const handleClick = () => setContextMenu(null);
        window.addEventListener('click', handleClick);
        return () => window.removeEventListener('click', handleClick);
    }, []);

    const handleCreateFile = async () => {
        if (!modal || !inputName) return;
        const parentPath = modal.type === 'new-file' ? modal.path : ''; // path is the directory
        // If right clicked on file, parent is its parent dir.
        // But our context menu logic:
        // If dir: create inside.
        // If file: create inside parent dir.
        
        // Wait, contextMenu.path is the node path.
        // Let's resolve parent logic when opening modal.
        
        // Construct full path
        // inputName might be relative? "foo.txt"
        // If parentPath is ".", join is simple.
        
        // We need to know the target directory.
        // Let's assume modal.path is the TARGET DIRECTORY.
        
        let targetPath = modal.path;
        if (targetPath === '.' || targetPath === './') targetPath = '';
        
        const fullPath = targetPath ? `${targetPath}/${inputName}` : inputName;
        
        try {
            const res = await fetch(`${API_BASE}/v1/files/create`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ path: fullPath })
            });
            if (res.ok) {
                toast.success(`Created ${fullPath}`);
                handleRefresh();
                setModal(null);
            } else {
                const err = await res.text();
                toast.error(`Failed to create file: ${err}`);
            }
        } catch {
            toast.error("Network error");
        }
    };

    const handleCreateFolder = async () => {
        if (!modal || !inputName) return;
        
        let targetPath = modal.path;
        if (targetPath === '.' || targetPath === './') targetPath = '';
        
        const fullPath = targetPath ? `${targetPath}/${inputName}` : inputName;

        try {
            const res = await fetch(`${API_BASE}/v1/files/mkdir`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ path: fullPath })
            });
            if (res.ok) {
                toast.success(`Created directory ${fullPath}`);
                handleRefresh();
                setModal(null);
            } else {
                const err = await res.text();
                toast.error(`Failed to create directory: ${err}`);
            }
        } catch {
            toast.error("Network error");
        }
    };

    const handleRename = async () => {
        if (!modal || !inputName) return;
        
        // modal.path is the old path
        // We need to construct new path.
        // Get parent dir of old path
        const parentDir = modal.path.includes('/') ? modal.path.substring(0, modal.path.lastIndexOf('/')) : '';
        const newPath = parentDir ? `${parentDir}/${inputName}` : inputName;
        
        if (newPath === modal.path) {
            setModal(null);
            return;
        }

        try {
            const res = await fetch(`${API_BASE}/v1/files/move`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ from: modal.path, to: newPath })
            });
            if (res.ok) {
                toast.success(`Renamed to ${inputName}`);
                // If renamed active file, update selection?
                if (selectedFile === modal.path) {
                    setSelectedFile(newPath);
                }
                handleRefresh();
                setModal(null);
            } else {
                const err = await res.text();
                toast.error(`Failed to rename: ${err}`);
            }
        } catch {
            toast.error("Network error");
        }
    };

    const handleDelete = async () => {
        if (!modal) return;
        
        try {
            const res = await fetch(`${API_BASE}/v1/files/delete`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ path: modal.path })
            });
            if (res.ok) {
                toast.success(`Deleted ${modal.path}`);
                if (selectedFile === modal.path) {
                    setSelectedFile(null);
                    setEditContent('');
                    setExternalChangeNotice(null);
                }
                handleRefresh();
                setModal(null);
            } else {
                const err = await res.text();
                toast.error(`Failed to delete: ${err}`);
            }
        } catch {
            toast.error("Network error");
        }
    };

    // Error and loading states
    const [fileError, setFileError] = useState<string | null>(null);
    
    // Auto-scroll for operations log - REMOVED

    // Derived filtered files based on currentPath
    const filteredChangedFiles = changedFiles.filter(file => {
        // If currentPath is root, show all
        if (currentPath === '.' || currentPath === './' || currentPath === '') return true;
        
        // Normalize paths to use forward slashes for comparison
        const normalize = (p: string) => p.replace(/\\/g, '/');
        const fileP = normalize(file.path);
        const currentP = normalize(currentPath).replace(/\/$/, ''); // Remove trailing slash
        
        // Case 1: File is directly inside currentPath
        // Case 2: File is inside a subdirectory of currentPath (recursive)
        // Check if fileP starts with currentP + '/'
        
        // Handle exact match (should not happen for file vs dir but good for safety)
        if (fileP === currentP) return true;
        
        // Check prefix
        if (fileP.startsWith(currentP + '/')) {
            return true;
        }
        
        return false; 
    });

    // Helper: calculate diff for a file using Backend API
    // Removed calculateFileDiff as it is no longer needed

    // Helper: Update file status
    const updateFileStatus = useCallback(async (path: string, currentContent: string | null) => {
        // If currentContent is null, file is deleted
        // If originalContentMap has it, and currentContent is null -> Deleted
        // If originalContentMap has it, and currentContent != original -> Modified
        // If originalContentMap has it, and currentContent == original -> Clean (remove from status)
        // If originalContentMap DOES NOT have it -> We assume it's existing file unless we track "New" specifically.
        // But for "New" files created by AI, we set original to empty string.
        
        const original = originalContentMap.get(path);
        
        // If we don't have a snapshot, we can't track changes yet.
        // Unless we want to track *everything*? No, only tracked files.
        if (original === undefined) return;

        setFileStatusMap(prev => {
            const next = new Map(prev);
            
            if (currentContent === null) {
                // Deleted
                next.set(path, 'deleted');
            } else if (normalizeLineEndings(currentContent) !== normalizeLineEndings(original)) {
                // Modified (or New if original was empty)
                if (original === '') {
                     next.set(path, 'new');
                } else {
                     next.set(path, 'modified');
                }
            } else {
                // Clean / Reverted
                next.delete(path);
            }
            return next;
        });
    }, [originalContentMap]);


    // Load file content
    const loadFile = useCallback(async (
        path: string,
        options: { force?: boolean; replaceSnapshot?: boolean } = {}
    ) => {
        if (!path) return;
        const { force = false, replaceSnapshot = false } = options;
        
        // Check cache
        const cached = fileContentCache.get(path);
        const now = Date.now();
        
        if (!force && cached && (now - cached.timestamp) < CACHE_TTL) {
            setEditContent(cached.content);
            setFileLang(getLanguageFromFilename(path));
            setFileError(null);
            
            // 初始化或替换基线，确保外部刷新后 diff 基准是最新磁盘内容。
            setOriginalContentMap(prev => {
                if (replaceSnapshot || !prev.has(path)) {
                    return new Map(prev).set(path, cached.content);
                }
                return prev;
            });

            // replaceSnapshot 时不再主动清除 fileStatusMap，由 loadChanges() 统一从后端同步
            return;
        }
        
        if (isBinaryFile(path)) {
            // ... (binary file handling)
            setEditContent('');
            setFileLang('text');
            setFileError('Binary or large file not supported for preview/editing.');
            return;
        }

        setIsContentLoading(true);
        setFileError(null);
        
        try {
            const api = getElectronAPI();
            let content = '';
            
            if (api && api.readFile) {
                try {
                    const result = await api.readFile(path);
                    if (result.error) {
                        const res = await fetch(`${API_BASE}/v1/files/read?path=${encodeURIComponent(path)}&t=${Date.now()}`);
                        if (!res.ok) throw new Error(result.error);
                        const data = await res.json();
                        content = data.content;
                    } else {
                        content = result.content;
                    }
                } catch (ipcErr) {
                    console.error("IPC readFile failed", ipcErr);
                    const res = await fetch(`${API_BASE}/v1/files/read?path=${encodeURIComponent(path)}&t=${Date.now()}`);
                    if (!res.ok) throw ipcErr;
                    const data = await res.json();
                    content = data.content;
                }
            } else {
                const res = await fetch(`${API_BASE}/v1/files/read?path=${encodeURIComponent(path)}&t=${Date.now()}`);
                if (!res.ok) throw new Error("Failed to fetch");
                const data = await res.json();
                content = data.content;
            }

            setEditContent(content);
            setFileLang(getLanguageFromFilename(path));
            
            // Update cache
            fileContentCache.set(path, { content, timestamp: now });

            // 初始化或替换基线，确保外部刷新后 diff 基准是最新磁盘内容。
            setOriginalContentMap(prev => {
                if (replaceSnapshot || !prev.has(path)) {
                    return new Map(prev).set(path, content);
                }
                return prev;
            });

            // replaceSnapshot 时不再主动清除 fileStatusMap，由 loadChanges() 统一从后端同步
            // 避免前端状态和后端真实状态不一致导致闪烁

        } catch (err) {
            console.error(err);
            setFileError("无法读取文件");
            setEditContent("");
        } finally {
            setIsContentLoading(false);
        }
    }, []);

    // Diff 改为前端基于首次加载基线实时计算，避免无快照文件被误判为整文件新增。
    const loadDiff = useCallback(async (path: string) => {
        // 基于 originalContentMap 中的快照计算 diff
        const original = originalContentMapRef.current.get(path);
        const current = editContentRef.current;
        
        if (original === undefined || !current) {
            setDiffContent(null);
            return;
        }
        
        // 如果内容相同，不显示 diff
        if (normalizeLineEndings(original) === normalizeLineEndings(current)) {
            setDiffContent(null);
            return;
        }
        
        // 有差异，设置标志触发显示
        setDiffContent('computed');
    }, []);

    const startResizing = useCallback(() => {
        setIsResizing(true);
    }, []);

    const stopResizing = useCallback(() => {
        setIsResizing(false);
    }, []);

    const resize = useCallback((mouseMoveEvent: MouseEvent) => {
        if (isResizing) {
            const newWidth = mouseMoveEvent.clientX;
            if (newWidth > 150 && newWidth < 600) {
                setSidebarWidth(newWidth);
            }
        }
    }, [isResizing]);

    useEffect(() => {
        if (isResizing) {
            window.addEventListener("mousemove", resize);
            window.addEventListener("mouseup", stopResizing);
        }
        return () => {
            window.removeEventListener("mousemove", resize);
            window.removeEventListener("mouseup", stopResizing);
        };
    }, [isResizing, resize, stopResizing]);

    // AI Snapshot Capture
    useEffect(() => {
        if (!activeOperations) return;
        
        activeOperations.forEach((op, opId) => {
            // Check if this operation involves file modification
            // 'SearchReplace' tool activity usually starts with "Searching" or "Replacing" or "Editing"
            // The activity string is what the backend sends.
            // Standard tool names: write, replace, edit
            if (op.activity.includes('Writing') || op.activity.includes('Editing') || op.activity.includes('Patching') || op.activity.includes('Replacing')) {
                const path = op.file;
                if (path) {
                    // Try to capture snapshot BEFORE AI overwrites it
                    // We only capture if we don't have it yet
                    const capture = async () => {
                        // Double check inside async
                        // Access current map from state setter to be safe, or ref
                        // Here we use state setter callback logic
                        
                        let content = '';
                        try {
                            const api = getElectronAPI();
                            if (api && api.readFile) {
                                const res = await api.readFile(path);
                                content = res.content;
                            } else {
                                const res = await fetch(`${API_BASE}/v1/files/read?path=${encodeURIComponent(path)}`);
                                if (res.ok) {
                                    const data = await res.json();
                                    content = data.content;
                                }
                            }
                        } catch (e) {
                            // File likely new, content is empty
                            content = '';
                        }

                        setOriginalContentMap(prev => {
                            if (!prev.has(path)) {
                                console.log(`[Snapshot] Captured for ${path}, length: ${content.length}`);
                                return new Map(prev).set(path, content);
                            }
                            return prev;
                        });
                    };
                    capture();
                }
            }
        });
    }, [activeOperations]); // eslint-disable-line react-hooks/exhaustive-deps

    // 复用后端 fsnotify + WebSocket 事件，做增量状态同步与去抖刷新，行为接近 VS Code。
    useEffect(() => {
        if (!isOpen) {
            return;
        }

        const normalizedWatchPaths = (fileWatchPaths || [])
            .map(normalizePath)
            .filter(Boolean);
        const normalizedStatusUpdates = (gitStatusUpdates || []).map(update => ({
            path: normalizePath(update.path),
            status: update.status
        }));

        if (normalizedWatchPaths.length === 0 && normalizedStatusUpdates.length === 0) {
            return;
        }

        if (normalizedStatusUpdates.length > 0) {
            setFileStatusMap(prev => {
                const next = new Map(prev);
                normalizedStatusUpdates.forEach(update => {
                    if (update.status === 'M') {
                        next.set(update.path, 'modified');
                    } else if (update.status === 'A' || update.status === '??') {
                        next.set(update.path, 'new');
                    } else if (update.status === 'D') {
                        next.set(update.path, 'deleted');
                    } else if (update.status === 'C') {
                        next.delete(update.path);
                    }
                });
                return mergeCurrentLocalStatus(
                    next,
                    selectedFileRef.current,
                    originalContentMapRef.current,
                    editContentRef.current
                );
            });
        }

        if (refreshDebounceRef.current) {
            clearTimeout(refreshDebounceRef.current);
        }

        refreshDebounceRef.current = setTimeout(() => {
            fileTreeCache.clear();

            const currentSelectedFile = selectedFileRef.current;
            const normalizedSelectedFile = currentSelectedFile ? normalizePath(currentSelectedFile) : null;
            const selectedStatus = normalizedSelectedFile
                ? normalizedStatusUpdates.find(update => update.path === normalizedSelectedFile)?.status
                : undefined;
            const selectedPathChanged = normalizedSelectedFile
                ? normalizedWatchPaths.includes(normalizedSelectedFile) || Boolean(selectedStatus)
                : false;

            if (normalizedSelectedFile && selectedStatus === 'D') {
                fileContentCache.delete(currentSelectedFile!);
                setSelectedFile(null);
                setEditContent('');
                setDiffContent(null);
                setFileError('当前文件已被外部删除');
            } else if (currentSelectedFile && selectedPathChanged) {
                const originalContent = originalContentMapRef.current.get(currentSelectedFile);
                const hasLocalUnsavedChanges = originalContent !== undefined &&
                    normalizeLineEndings(originalContent) !== normalizeLineEndings(editContentRef.current);

                if (hasLocalUnsavedChanges) {
                    setExternalChangeNotice({
                        path: currentSelectedFile,
                        type: selectedStatus === 'D' ? 'deleted' : 'modified'
                    });
                } else {
                    setExternalChangeNotice(null);
                    // 同时清除内容缓存和快照，强制从磁盘重新加载
                    fileContentCache.delete(currentSelectedFile);
                    loadFile(currentSelectedFile, { force: true, replaceSnapshot: true }).then(() => {
                        // 加载完成后重新计算 diff
                        loadDiff(currentSelectedFile);
                    });
                }
            }

            // 刷新文件树以显示新的状态标记
            fileTreeCache.clear();
            setRefreshTrigger(prev => prev + 1);
            fetchRoot(currentPathRef.current);
        }, 180);

        return () => {
            if (refreshDebounceRef.current) {
                clearTimeout(refreshDebounceRef.current);
                refreshDebounceRef.current = null;
            }
        };
    }, [isOpen, fileWatchPaths, gitStatusUpdates, loadFile]);
    
    // Trigger refresh when snapshot map changes (e.g. AI modified a file and we captured snapshot)
    useEffect(() => {
        // When originalContentMap changes, it means we started tracking new files.
        // We should check their current status.
        // Also refresh the file tree to reflect potential new files.
        
        // Use a timeout to debounce multiple snapshot updates
        const timeout = setTimeout(() => {
            // Refresh file tree if we have a path
            if (currentPath) fetchRoot(currentPath);
            
            // Refresh diff for selected file
            if (selectedFile) loadDiff(selectedFile);
            
            // Re-evaluate all tracked files status using Backend API
            const checkAllStatuses = async () => {
                try {
                    const res = await fetch(`${API_BASE}/v1/files/status`);
                    if (res.ok) {
                        const statusMap: Record<string, string> = await res.json();
                        const backendStatusMap = buildFileStatusMapFromBackend(statusMap);
                        // 直接用后端状态替换（后端是权威来源），仅叠加当前编辑文件的本地状态
                        setFileStatusMap(
                            mergeCurrentLocalStatus(
                                backendStatusMap,
                                selectedFileRef.current,
                                originalContentMapRef.current,
                                editContentRef.current
                            )
                        );
                        // 刷新文件树以显示新的状态标记
                        fileTreeCache.clear();
                        setRefreshTrigger(prev => prev + 1);
                    }
                } catch (e) {
                    console.error("Failed to fetch file status", e);
                }
            };
            
            checkAllStatuses();
            
        }, 500); // 500ms debounce
        
        return () => clearTimeout(timeout);
    }, [originalContentMap]); // Trigger when snapshots change

    // Refresh trigger effect - now we need something to trigger it.
    // Maybe we expose a refresh button? Or auto-refresh on window focus?
    useEffect(() => {
        const handleFocus = () => {
            if (isOpen) {
                // Refresh current view
                if (currentPath) fetchRoot(currentPath);
                // Refresh diff if selected
                if (selectedFile) loadDiff(selectedFile);
            }
        };
        window.addEventListener('focus', handleFocus);
        return () => window.removeEventListener('focus', handleFocus);
    }, [isOpen, currentPath, selectedFile, loadDiff]);

    // Also trigger refresh when activeOperations complete (AI done)
    // Use a ref to track previous active operations size to detect "completion" event
    const prevActiveOpsSizeRef = React.useRef(0);
    const checkStatusTimersRef = React.useRef<ReturnType<typeof setTimeout>[]>([]);

    useEffect(() => {
        const currentSize = activeOperations ? activeOperations.size : 0;
        const prevSize = prevActiveOpsSizeRef.current;
        prevActiveOpsSizeRef.current = currentSize;

        // Only trigger when going from "busy" (size > 0) to "idle" (size === 0)
        if (prevSize > 0 && currentSize === 0) {
            // Clear any existing timers
            checkStatusTimersRef.current.forEach(clearTimeout);
            checkStatusTimersRef.current = [];

            const checkAllStatuses = async () => {
                try {
                    const res = await fetch(`${API_BASE}/v1/files/status`);
                    if (res.ok) {
                        const statusMap: Record<string, string> = await res.json();
                        setFileStatusMap(
                            mergeCurrentLocalStatus(
                                buildFileStatusMapFromBackend(statusMap),
                                selectedFileRef.current,
                                originalContentMapRef.current,
                                editContentRef.current
                            )
                        );
                    }
                } catch (e) {
                    console.error("Failed to fetch file status", e);
                }
            };
            
            const runCheck = () => {
                checkAllStatuses();
                if (currentPath) fetchRoot(currentPath);
                if (selectedFile) loadDiff(selectedFile);
            };

            // Schedule checks
            const t1 = setTimeout(runCheck, 500);
            const t2 = setTimeout(runCheck, 1500);
            const t3 = setTimeout(runCheck, 3000);
            
            checkStatusTimersRef.current.push(t1, t2, t3);
        }
        
        // Cleanup on unmount or re-run
        return () => {
            // We DON'T want to clear timers if activeOperations just changed size but still > 0
            // But if this effect re-runs, we should probably clear previous timers to avoid duplicates
            // if we are re-entering the "idle" state?
            // Actually, since we check (prev > 0 && current === 0), this block only runs ONCE
            // when becoming idle.
            // So we don't strictly need to clear timers in cleanup unless we want to cancel checks
            // if user starts new operations immediately.
            // Yes, if user starts new op, we should cancel pending checks.
            if (activeOperations && activeOperations.size > 0) {
                 checkStatusTimersRef.current.forEach(clearTimeout);
                 checkStatusTimersRef.current = [];
            }
        };
    }, [activeOperations, currentPath, selectedFile, loadDiff, originalContentMap, fileStatusMap]);

    // Effect to track agent operations history - REMOVED

    const syncWorkspace = useCallback(async () => {
        try {
            const res = await fetch(`${API_BASE}/v1/system/cwd`);
            if (res.ok) {
                const data = await res.json();
                if (data.path && data.path !== '.') {
                    const api = getElectronAPI();
                    if (api && api.setWorkspace) {
                        await api.setWorkspace(data.path);
                        console.log("Synced electron workspace to:", data.path);
                    }
                    return data.path;
                }
            }
        } catch (err) {
            console.error("Failed to sync workspace", err);
        }
        return null;
    }, []);

    useEffect(() => {
        currentPathRef.current = currentPath;
    }, [currentPath]);

    useEffect(() => {
        selectedFileRef.current = selectedFile;
    }, [selectedFile]);

    useEffect(() => {
        editContentRef.current = editContent;
    }, [editContent]);

    useEffect(() => {
        originalContentMapRef.current = originalContentMap;
    }, [originalContentMap]);

    useEffect(() => {
        if (selectedFile) {
            setEditorViewMode('editor');
            // Don't clear content immediately if it's already loading from onSelect
            // setEditContent(''); 
            setDiffContent(null);
            // setIsContentLoading(true); // Already set in onSelect

            // 先加载文件，确保快照已设置后再计算 diff
            loadFile(selectedFile).then(() => {
                // 文件加载完成后，基于快照和当前内容计算 diff
                loadDiff(selectedFile);
            }).finally(() => {
                setIsContentLoading(false);
            });
        } else {
            setEditContent('');
            setDiffContent(null);
            setExternalChangeNotice(null);
        }
    }, [selectedFile, loadFile, loadDiff]);

    useEffect(() => {
        if (!selectedFile) {
            return;
        }

        updateFileStatus(selectedFile, editContent);
    }, [selectedFile, editContent, updateFileStatus]);

    const fetchRoot = useCallback(async (path: string = '.') => {
        try {
            const api = getElectronAPI();
            let data: FileNode[] = [];
            
            if (api && api.listFiles) {
                // Wrap in try-catch to prevent IPC crash
                try {
                    data = await api.listFiles(path);
                } catch (e) {
                    console.error("IPC listFiles failed", e);
                    data = [];
                }
            } else {
                const res = await fetch(`${API_BASE}/v1/files/list?path=${encodeURIComponent(path)}&t=${Date.now()}`);
                if (!res.ok) throw new Error("Failed to fetch root");
                data = await res.json();
            }
            const newData = Array.isArray(data) ? data : [];
            
            // Optimization: Only update if data changed
            setRootFiles(prev => {
                if (areFileNodesEqual(prev, newData)) return prev;
                return newData;
            });
            setCurrentPath(path);
        } catch (err) {
            console.error(err);
            toast.error(`Failed to load directory: ${path}`);
        }
    }, []);

    const loadChanges = useCallback(async () => {
        try {
            const res = await fetch(`${API_BASE}/v1/files/status`);
            if (res.ok) {
                const statusMap: Record<string, string> = await res.json();
                const backendStatusMap = buildFileStatusMapFromBackend(statusMap);
                // 直接用后端状态替换（后端是权威来源），仅叠加当前编辑文件的本地状态
                setFileStatusMap(
                    mergeCurrentLocalStatus(
                        backendStatusMap,
                        selectedFileRef.current,
                        originalContentMapRef.current,
                        editContentRef.current
                    )
                );
                // 刷新文件树以显示新的状态标记
                fileTreeCache.clear();
                setRefreshTrigger(prev => prev + 1);
            }
        } catch (e) {
            console.error("Failed to load changes", e);
        }
    }, []);

    const refreshDirectoryOnOpen = useCallback(async () => {
        // 打开编程模式时强制刷新目录树和变更状态，避免沿用旧缓存。
        fileTreeCache.clear();
        setRefreshTrigger(prev => prev + 1);

        const path = await syncWorkspace();
        const fallbackPath = currentPathRef.current;
        const targetPath = path && path !== '.' ? path : fallbackPath;

        if (targetPath !== fallbackPath) {
            setCurrentPath(targetPath);
        }

        await Promise.all([
            fetchRoot(targetPath),
            loadChanges()
        ]);
    }, [syncWorkspace, fetchRoot, loadChanges]);

    useEffect(() => {
        const justOpened = isOpen && !wasOpenRef.current;
        wasOpenRef.current = isOpen;

        if (!justOpened) {
            return;
        }

        void refreshDirectoryOnOpen();
    }, [isOpen, refreshDirectoryOnOpen]);

    const syncBackendCwd = useCallback(async () => {
        try {
            if (onWorkingDirectoryChange) {
                await Promise.resolve(onWorkingDirectoryChange(currentPath));
                return true;
            }

            const res = await fetch(`${API_BASE}/v1/system/cwd`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ path: currentPath })
            });
            return res.ok;
        } catch {
            return false;
        }
    }, [currentPath, onWorkingDirectoryChange]);

    const handleShowChanges = async () => {
        const cwdOk = await syncBackendCwd();
        if (!cwdOk) {
            toast.warning("后端服务未连接，请检查后端是否正常运行");
            return;
        }
        await loadChanges();
        setShowChangesModal(true);
    };

    // Helper to detect language from filename
    const getLanguageFromFilename = (filename: string): string => {
        if (!filename) return 'text';
        const base = filename.split(/[/\\]/).pop()?.toLowerCase() || '';

        // Special cases for files without extension or dotfiles
        if (base === 'dockerfile') return 'dockerfile';
        if (base === 'makefile') return 'makefile';
        if (base === 'jenkinsfile') return 'groovy';
        if (base.startsWith('.env')) return 'ini';
        
        const ext = filename.split('.').pop()?.toLowerCase();
        if (!ext || ext === filename) return 'text';
        
        return ext;
    };

    // Helper to check if file is likely binary
    const isBinaryFile = (filename: string): boolean => {
        const ext = filename.split('.').pop()?.toLowerCase();
        if (!ext) return false;
        const binaryExts = [
            'zip', 'tar', 'gz', 'rar', '7z', 'iso', 'img',
            'exe', 'dll', 'so', 'dylib', 'bin', 'obj', 'lib',
            'png', 'jpg', 'jpeg', 'gif', 'bmp', 'ico', 'webp', 'tiff',
            'mp3', 'wav', 'ogg', 'flac', 'aac',
            'mp4', 'avi', 'mov', 'mkv', 'webm',
            'pdf', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx',
            'pyc', 'class', 'jar', 'war', 'ear',
            'db', 'sqlite', 'sqlite3', 'db3', 's3db',
            'psd', 'ai', 'sketch', 'fig'
        ];
        return binaryExts.includes(ext);
    };

    const handleKeep = async (path: string) => {
        try {
            await syncBackendCwd();

            if (selectedFile === path) {
                const saveRes = await fetch(`${API_BASE}/v1/files/save`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        path,
                        content: editContent
                    })
                });

                if (!saveRes.ok) {
                    const saveErr = await saveRes.text();
                    toast.error(`Failed to save before keep: ${saveErr}`);
                    return;
                }

                fileContentCache.set(path, { content: editContent, timestamp: Date.now() });
            }

            const res = await fetch(`${API_BASE}/v1/files/keep`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ path })
            });

            if (res.ok) {
                toast.success(`Changes kept for ${path}`);
                setExternalChangeNotice(null);
                
                // 更新快照：如果是当前编辑文件，使用当前内容；否则从磁盘读取
                setOriginalContentMap(prev => {
                    const next = new Map(prev);
                    if (selectedFile === path) {
                        // 当前编辑文件，使用编辑内容作为新基线
                        next.set(path, editContent);
                    } else {
                        // 其他文件，从快照中删除（表示已提交）
                        next.delete(path);
                    }
                    originalContentMapRef.current = next;
                    return next;
                });
                
                // 清除该文件的状态标记
                setFileStatusMap(prev => {
                    const next = new Map(prev);
                    next.delete(path);
                    return next;
                });
                
                // 如果是当前编辑文件，重新计算 diff
                if (selectedFile === path) {
                    loadDiff(path);
                }
                
                setRefreshTrigger(prev => prev + 1);
                fetchRoot(currentPath);
            } else {
                const err = await res.text();
                toast.error(`Failed to keep changes: ${err}`);
            }
        } catch {
            toast.error("Network error");
        }
    };

    const handleKeepAll = async () => {
        if (!confirm("Are you sure you want to keep ALL changes? This will clear the undo history for this session.")) return;
        
        try {
            await syncBackendCwd();
            const res = await fetch(`${API_BASE}/v1/files/keep-all`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ path: currentPath })
            });

            if (res.ok) {
                toast.success("All changes kept");
                setExternalChangeNotice(null);
                setOriginalContentMap(prev => {
                    if (!selectedFile) {
                        return new Map();
                    }

                    const next = new Map(prev);
                    next.clear();
                    next.set(selectedFile, editContent);
                    return next;
                });
                setFileStatusMap(new Map());
                await loadChanges();
            } else {
                const err = await res.text();
                toast.error(`Failed to keep all changes: ${err}`);
            }
        } catch {
            toast.error("Network error");
        }
    };

    const handleUndo = async (path: string) => {
        if (!confirm(`确定要撤销 ${path} 的更改吗？`)) return;
        
        try {
            await syncBackendCwd();
            const res = await fetch(`${API_BASE}/v1/files/undo`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ path })
            });

            if (!res.ok) {
                const errText = await res.text();
                toast.error(`撤销失败：${errText}`);
                return;
            }

            toast.success(`已撤销 ${path}`);
            setExternalChangeNotice(null);
            
            // 清除该文件的前端跟踪状态（后端历史已删除）
            setOriginalContentMap(prev => {
                const next = new Map(prev);
                next.delete(path);
                originalContentMapRef.current = next;
                return next;
            });
            setFileStatusMap(prev => {
                const next = new Map(prev);
                next.delete(path);
                return next;
            });
            
            // 如果当前选中的文件被撤销，重新加载或清空
            if (selectedFile === path) {
                fileContentCache.delete(path);
                try {
                    await loadFile(path, { force: true });
                } catch {
                    // 文件可能已被删除（AI创建的文件撤销后会删除）
                    setSelectedFile(null);
                    setEditContent('');
                    setDiffContent(null);
                    setFileError(null);
                }
            }
            
            // 统一从后端刷新所有状态
            await loadChanges();
            
            // 刷新文件树
            fileTreeCache.clear();
            setRefreshTrigger(prev => prev + 1);
            fetchRoot(currentPath);
        } catch (err) {
            toast.error(`网络错误：${err}`);
        }
    };

    const handleUndoAll = async () => {
        if (!confirm("确定要撤销所有更改吗？此操作不可恢复。")) return;
        
        try {
            await syncBackendCwd();
            const res = await fetch(`${API_BASE}/v1/files/undo-all`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ path: currentPath })
            });

            if (res.ok) {
                toast.success("已撤销所有更改");
                setExternalChangeNotice(null);
                
                // 清除所有前端跟踪状态（后端历史已全部删除）
                setOriginalContentMap(new Map());
                originalContentMapRef.current = new Map();
                setFileStatusMap(new Map());
                
                // 统一从后端刷新所有状态
                await loadChanges();
                
                // 刷新当前打开文件
                if (selectedFile) {
                    fileContentCache.delete(selectedFile);
                    try {
                        await loadFile(selectedFile, { force: true });
                    } catch {
                        setSelectedFile(null);
                        setEditContent('');
                        setDiffContent(null);
                        setFileError(null);
                    }
                }
                
                // 刷新文件树
                fileTreeCache.clear();
                setRefreshTrigger(prev => prev + 1);
            } else {
                const err = await res.text();
                toast.error(`撤销所有更改失败：${err}`);
            }
        } catch {
            toast.error("网络错误");
        }
    };

    // Expose refresh functionality
    const handleRefresh = useCallback(() => {
        setRefreshTrigger(prev => prev + 1);
        fetchRoot(currentPath);
        if (selectedFile) loadDiff(selectedFile);
        toast.info("Refreshed file tree");
    }, [currentPath, selectedFile, loadDiff]);

    const handleSelectDirectory = async () => {
        const api = getElectronAPI();
        if (api && api.selectDirectory) {
            try {
                const path = await api.selectDirectory();
                if (path) {
                    setSelectedFile(null);
                    setEditContent('');
                    setDiffContent(null);
                    setFileLang('text');
                    setExternalChangeNotice(null);
                    setEditorViewMode('editor');
                    setOriginalContentMap(new Map());
                    setFileStatusMap(new Map());

                    try {
                        if (onWorkingDirectoryChange) {
                            await Promise.resolve(onWorkingDirectoryChange(path));
                        } else {
                            const res = await fetch(`${API_BASE}/v1/system/cwd`, {
                                method: 'POST',
                                headers: { 'Content-Type': 'application/json' },
                                body: JSON.stringify({ path })
                            });
                            if (!res.ok) {
                                throw new Error('Failed to notify backend of CWD change');
                            }
                        }

                        await fetchRoot(path);
                        await loadChanges();
                    } catch (err) {
                        console.error("Failed to notify backend of CWD change", err);
                        toast.error("Failed to switch working directory");
                        return;
                    }

                    toast.success(`Switched to ${path}`);
                }
            } catch (err) {
                console.error("Failed to select directory:", err);
                toast.error("Failed to select directory");
            }
        } else {
            toast.error("Directory selection only available in desktop app");
        }
    };

    const handleSave = async () => {
        if (!selectedFile) return;
        try {
            const res = await fetch(`${API_BASE}/v1/files/save`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ 
                    path: selectedFile,
                    content: editContent
                })
            });
            
            if (res.ok) {
                toast.success(`Saved ${selectedFile}`);
                setExternalChangeNotice(null);
                
                // Update file content cache
                fileContentCache.set(selectedFile, { content: editContent, timestamp: Date.now() });

                // 更新快照为当前保存的内容，清除修改状态
                setOriginalContentMap(prev => {
                    const next = new Map(prev);
                    // 保存后，磁盘内容就是新的基线
                    next.set(selectedFile, editContent);
                    return next;
                });
                
                // 清除该文件的状态标记
                setFileStatusMap(prev => {
                    const next = new Map(prev);
                    next.delete(selectedFile);
                    return next;
                });
                
                updateFileStatus(selectedFile, editContent);
                loadDiff(selectedFile);
                
                // 刷新文件树以显示新文件/状态标记
                fileTreeCache.clear();
                setRefreshTrigger(prev => prev + 1);
                fetchRoot(currentPath);
            } else {
                toast.error("Failed to save");
            }
        } catch {
            toast.error("Network error");
        }
    };

    const handleReloadFromDisk = useCallback(async () => {
        if (!selectedFile) {
            return;
        }

        setExternalChangeNotice(null);
        setEditorViewMode('editor');
        fileContentCache.delete(selectedFile);
        await loadFile(selectedFile, { force: true, replaceSnapshot: true });
    }, [selectedFile, loadFile]);

    const handleKeepLocalEdits = useCallback(() => {
        setExternalChangeNotice(null);
    }, []);

    const selectedOriginalContent = selectedFile ? originalContentMap.get(selectedFile) : undefined;
    const diffRows = React.useMemo(() => {
        if (!selectedFile || selectedOriginalContent === undefined) {
            return [];
        }
        // 只有当编辑内容与快照不同时才计算 diff
        if (normalizeLineEndings(selectedOriginalContent) === normalizeLineEndings(editContent)) {
            return [];
        }
        return buildDiffRows(selectedOriginalContent, editContent);
    }, [selectedFile, selectedOriginalContent, editContent]);
    const hasVisibleDiff = diffRows.length > 0;
    const isDiffView = editorViewMode === 'diff' && hasVisibleDiff;

    const getDiffCellClassName = (kind: DiffCellKind) => {
        if (kind === 'added') {
            return 'bg-green-500/10 text-green-100';
        }
        if (kind === 'removed') {
            return 'bg-red-500/10 text-red-100';
        }
        if (kind === 'empty') {
            return 'bg-[#161616] text-zinc-700';
        }
        return 'bg-transparent text-zinc-300';
    };

    if (!isOpen) return null;

    const dragRegionStyle: AppRegionStyle = { WebkitAppRegion: 'drag' };
    const noDragRegionStyle: AppRegionStyle = { WebkitAppRegion: 'no-drag' };

    return (
        <div className="w-full h-full bg-[#1e1e1e]/95 backdrop-blur-md flex flex-col animate-in fade-in duration-300 border-l border-white/10">
            {/* Header */}
            <div className="h-12 border-b border-white/10 flex items-center px-4 justify-between bg-[#1e1e1e]" style={dragRegionStyle}>
                <div className="flex items-center gap-4">
                    <span className="text-xs font-bold text-zinc-500 uppercase tracking-wider">Explorer</span>
                    {/* Directory Selector */}
                    <div className="relative" style={noDragRegionStyle}>
                        <div 
                            onClick={handleSelectDirectory}
                            className="flex items-center gap-1 text-sm text-zinc-400 bg-white/5 px-2 py-1 rounded cursor-pointer hover:bg-white/10 transition-colors"
                            title="Click to change directory"
                        >
                            <FolderOpen className="w-4 h-4" />
                            <span className="max-w-[200px] truncate" title={currentPath}>{currentPath}</span>
                            <ChevronDown className="w-3 h-3" />
                        </div>
                    </div>
                    
                    {/* Refresh Button */}
                    <button
                        onClick={handleRefresh}
                        className="p-1.5 hover:bg-white/10 rounded text-zinc-400 hover:text-white transition-colors"
                        title="Refresh file list"
                        style={noDragRegionStyle}
                    >
                        <RotateCcw className="w-3.5 h-3.5" />
                    </button>
                </div>
                
                {/* Right Actions */}
                <div className="flex items-center gap-2" style={noDragRegionStyle}>
                     {/* Keep All / Undo All removed as per user request */}

                     <button 
                        onClick={handleShowChanges}
                        disabled={filteredChangedFiles.length === 0}
                        className={cn(
                            "flex items-center gap-2 px-3 py-1.5 rounded-md text-sm border transition-colors",
                            filteredChangedFiles.length > 0
                                ? "bg-blue-600/20 text-blue-400 hover:bg-blue-600/30 border-blue-500/20"
                                : "bg-zinc-800/50 text-zinc-600 border-zinc-700 cursor-not-allowed"
                        )}
                    >
                        <List className="w-4 h-4" />
                        查看全部变更
                    </button>
                    <button 
                        onClick={onClose}
                        className="p-2 hover:bg-zinc-800 rounded-md transition-colors"
                    >
                        <X className="w-5 h-5" />
                    </button>
                </div>
            </div>

            {/* Main Content */}
            <div className="flex-1 flex overflow-hidden">
                {/* File Tree Sidebar */}
                <div 
                    style={{ width: sidebarWidth }} 
                    className="border-r border-white/10 bg-[#1e1e1e]/50 flex-shrink-0 flex flex-col relative"
                    ref={sidebarRef}
                >
                    {/* File List */}
                    <div className="flex-1 overflow-y-auto">
                        {rootFiles.map(node => (
                            <FileTreeItem 
                                key={node.path} 
                                node={node} 
                                depth={0} 
                                onSelect={(path) => {
                                    // 规范化路径，确保前后一致
                                    const normalizedPath = normalizePath(path);
                                    if (normalizedPath === selectedFile) return;
                                    
                                    // 清除该文件的缓存，确保加载最新内容
                                    fileContentCache.delete(normalizedPath);
                                    
                                    setEditContent(''); // Clear content to prevent flash of old content
                                    setFileLang(getLanguageFromFilename(normalizedPath)); // Set language optimistically
                                    setIsContentLoading(true); // Start loading state
                                    setExternalChangeNotice(null);
                                    setEditorViewMode('editor');
                                    setSelectedFile(normalizedPath);
                                }}
                                selectedPath={selectedFile || undefined}
                                refreshTrigger={refreshTrigger}
                                onContextMenu={handleContextMenu}
                                fileStatusMap={normalizedFileStatusMap}
                                directoryStatusMap={directoryStatusMap}
                            />
                        ))}
                    </div>

                    {/* Context Menu */}
                    {contextMenu && (
                        <div 
                            className="absolute bg-[#252526] border border-[#454545] shadow-xl rounded py-1 z-[9999] min-w-[160px]"
                            style={{ 
                                top: contextMenu.y,
                                left: contextMenu.x
                            }}
                            onClick={(e) => e.stopPropagation()} // Prevent closing when clicking inside
                        >
                            <button 
                                className="w-full text-left px-3 py-1.5 text-xs text-zinc-300 hover:bg-[#094771] hover:text-white flex items-center gap-2"
                                onClick={() => {
                                    // If dir, create inside. If file, create in parent.
                                    let targetDir = contextMenu.path;
                                    if (contextMenu.type === 'file') {
                                        targetDir = targetDir.includes('/') ? targetDir.substring(0, targetDir.lastIndexOf('/')) : '';
                                    }
                                    setInputName('');
                                    setModal({ type: 'new-file', path: targetDir || '.' }); // Use '.' for root if empty
                                    setContextMenu(null);
                                }}
                            >
                                <FilePlus className="w-3.5 h-3.5" />
                                New File
                            </button>
                            <button 
                                className="w-full text-left px-3 py-1.5 text-xs text-zinc-300 hover:bg-[#094771] hover:text-white flex items-center gap-2"
                                onClick={() => {
                                    let targetDir = contextMenu.path;
                                    if (contextMenu.type === 'file') {
                                        targetDir = targetDir.includes('/') ? targetDir.substring(0, targetDir.lastIndexOf('/')) : '';
                                    }
                                    setInputName('');
                                    setModal({ type: 'new-folder', path: targetDir || '.' });
                                    setContextMenu(null);
                                }}
                            >
                                <FolderPlus className="w-3.5 h-3.5" />
                                New Folder
                            </button>
                            <div className="h-px bg-[#454545] my-1" />
                            <button 
                                className="w-full text-left px-3 py-1.5 text-xs text-zinc-300 hover:bg-[#094771] hover:text-white flex items-center gap-2"
                                onClick={() => {
                                    const name = contextMenu.path.split('/').pop() || '';
                                    setInputName(name);
                                    setModal({ type: 'rename', path: contextMenu.path, name });
                                    setContextMenu(null);
                                }}
                            >
                                <Edit className="w-3.5 h-3.5" />
                                Rename
                            </button>
                            <button 
                                className="w-full text-left px-3 py-1.5 text-xs text-red-400 hover:bg-[#094771] hover:text-white flex items-center gap-2"
                                onClick={() => {
                                    setModal({ type: 'delete', path: contextMenu.path });
                                    setContextMenu(null);
                                }}
                            >
                                <Trash2 className="w-3.5 h-3.5" />
                                Delete
                            </button>
                        </div>
                    )}

                    {/* Operation Modal */}
                    {modal && (
                        <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/50 backdrop-blur-sm p-4">
                            <div className="bg-[#252526] border border-[#454545] rounded-lg shadow-2xl w-full max-w-sm p-4 animate-in zoom-in-95 duration-200">
                                <h3 className="text-sm font-medium text-zinc-200 mb-4">
                                    {modal.type === 'new-file' && 'New File'}
                                    {modal.type === 'new-folder' && 'New Folder'}
                                    {modal.type === 'rename' && 'Rename'}
                                    {modal.type === 'delete' && 'Confirm Delete'}
                                </h3>
                                
                                {modal.type === 'delete' ? (
                                    <p className="text-sm text-zinc-400 mb-4">
                                        Are you sure you want to delete <span className="text-white font-mono">{modal.path}</span>?
                                        <br />This action cannot be undone.
                                    </p>
                                ) : (
                                    <div className="mb-4">
                                        <input
                                            type="text"
                                            value={inputName}
                                            onChange={(e) => setInputName(e.target.value)}
                                            className="w-full bg-[#3c3c3c] border border-[#3c3c3c] focus:border-[#007fd4] rounded px-2 py-1.5 text-sm text-white outline-none placeholder-zinc-500"
                                            placeholder={modal.type === 'new-folder' ? 'Folder Name' : 'File Name'}
                                            autoFocus
                                            onKeyDown={(e) => {
                                                if (e.key === 'Enter') {
                                                    if (modal.type === 'new-file') handleCreateFile();
                                                    else if (modal.type === 'new-folder') handleCreateFolder();
                                                    else if (modal.type === 'rename') handleRename();
                                                } else if (e.key === 'Escape') {
                                                    setModal(null);
                                                }
                                            }}
                                        />
                                        <p className="text-xs text-zinc-500 mt-2">
                                            Creating in: <span className="font-mono text-zinc-400">{modal.type === 'rename' ? 'Current location' : (modal.path === '.' ? 'Root' : modal.path)}</span>
                                        </p>
                                    </div>
                                )}
                                
                                <div className="flex justify-end gap-2">
                                    <button 
                                        onClick={() => setModal(null)}
                                        className="px-3 py-1.5 bg-transparent hover:bg-white/5 text-zinc-300 rounded text-sm transition-colors"
                                    >
                                        Cancel
                                    </button>
                                    <button 
                                        onClick={() => {
                                            if (modal.type === 'new-file') handleCreateFile();
                                            else if (modal.type === 'new-folder') handleCreateFolder();
                                            else if (modal.type === 'rename') handleRename();
                                            else if (modal.type === 'delete') handleDelete();
                                        }}
                                        className={cn(
                                            "px-3 py-1.5 text-white rounded text-sm transition-colors",
                                            modal.type === 'delete' 
                                                ? "bg-red-600 hover:bg-red-700" 
                                                : "bg-[#007fd4] hover:bg-[#026ec1]"
                                        )}
                                    >
                                        {modal.type === 'delete' ? 'Delete' : (modal.type === 'rename' ? 'Rename' : 'Create')}
                                    </button>
                                </div>
                            </div>
                        </div>
                    )}

                    {/* Active Operations Status Bar */}
                    {activeOperations && activeOperations.size > 0 && (
                        <div className="p-3 border-t border-white/10 bg-[#1e1e1e]">
                            <div className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider mb-2 flex items-center gap-2">
                                <Activity className="w-3 h-3" />
                                Active Operations
                            </div>
                            <div className="space-y-2">
                                {Array.from(activeOperations.entries()).map(([id, op]) => (
                                    <div key={id} className="flex flex-col gap-1">
                                        <div className="flex items-center gap-2 text-xs text-blue-400">
                                            <Loader2 className="w-3 h-3 animate-spin flex-shrink-0" />
                                            <span className="truncate font-medium">{op.activity}</span>
                                        </div>
                                        {op.file && (
                                            <div className="pl-5 truncate text-zinc-500 text-[10px] font-mono">
                                                {op.file}
                                            </div>
                                        )}
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}
                </div>
                
                {/* Resizer */}
                <div
                    className="w-1.5 hover:bg-blue-500/50 active:bg-blue-500 cursor-col-resize transition-colors z-20 flex-shrink-0"
                    onMouseDown={startResizing}
                />

                {/* Editor Area */}
                <div className="flex-1 flex flex-col bg-[#1e1e1e]/80 backdrop-blur-sm overflow-hidden">
                    {selectedFile ? (
                        <>
                            <div className="h-10 border-b border-white/10 flex items-center justify-between px-4 bg-black/20">
                                <span className="text-sm font-mono text-zinc-400" title={selectedFile}>{selectedFile}</span>
                                <div className="flex items-center gap-2">
                                    {!isContentLoading && hasVisibleDiff && (
                                        <button
                                            onClick={() => setEditorViewMode(prev => prev === 'diff' ? 'editor' : 'diff')}
                                            className={cn(
                                                "px-2 py-1 rounded text-xs transition-colors border",
                                                isDiffView
                                                    ? "bg-zinc-700 text-zinc-100 border-zinc-600"
                                                    : "bg-zinc-800/80 text-zinc-300 border-zinc-700 hover:bg-zinc-700"
                                            )}
                                            title={isDiffView ? '切换到编辑视图' : '切换到差异视图'}
                                        >
                                            {isDiffView ? '编辑' : '差异'}
                                        </button>
                                    )}
                                    <button 
                                        onClick={handleSave}
                                        className="px-2 py-1 bg-blue-600 hover:bg-blue-500 rounded text-xs text-white transition-colors flex items-center gap-1"
                                        title="Save changes (Ctrl+S)"
                                    >
                                        <Save className="w-3 h-3" />
                                        Save
                                    </button>

                                    {!isContentLoading && hasVisibleDiff && (
                                        <div className="flex items-center gap-2">
                                            <span className="text-xs text-amber-500 flex items-center gap-1">
                                                <AlertCircle className="w-3 h-3" />
                                                已变更
                                            </span>
                                            <div className="h-4 w-px bg-white/10 mx-2" />
                                            <button 
                                                onClick={() => handleKeep(selectedFile)}
                                                className="px-2 py-1 bg-zinc-700 hover:bg-zinc-600 rounded text-xs text-zinc-300 transition-colors"
                                                title="Keep changes (Commit)"
                                            >
                                                保留
                                            </button>
                                            <button 
                                                onClick={() => handleUndo(selectedFile)}
                                                className="px-2 py-1 bg-red-900/50 hover:bg-red-900/70 text-red-400 border border-red-900 rounded text-xs transition-colors flex items-center gap-1"
                                            >
                                                <RotateCcw className="w-3 h-3" />
                                                撤销
                                            </button>
                                        </div>
                                    )}
                                </div>
                            </div>
                            <div className="flex-1 overflow-hidden relative bg-[#1e1e1e]">
                                {isContentLoading && (
                                    <div className="absolute inset-0 z-50 bg-[#1e1e1e]/50 backdrop-blur-sm flex items-center justify-center text-zinc-500 gap-2">
                                        <div className="w-4 h-4 border-2 border-zinc-500 border-t-transparent rounded-full animate-spin" />
                                        <span>Loading...</span>
                                    </div>
                                )}
                                {externalChangeNotice && (
                                    <div className="absolute top-2 left-2 right-2 z-40 rounded border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-100">
                                        <div className="flex items-center gap-2">
                                            <AlertCircle className="w-3.5 h-3.5 flex-shrink-0 text-amber-400" />
                                            <span className="min-w-0 flex-1">
                                                {externalChangeNotice.type === 'deleted'
                                                    ? '检测到外部已删除当前文件，你的本地编辑尚未丢失，可选择保留或关闭文件。'
                                                    : '检测到外部修改了当前文件。为避免覆盖你的未保存编辑，已暂停自动刷新。'}
                                            </span>
                                            {externalChangeNotice.type === 'modified' ? (
                                                <button
                                                    onClick={handleReloadFromDisk}
                                                    className="rounded bg-amber-500/20 px-2 py-1 text-[11px] text-amber-100 hover:bg-amber-500/30"
                                                >
                                                    加载外部版本
                                                </button>
                                            ) : (
                                                <button
                                                    onClick={() => {
                                                        setExternalChangeNotice(null);
                                                        setSelectedFile(null);
                                                        setEditContent('');
                                                    }}
                                                    className="rounded bg-zinc-800/80 px-2 py-1 text-[11px] text-zinc-100 hover:bg-zinc-700"
                                                >
                                                    关闭文件
                                                </button>
                                            )}
                                            <button
                                                onClick={handleKeepLocalEdits}
                                                className="rounded bg-zinc-800/80 px-2 py-1 text-[11px] text-zinc-100 hover:bg-zinc-700"
                                            >
                                                保留当前编辑
                                            </button>
                                        </div>
                                    </div>
                                )}
                                {isDiffView ? (
                                    <div className="absolute inset-0 flex flex-col">
                                        <div className="bg-zinc-800 text-xs text-zinc-400 sticky top-0 z-10 border-b border-zinc-700 font-mono flex-shrink-0">
                                            <div className="grid grid-cols-2">
                                                <div className="px-4 py-1 border-r border-zinc-700">原内容</div>
                                                <div className="px-4 py-1">当前内容</div>
                                            </div>
                                        </div>
                                        <div className="font-mono text-[13px] font-light overflow-auto flex-1">
                                            {diffRows.map((row, index) => (
                                                <div key={`${row.leftLineNumber ?? 'n'}-${row.rightLineNumber ?? 'n'}-${index}`} className="grid grid-cols-2 border-b border-white/5">
                                                    <div className={cn("min-w-0 border-r border-white/5", getDiffCellClassName(row.leftKind))}>
                                                        <div className="flex min-w-0">
                                                            <div className="w-14 shrink-0 border-r border-white/5 px-2 py-1 text-right text-zinc-500 select-none">
                                                                {row.leftLineNumber ?? ''}
                                                            </div>
                                                            <pre className="m-0 flex-1 whitespace-pre-wrap break-all px-3 py-1">{row.leftContent || ' '}</pre>
                                                        </div>
                                                    </div>
                                                    <div className={cn("min-w-0", getDiffCellClassName(row.rightKind))}>
                                                        <div className="flex min-w-0">
                                                            <div className="w-14 shrink-0 border-r border-white/5 px-2 py-1 text-right text-zinc-500 select-none">
                                                                {row.rightLineNumber ?? ''}
                                                            </div>
                                                            <pre className="m-0 flex-1 whitespace-pre-wrap break-all px-3 py-1">{row.rightContent || ' '}</pre>
                                                        </div>
                                                    </div>
                                                </div>
                                            ))}
                                        </div>
                                    </div>
                                ) : (
                                    <>
                                        {fileError && (
                                            <div className={cn(
                                                "absolute left-2 right-2 z-40 bg-red-900/20 border border-red-500/30 rounded p-2 text-xs text-red-300 flex items-center gap-2",
                                                externalChangeNotice ? "top-14" : "top-2"
                                            )}>
                                                <AlertCircle className="w-3 h-3 flex-shrink-0" />
                                                <span>{fileError}</span>
                                                <button 
                                                    onClick={() => setFileError(null)}
                                                    className="ml-auto text-red-400 hover:text-red-300"
                                                >
                                                    <X className="w-3 h-3" />
                                                </button>
                                            </div>
                                        )}
                                        <CodeEditor
                                            key={selectedFile || 'editor'} // Keep key to force remount if needed, but path helps too
                                            path={selectedFile || undefined}
                                            value={editContent}
                                            onChange={setEditContent}
                                            onSave={handleSave}
                                            language={fileLang}
                                            readOnly={isContentLoading} // Disable editing while loading
                                        />
                                    </>
                                )}
                            </div>
                        </>
                    ) : (
                        <div className="flex-1 flex items-center justify-center text-zinc-600 flex-col gap-4">
                            <Terminal className="w-16 h-16 opacity-20" />
                            <p>Select a file to view content</p>
                        </div>
                    )}
                </div>
            </div>

            {/* Changes Modal */}
            {showChangesModal && (
                <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/50 backdrop-blur-sm p-4">
                    <div className="bg-[#1e1e1e] border border-white/10 rounded-lg shadow-2xl w-full max-w-4xl h-[80vh] flex flex-col overflow-hidden">
                        <div className="flex items-center justify-between px-4 py-3 border-b border-white/10 bg-zinc-900/50">
                            <h3 className="text-sm font-medium text-zinc-200 flex items-center gap-2">
                                <List className="w-4 h-4 text-blue-400" />
                                All Changes ({filteredChangedFiles.length})
                            </h3>
                            <button 
                                onClick={() => setShowChangesModal(false)}
                                className="text-zinc-500 hover:text-zinc-300 transition-colors"
                            >
                                <X className="w-5 h-5" />
                            </button>
                        </div>
                        
                        <div className="flex-1 overflow-auto p-4 space-y-2">
                            {filteredChangedFiles.length === 0 ? (
                                <div className="text-center text-zinc-500 py-10">
                                    No changes detected in this session.
                                </div>
                            ) : (
                                filteredChangedFiles.map(file => (
                                    <div key={file.path} className="flex items-center justify-between p-3 bg-zinc-800/30 border border-white/5 rounded hover:bg-zinc-800/50 transition-colors group">
                                        <div className="flex items-center gap-3 overflow-hidden">
                                            <span className={cn(
                                                "px-1.5 py-0.5 rounded text-[10px] font-bold uppercase w-6 text-center shrink-0",
                                                file.status === 'M' ? "bg-blue-500/20 text-blue-400" :
                                                file.status === 'A' ? "bg-green-500/20 text-green-400" :
                                                "bg-red-500/20 text-red-400"
                                            )}>
                                                {file.status}
                                            </span>
                                            <span className="text-sm text-zinc-300 truncate" title={file.path}>
                                                {file.path}
                                            </span>
                                        </div>
                                        <div className="flex items-center gap-2 opacity-0 group-hover:opacity-100 transition-opacity">
                                            <button 
                                                onClick={async () => {
                                                    await syncBackendCwd();
                                                    setSelectedFile(file.path);
                                                    setShowChangesModal(false);
                                                }}
                                                className="px-2 py-1 bg-zinc-700 hover:bg-zinc-600 rounded text-xs text-zinc-300"
                                            >
                                                View Diff
                                            </button>
                                            <button 
                                                onClick={() => handleKeep(file.path)}
                                                className="px-2 py-1 bg-blue-600/20 hover:bg-blue-600/30 text-blue-400 border border-blue-500/20 rounded text-xs"
                                            >
                                                Keep
                                            </button>
                                            <button 
                                                onClick={() => handleUndo(file.path)}
                                                className="px-2 py-1 bg-red-900/20 hover:bg-red-900/30 text-red-400 border border-red-500/20 rounded text-xs"
                                            >
                                                Undo
                                            </button>
                                        </div>
                                    </div>
                                ))
                            )}
                        </div>
                        
                        <div className="p-4 border-t border-white/10 bg-zinc-900/50 flex justify-end gap-3">
                            <button 
                                onClick={() => setShowChangesModal(false)}
                                className="px-4 py-2 bg-transparent hover:bg-white/5 text-zinc-400 rounded text-sm transition-colors"
                            >
                                Close
                            </button>
                            <button 
                                onClick={() => {
                                    handleKeepAll();
                                    setShowChangesModal(false);
                                }}
                                disabled={filteredChangedFiles.length === 0}
                                className="px-4 py-2 bg-blue-600 hover:bg-blue-500 text-white rounded text-sm shadow-lg shadow-blue-900/20 disabled:opacity-50 disabled:cursor-not-allowed transition-all"
                            >
                                Keep All Changes
                            </button>
                        </div>
                    </div>
                </div>
            )}
        </div>
    );
};

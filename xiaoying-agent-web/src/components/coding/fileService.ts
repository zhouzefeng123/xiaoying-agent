/**
 * 文件服务层 - 统一管理文件读取、缓存和快照
 */

const API_BASE = '';
const CACHE_TTL = 5000; // 5秒缓存

interface CacheEntry {
    content: string;
    timestamp: number;
}

interface ElectronAPI {
    readFile?: (path: string) => Promise<{ content?: string; error?: string }>;
    listFiles?: (path: string) => Promise<any[]>;
}

// 全局缓存
const fileContentCache = new Map<string, CacheEntry>();

/**
 * 规范化路径
 */
export const normalizePath = (path: string): string => {
    return path.replace(/\\/g, '/').replace(/^\.\//, '').replace(/\/+/g, '/');
};

/**
 * 规范化换行符
 */
export const normalizeLineEndings = (content: string): string => {
    return content.replace(/\r\n/g, '\n');
};

/**
 * 安全获取 Electron API
 */
const getElectronAPI = (): ElectronAPI | undefined => {
    const apiWindow = window as typeof window & { electronAPI?: ElectronAPI };
    return apiWindow.electronAPI;
};

/**
 * 从 Electron IPC 读取文件
 */
const readFileFromIPC = async (path: string): Promise<string> => {
    const api = getElectronAPI();
    if (!api?.readFile) {
        throw new Error('IPC API not available');
    }
    
    const result = await api.readFile(path);
    if (result.error) {
        throw new Error(result.error);
    }
    return result.content || '';
};

/**
 * 从 HTTP API 读取文件
 */
const readFileFromHTTP = async (path: string): Promise<string> => {
    const url = `${API_BASE}/v1/files/read?path=${encodeURIComponent(path)}&t=${Date.now()}`;
    const response = await fetch(url);
    
    if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }
    
    const data = await response.json();
    return data.content || '';
};

/**
 * 读取文件内容(带缓存)
 * @param path 文件路径
 * @param options 选项
 * @returns 文件内容
 */
export const readFile = async (
    path: string,
    options: { force?: boolean } = {}
): Promise<string> => {
    const { force = false } = options;
    const normalizedPath = normalizePath(path);
    
    // 检查缓存
    if (!force) {
        const cached = fileContentCache.get(normalizedPath);
        if (cached && (Date.now() - cached.timestamp) < CACHE_TTL) {
            return cached.content;
        }
    }
    
    // 读取文件内容,优先使用 IPC,失败则降级到 HTTP
    let content: string;
    try {
        content = await readFileFromIPC(normalizedPath);
    } catch (ipcError) {
        console.warn('IPC 读取失败,降级到 HTTP:', ipcError);
        content = await readFileFromHTTP(normalizedPath);
    }
    
    // 更新缓存
    fileContentCache.set(normalizedPath, {
        content,
        timestamp: Date.now()
    });
    
    return content;
};

/**
 * 清除文件缓存
 */
export const clearFileCache = (path?: string): void => {
    if (path) {
        fileContentCache.delete(normalizePath(path));
    } else {
        fileContentCache.clear();
    }
};

/**
 * 获取语言类型
 */
export const getLanguageFromFilename = (filename: string): string => {
    if (!filename) return 'text';
    const base = filename.split(/[/\\]/).pop()?.toLowerCase() || '';
    const ext = base.split('.').pop() || '';
    
    const langMap: Record<string, string> = {
        js: 'javascript', jsx: 'javascript',
        ts: 'typescript', tsx: 'typescript',
        py: 'python',
        go: 'go',
        java: 'java',
        cpp: 'cpp', c: 'c',
        rs: 'rust',
        rb: 'ruby',
        php: 'php',
        cs: 'csharp',
        html: 'html',
        css: 'css',
        json: 'json',
        md: 'markdown',
        yaml: 'yaml', yml: 'yaml',
        xml: 'xml',
        sql: 'sql',
        sh: 'bash', bash: 'bash',
        vue: 'vue',
        svelte: 'svelte'
    };
    
    return langMap[ext] || 'text';
};

/**
 * 判断是否为二进制文件
 */
export const isBinaryFile = (path: string): boolean => {
    const ext = path.split('.').pop()?.toLowerCase() || '';
    const binaryExts = ['png', 'jpg', 'jpeg', 'gif', 'bmp', 'ico', 'svg', 'webp', 'exe', 'dll', 'so', 'dylib', 'zip', 'tar', 'gz', 'pdf'];
    return binaryExts.includes(ext);
};

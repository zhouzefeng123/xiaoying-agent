import { useCallback, useEffect, useRef, useState } from 'react';
import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';
import { Plus, TerminalSquare, X } from 'lucide-react';
import { cn } from '../../lib/utils';

type TerminalStatus = 'running' | 'completed' | 'failed';

interface TerminalTab {
    id: string;
    title: string;
    cwd?: string;
    shell?: string;
    status: TerminalStatus;
}

interface TerminalRuntime {
    terminal: Terminal;
    fitAddon: FitAddon;
    disposable?: { dispose: () => void };
}

interface TerminalPanelProps {
    isOpen: boolean;
    defaultCwd?: string | null;
    onClose: () => void;
}

const normalizeTerminalOutput = (data: string) => data
    .replace(/\r\n/g, '\n')
    .replace(/\r/g, '\n')
    .replace(/\n/g, '\r\n');

const terminalTheme = {
    background: '#ffffff',
    foreground: '#050505',
    cursor: '#050505',
    cursorAccent: '#ffffff',
    selectionBackground: '#b8d7ff',
    black: '#050505',
    red: '#b91c1c',
    green: '#047857',
    yellow: '#92400e',
    blue: '#1d4ed8',
    magenta: '#7e22ce',
    cyan: '#0e7490',
    white: '#d4d4d8',
    brightBlack: '#52525b',
    brightRed: '#dc2626',
    brightGreen: '#059669',
    brightYellow: '#b45309',
    brightBlue: '#2563eb',
    brightMagenta: '#9333ea',
    brightCyan: '#0891b2',
    brightWhite: '#18181b',
};

export function TerminalPanel({
    isOpen,
    defaultCwd,
    onClose,
}: TerminalPanelProps) {
    const [tabs, setTabs] = useState<TerminalTab[]>([]);
    const [activeId, setActiveId] = useState<string | null>(null);
    const activeIdRef = useRef<string | null>(null);
    const runtimesRef = useRef(new Map<string, TerminalRuntime>());
    const containersRef = useRef(new Map<string, HTMLDivElement>());
    const outputRef = useRef(new Map<string, string>());
    const creatingRef = useRef(false);

    useEffect(() => {
        activeIdRef.current = activeId;
    }, [activeId]);

    const writeToTerminal = useCallback((id: string, data: string) => {
        const previous = outputRef.current.get(id) || '';
        const next = previous.length > 120_000
            ? `${previous.slice(-80_000)}${data}`
            : `${previous}${data}`;
        outputRef.current.set(id, next);
        runtimesRef.current.get(id)?.terminal.write(data);
    }, []);

    const fitTerminal = useCallback((id: string) => {
        const runtime = runtimesRef.current.get(id);
        if (!runtime) return;
        try {
            runtime.fitAddon.fit();
            const { cols, rows } = runtime.terminal;
            void window.terminalAPI?.resize(id, cols, rows);
        } catch {
            // Fit can fail while the panel is transitioning.
        }
    }, []);

    const ensureRuntime = useCallback((tab: TerminalTab) => {
        const container = containersRef.current.get(tab.id);
        if (!container || runtimesRef.current.has(tab.id)) return;

        const terminal = new Terminal({
            cursorBlink: true,
            convertEol: true,
            fontFamily: 'Cascadia Mono, Consolas, "SFMono-Regular", monospace',
            fontSize: 13,
            lineHeight: 1.35,
            scrollback: 6000,
            theme: terminalTheme,
        });
        const fitAddon = new FitAddon();
        terminal.loadAddon(fitAddon);
        terminal.open(container);

        const runtime: TerminalRuntime = { terminal, fitAddon };
        runtime.disposable = terminal.onData((data) => {
            void window.terminalAPI?.write(tab.id, data);
        });

        runtimesRef.current.set(tab.id, runtime);
        const cachedOutput = outputRef.current.get(tab.id);
        if (cachedOutput) {
            terminal.write(cachedOutput);
        }

        window.setTimeout(() => fitTerminal(tab.id), 0);
    }, [fitTerminal]);

    useEffect(() => {
        tabs.forEach(ensureRuntime);
    }, [ensureRuntime, tabs]);

    useEffect(() => {
        if (!isOpen || !activeId) return;
        const timers = [
            window.setTimeout(() => fitTerminal(activeId), 80),
            window.setTimeout(() => fitTerminal(activeId), 240),
        ];
        return () => timers.forEach((timer) => window.clearTimeout(timer));
    }, [activeId, fitTerminal, isOpen]);

    useEffect(() => {
        if (!isOpen || !activeId || typeof ResizeObserver === 'undefined') return;
        const container = containersRef.current.get(activeId);
        if (!container) return;

        let frame = 0;
        const observer = new ResizeObserver(() => {
            window.cancelAnimationFrame(frame);
            frame = window.requestAnimationFrame(() => fitTerminal(activeId));
        });
        observer.observe(container);

        return () => {
            window.cancelAnimationFrame(frame);
            observer.disconnect();
        };
    }, [activeId, fitTerminal, isOpen]);

    useEffect(() => {
        const removeDataListener = window.terminalAPI?.onData((payload) => {
            writeToTerminal(payload.id, normalizeTerminalOutput(payload.data));
        });
        const removeExitListener = window.terminalAPI?.onExit((payload) => {
            setTabs((current) => current.map((tab) => (
                tab.id === payload.id
                    ? { ...tab, status: payload.code === 0 ? 'completed' : 'failed' }
                    : tab
            )));
            writeToTerminal(payload.id, `\r\n[进程已退出 ${payload.code ?? payload.signal ?? 'unknown'}]\r\n`);
        });

        return () => {
            removeDataListener?.();
            removeExitListener?.();
        };
    }, [writeToTerminal]);

    const createTerminal = useCallback(async (title?: string) => {
        if (creatingRef.current) return;
        creatingRef.current = true;
        try {
            if (!window.terminalAPI?.create) {
                const id = `terminal-unavailable-${Date.now()}`;
                outputRef.current.set(id, '当前桌面桥接不可用，无法创建 PTY 终端。\r\n');
                setTabs((current) => [...current, {
                    id,
                    title: '终端不可用',
                    status: 'failed',
                }]);
                setActiveId(id);
                return;
            }

            const created = await window.terminalAPI.create({
                cwd: defaultCwd || undefined,
                cols: 100,
                rows: 12,
            });

            setTabs((current) => [...current, {
                id: created.id,
                title: title || created.title || created.cwd,
                cwd: created.cwd,
                shell: created.shell,
                status: 'running',
            }]);
            setActiveId(created.id);
        } catch (error) {
            const id = `terminal-error-${Date.now()}`;
            outputRef.current.set(id, `创建 PTY 终端失败：${error instanceof Error ? error.message : String(error)}\r\n`);
            setTabs((current) => [...current, {
                id,
                title: '终端启动失败',
                status: 'failed',
            }]);
            setActiveId(id);
        } finally {
            creatingRef.current = false;
        }
    }, [defaultCwd]);

    useEffect(() => {
        if (!isOpen || tabs.length > 0) return;
        void createTerminal();
    }, [createTerminal, isOpen, tabs.length]);

    const closeTab = useCallback((id: string) => {
        void window.terminalAPI?.close(id);

        const runtime = runtimesRef.current.get(id);
        runtime?.disposable?.dispose();
        runtime?.terminal.dispose();
        runtimesRef.current.delete(id);
        containersRef.current.delete(id);
        outputRef.current.delete(id);

        setTabs((current) => {
            const next = current.filter((item) => item.id !== id);
            if (activeIdRef.current === id) {
                setActiveId(next[next.length - 1]?.id ?? null);
            }
            return next;
        });
    }, []);

    const activeTab = tabs.find((tab) => tab.id === activeId);

    return (
        <div className={cn(
            'shrink-0 overflow-hidden border-t border-zinc-200 bg-white transition-[height] duration-200 dark:border-zinc-800 dark:bg-zinc-950',
            isOpen ? 'h-[240px]' : 'h-0 border-t-0',
        )}>
            <div className="flex h-full flex-col">
                <div className="flex h-10 shrink-0 items-center justify-between border-b border-zinc-100 px-3 dark:border-zinc-800">
                    <div className="flex min-w-0 flex-1 items-center gap-2 overflow-x-auto">
                        {tabs.map((tab) => (
                            <button
                                key={tab.id}
                                onClick={() => setActiveId(tab.id)}
                                className={cn(
                                    'group flex h-7 max-w-[260px] items-center gap-2 rounded-lg px-2 text-sm transition-colors',
                                    activeId === tab.id
                                        ? 'bg-zinc-100 text-zinc-950 dark:bg-zinc-800 dark:text-zinc-50'
                                        : 'text-zinc-500 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800',
                                )}
                                title={tab.cwd || tab.title}
                            >
                                <TerminalSquare className="h-4 w-4 shrink-0" />
                                <span className="truncate">{tab.title}</span>
                                {tab.status === 'running' && <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-emerald-500" />}
                                {tab.status === 'failed' && <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-red-500" />}
                                <span
                                    role="button"
                                    tabIndex={0}
                                    onClick={(event) => {
                                        event.stopPropagation();
                                        closeTab(tab.id);
                                    }}
                                    onKeyDown={(event) => {
                                        if (event.key === 'Enter' || event.key === ' ') {
                                            event.preventDefault();
                                            event.stopPropagation();
                                            closeTab(tab.id);
                                        }
                                    }}
                                    className="rounded p-0.5 opacity-0 transition-opacity hover:bg-zinc-200 group-hover:opacity-100 dark:hover:bg-zinc-700"
                                    title="关闭终端"
                                >
                                    <X className="h-3.5 w-3.5" />
                                </span>
                            </button>
                        ))}
                        <button
                            onClick={() => void createTerminal()}
                            className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-zinc-500 transition-colors hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100"
                            title="新建终端"
                        >
                            <Plus className="h-4 w-4" />
                        </button>
                    </div>
                    <button
                        onClick={onClose}
                        className="ml-2 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-zinc-400 transition-colors hover:bg-zinc-100 hover:text-zinc-900 dark:hover:bg-zinc-800 dark:hover:text-zinc-100"
                        title="关闭命令行面板"
                    >
                        <X className="h-4 w-4" />
                    </button>
                </div>

                <div className="relative min-h-0 flex-1">
                    {tabs.map((tab) => (
                        <div
                            key={tab.id}
                            className={cn(
                                'absolute inset-0 p-3',
                                activeId === tab.id ? 'block' : 'hidden',
                            )}
                        >
                            <div
                                ref={(element) => {
                                    if (element) {
                                        containersRef.current.set(tab.id, element);
                                        ensureRuntime(tab);
                                    } else {
                                        containersRef.current.delete(tab.id);
                                    }
                                }}
                                className="h-full w-full overflow-hidden"
                            />
                        </div>
                    ))}
                    {!activeTab && (
                        <div className="flex h-full items-center justify-center text-sm text-zinc-400">
                            点击 + 新建 PTY 终端
                        </div>
                    )}
                </div>
            </div>
        </div>
    );
}

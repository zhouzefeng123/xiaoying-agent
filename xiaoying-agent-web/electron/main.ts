import { app, BrowserWindow, screen, ipcMain, dialog, session, protocol, net, Notification } from 'electron'
import electronUpdater from 'electron-updater'
import log from 'electron-log'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import fs from 'node:fs/promises'
import fsSync from 'node:fs'
import { spawn, execSync, ChildProcess } from 'node:child_process'
import nodeNet from 'node:net'
import type { Socket } from 'node:net'
import nodeHttp from 'node:http'
import { randomUUID } from 'node:crypto'
import type { IPty } from '@lydell/node-pty'
import {
  APP_SIDEBAR_WIDTH,
  COLLAPSED_WINDOW_HEIGHT,
  COLLAPSED_WINDOW_WIDTH,
  EXPANDED_WINDOW_HEIGHT,
  EXPANDED_WINDOW_WIDTH,
} from '../src/shared/window-size'

const { autoUpdater } = electronUpdater

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const PACKAGED_USER_DATA_DIRNAME = 'AuraMate'

// Keep the Windows shell identity stable so taskbar grouping and the icon
// shown for the running app use the packaged XiaoYing application identity.
if (process.platform === 'win32') {
  app.setAppUserModelId('com.auramate.app')
}

// Configure logging
autoUpdater.logger = log;
// @ts-ignore
autoUpdater.logger.transports.file.level = 'info';

let backendProcess: ChildProcess | null = null;
let managedBackendPid: number | null = null;
let lastKnownBackendPid: number | null = null;
let packagedFrontendServer: nodeHttp.Server | null = null;
let packagedFrontendURL: string | null = null;
let localBackendPort = 3000;
let isQuitting = false;

let backendRestartAttempts = 0;
const MAX_BACKEND_RESTART_ATTEMPTS = 5;
const BACKEND_RESTART_DELAY_MS = 2000;
let backendRestartTimer: ReturnType<typeof setTimeout> | null = null;

type BackendRuntimePaths = {
  backendPath: string;
  backendCwd: string;
  configFile?: string;
  workspaceDir?: string;
};

// Check if a port is in use
const isPortInUse = (port: number): Promise<boolean> => {
  return new Promise((resolve) => {
    const server = nodeNet.createServer();
    (server as any).once('error', (err: NodeJS.ErrnoException) => {
      if (err.code === 'EADDRINUSE') {
        resolve(true);
      } else {
        resolve(false);
      }
    });
    (server as any).once('listening', () => {
      server.close();
      resolve(false);
    });
    server.listen(port);
  });
};

type PortOwnerInfo = {
  pid: number | null;
  processName: string | null;
  executablePath: string | null;
};

const getPortOwnerInfo = (port: number): PortOwnerInfo => {
  if (process.platform !== 'win32') {
    return { pid: null, processName: null, executablePath: null };
  }

  try {
    const pidOutput = execSync(
      `powershell -NoProfile -NonInteractive -Command "(Get-NetTCPConnection -LocalPort ${port} -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty OwningProcess)"`,
      { stdio: ['ignore', 'pipe', 'ignore'] }
    )
      .toString()
      .trim();

    if (!pidOutput) {
      return { pid: null, processName: null, executablePath: null };
    }

    const pid = Number.parseInt(pidOutput, 10);
    if (!Number.isFinite(pid) || pid <= 0) {
      return { pid: null, processName: null, executablePath: null };
    }

    let processName: string | null = null;
    let executablePath: string | null = null;
    try {
      processName = execSync(
        `powershell -NoProfile -NonInteractive -Command "(Get-Process -Id ${pid} -ErrorAction SilentlyContinue).ProcessName"`,
        { stdio: ['ignore', 'pipe', 'ignore'] }
      )
        .toString()
        .trim()
        .toLowerCase();
    } catch {
      processName = null;
    }

    try {
      executablePath = execSync(
        `powershell -NoProfile -NonInteractive -Command "(Get-Process -Id ${pid} -ErrorAction SilentlyContinue).Path"`,
        { stdio: ['ignore', 'pipe', 'ignore'] }
      )
        .toString()
        .trim();
    } catch {
      executablePath = null;
    }

    return { pid, processName, executablePath };
  } catch {
    return { pid: null, processName: null, executablePath: null };
  }
};

const killProcessTree = (pid: number): boolean => {
  if (!Number.isFinite(pid) || pid <= 0) {
    return false;
  }

  try {
    execSync(`taskkill /pid ${pid} /T /F`, { stdio: ['ignore', 'pipe', 'pipe'] });
    return true;
  } catch (error) {
    console.error(`[Main] Failed to taskkill PID ${pid}:`, error);
    return false;
  }
};

const waitForPortToBeFreed = async (port: number, attempts = 10, delayMs = 300): Promise<boolean> => {
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    if (!(await isPortInUse(port))) {
      return true;
    }
    await new Promise((resolve) => setTimeout(resolve, delayMs));
  }
  return !(await isPortInUse(port));
};

const getContentType = (filePath: string): string => {
  switch (path.extname(filePath).toLowerCase()) {
    case '.html': return 'text/html; charset=utf-8';
    case '.js': return 'text/javascript; charset=utf-8';
    case '.mjs': return 'text/javascript; charset=utf-8';
    case '.css': return 'text/css; charset=utf-8';
    case '.json': return 'application/json; charset=utf-8';
    case '.svg': return 'image/svg+xml';
    case '.png': return 'image/png';
    case '.jpg':
    case '.jpeg': return 'image/jpeg';
    case '.ico': return 'image/x-icon';
    case '.woff': return 'font/woff';
    case '.woff2': return 'font/woff2';
    case '.ttf': return 'font/ttf';
    case '.wasm': return 'application/wasm';
    default: return 'application/octet-stream';
  }
};

const findAvailableLocalPort = async (startPort: number, attempts = 30): Promise<number> => {
  for (let offset = 0; offset < attempts; offset += 1) {
    const port = startPort + offset;
    if (!(await isPortInUse(port))) {
      return port;
    }
  }
  throw new Error(`No available localhost frontend port from ${startPort} to ${startPort + attempts - 1}`);
};

const waitForLocalTcpPort = async (port: number, attempts = 40, delayMs = 250): Promise<boolean> => {
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    const ok = await new Promise<boolean>((resolve) => {
      const socket = nodeNet.connect({ host: '127.0.0.1', port });
      socket.once('connect', () => {
        socket.destroy();
        resolve(true);
      });
      socket.once('error', () => {
        socket.destroy();
        resolve(false);
      });
      socket.setTimeout(1000, () => {
        socket.destroy();
        resolve(false);
      });
    });
    if (ok) return true;
    await new Promise(resolve => setTimeout(resolve, delayMs));
  }
  return false;
};

const sanitizeProxyHeaders = (headers: nodeHttp.IncomingHttpHeaders): nodeHttp.OutgoingHttpHeaders => {
  const blockedHeaders = new Set([
    'host',
    'connection',
    'keep-alive',
    'proxy-authenticate',
    'proxy-authorization',
    'proxy-connection',
    'te',
    'trailer',
    'transfer-encoding',
    'upgrade',
    'content-length',
    'accept-encoding',
  ]);
  const result: nodeHttp.OutgoingHttpHeaders = {};
  for (const [key, value] of Object.entries(headers)) {
    if (value == null || blockedHeaders.has(key.toLowerCase())) continue;
    result[key] = value;
  }
  return result;
};

const proxyToLocalBackend = (req: nodeHttp.IncomingMessage, res: nodeHttp.ServerResponse, relPath: string, search: string) => {
  const backendPort = localBackendPort;
  const proxyPath = `/${relPath}${search}`;
  log.info(`[Main] Local frontend API proxy: ${req.method} http://127.0.0.1:${backendPort}${proxyPath}`);

  const proxyReq = nodeHttp.request({
    host: '127.0.0.1',
    port: backendPort,
    path: proxyPath,
    method: req.method,
    headers: sanitizeProxyHeaders(req.headers),
  }, (proxyRes) => {
    const responseHeaders = sanitizeProxyHeaders(proxyRes.headers);
    res.writeHead(proxyRes.statusCode || 502, responseHeaders);
    proxyRes.pipe(res);
  });

  proxyReq.on('error', (error) => {
    console.error('[Main] Local frontend API proxy error:', error);
    log.error('[Main] Local frontend API proxy error:', error);
    if (!res.headersSent) {
      res.writeHead(502, { 'Content-Type': 'application/json; charset=utf-8' });
    }
    res.end(JSON.stringify({ error: 'API Proxy Failed', detail: error instanceof Error ? error.message : String(error) }));
  });

  req.pipe(proxyReq);
};

const proxyUpgradeToLocalBackend = (
  req: nodeHttp.IncomingMessage,
  socket: Socket,
  head: Buffer
) => {
  const requestURL = new URL(req.url || '/', 'http://127.0.0.1');
  let relPath = decodeURIComponent(requestURL.pathname);
  if (relPath.startsWith('/')) relPath = relPath.slice(1);
  if (!relPath.startsWith('v1/') && !relPath.startsWith('api/')) {
    socket.destroy();
    return;
  }

  const backendPort = localBackendPort;
  const proxyPath = `/${relPath}${requestURL.search}`;
  log.info(`[Main] Local frontend WS proxy: ${req.method} ws://127.0.0.1:${backendPort}${proxyPath}`);

  const proxyReq = nodeHttp.request({
    host: '127.0.0.1',
    port: backendPort,
    path: proxyPath,
    method: req.method,
    headers: {
      ...req.headers,
      host: `127.0.0.1:${backendPort}`,
      connection: 'Upgrade',
      upgrade: req.headers.upgrade || 'websocket',
    },
  });

  proxyReq.on('upgrade', (proxyRes, proxySocket, proxyHead) => {
    socket.write(`HTTP/${proxyRes.httpVersion} ${proxyRes.statusCode} ${proxyRes.statusMessage}\r\n`);
    for (let index = 0; index < proxyRes.rawHeaders.length; index += 2) {
      socket.write(`${proxyRes.rawHeaders[index]}: ${proxyRes.rawHeaders[index + 1]}\r\n`);
    }
    socket.write('\r\n');

    if (proxyHead.length > 0) {
      socket.write(proxyHead);
    }
    if (head.length > 0) {
      proxySocket.write(head);
    }

    proxySocket.pipe(socket);
    socket.pipe(proxySocket);
  });

  proxyReq.on('error', (error) => {
    console.error('[Main] Local frontend WS proxy error:', error);
    log.error('[Main] Local frontend WS proxy error:', error);
    socket.destroy();
  });

  proxyReq.end();
};

const isSafeChildPath = (parentPath: string, childPath: string): boolean => {
  const relativePath = path.relative(parentPath, childPath);
  return relativePath === '' || (!relativePath.startsWith('..') && !path.isAbsolute(relativePath));
};

const startPackagedFrontendServer = async (): Promise<string | null> => {
  if (!app.isPackaged) {
    return null;
  }
  if (packagedFrontendURL) {
    return packagedFrontendURL;
  }

  const distPath = process.env.DIST || path.join(__dirname, '../dist');
  const port = await findAvailableLocalPort(51730);

  packagedFrontendServer = nodeHttp.createServer(async (req, res) => {
    try {
      const requestURL = new URL(req.url || '/', `http://127.0.0.1:${port}`);
      let relPath = decodeURIComponent(requestURL.pathname);
      if (relPath.startsWith('/')) relPath = relPath.slice(1);

      if (relPath.startsWith('v1/') || relPath.startsWith('api/')) {
        proxyToLocalBackend(req, res, relPath, requestURL.search);
        return;
      }

      if (!relPath || relPath === '/') {
        relPath = 'index.html';
      }

      let filePath = path.resolve(distPath, relPath);
      const normalizedDistPath = path.resolve(distPath);
      if (!isSafeChildPath(normalizedDistPath, filePath)) {
        res.writeHead(403, { 'Content-Type': 'text/plain; charset=utf-8' });
        res.end('Forbidden');
        return;
      }

      try {
        const stat = await fs.stat(filePath);
        if (!stat.isFile()) {
          throw new Error('Not a file');
        }
      } catch {
        if (path.extname(relPath)) {
          res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' });
          res.end('Not Found');
          return;
        }
        filePath = path.join(distPath, 'index.html');
      }

      res.writeHead(200, {
        'Content-Type': getContentType(filePath),
        'Cache-Control': path.basename(filePath) === 'index.html' ? 'no-store' : 'public, max-age=31536000, immutable',
      });
      fsSync.createReadStream(filePath).pipe(res);
    } catch (error) {
      console.error('[Main] Local frontend server error:', error);
      res.writeHead(500, { 'Content-Type': 'text/plain; charset=utf-8' });
      res.end('Internal Server Error');
    }
  });
  packagedFrontendServer.on('upgrade', proxyUpgradeToLocalBackend);

  await new Promise<void>((resolve, reject) => {
    packagedFrontendServer?.once('error', reject);
    packagedFrontendServer?.listen(port, '127.0.0.1', () => resolve());
  });

  packagedFrontendURL = `http://127.0.0.1:${port}/`;
  console.log(`[Main] Packaged frontend server listening at ${packagedFrontendURL}`);
  log.info(`[Main] Packaged frontend server listening at ${packagedFrontendURL}`);
  return packagedFrontendURL;
};

const startBackend = async (): Promise<boolean> => {
  const preferredPort = 3000;
  let port = preferredPort;
  const runtimePaths: BackendRuntimePaths = app.isPackaged
    ? {
        backendPath: path.join(process.resourcesPath, 'auramate-server.exe'),
        backendCwd: process.resourcesPath,
        configFile: path.join(app.getPath('userData'), '.auramate.json'),
        workspaceDir: path.join(app.getPath('userData'), 'workspace'),
      }
    : {
        backendPath: path.join(__dirname, '../../xiaoying-agent-api/dist/auramate-server.exe'),
        backendCwd: path.join(__dirname, '../../xiaoying-agent-api'),
      };
  
  // 1. Check if backend port is already occupied
  const inUse = await isPortInUse(port);
  if (inUse) {
    const owner = getPortOwnerInfo(port);
    const processLabel = owner.processName ? `${owner.processName}.exe` : 'unknown process';

    if (owner.processName === 'auramate-server' && owner.pid) {
      if (backendRestartAttempts > 0 && owner.pid !== lastKnownBackendPid) {
        console.log(
          `[Main] Port ${port} is occupied by auramate-server PID ${owner.pid} (previously ${lastKnownBackendPid}). Backend likely self-restarted. Adopting.`
        );
        managedBackendPid = owner.pid;
        lastKnownBackendPid = owner.pid;
        backendRestartAttempts = 0;
        localBackendPort = port;
        notifyRendererBackendStatus('started');
        return true;
      }

      if (app.isPackaged) {
        console.log(
          `[Main] Port ${port} is occupied by stale packaged backend PID ${owner.pid}. Replacing it before spawn.`
        );
        if (!killProcessTree(owner.pid) || !(await waitForPortToBeFreed(port))) {
          console.log(
            `[Main] Failed to reclaim port ${port} from PID ${owner.pid} (${processLabel}) at ${owner.executablePath ?? 'unknown path'}.`
          );
          backendProcess = null;
          managedBackendPid = null;
          return false;
        }
      }
    } else {
      const fallbackPort = await findAvailableLocalPort(preferredPort + 1, 100);
      const message = `[Main] Port ${port} is already in use by PID ${owner.pid ?? 'unknown'} (${processLabel}) at ${owner.executablePath ?? 'unknown path'}. Starting packaged backend on fallback port ${fallbackPort}.`;
      console.log(message);
      log.warn(message);
      port = fallbackPort;
    }
  }

  localBackendPort = port;

  // 2. Start Backend Child Process
  if (runtimePaths.workspaceDir) {
    await fs.mkdir(runtimePaths.workspaceDir, { recursive: true });
  }

  const backendPath = runtimePaths.backendPath;
  const backendCwd = runtimePaths.backendCwd;

  console.log('[Main] Starting backend from:', backendPath);
  console.log('[Main] Backend CWD:', backendCwd);
  if (runtimePaths.configFile) {
    console.log('[Main] Backend config file:', runtimePaths.configFile);
  }
  if (runtimePaths.workspaceDir) {
    console.log('[Main] Backend workspace:', runtimePaths.workspaceDir);
  }
  
  try {
    backendProcess = spawn(backendPath, ['server', '--cwd', backendCwd, `--port=${port}`], {
      stdio: 'pipe',
      env: {
        ...process.env,
        PORT: port.toString(),
        AURAMATE_OWNER_PID: process.pid.toString(),
        ...(runtimePaths.configFile ? { AURAMATE_CONFIG_FILE: runtimePaths.configFile } : {}),
        ...(runtimePaths.workspaceDir ? { AURAMATE_WORKSPACE: runtimePaths.workspaceDir } : {}),
      }
    });
  } catch (err: any) {
    console.error('[Main] Failed to spawn backend:', err);
    log.error('[Main] Failed to spawn backend:', err);
    backendProcess = null;
    managedBackendPid = null;
    notifyRendererBackendStatus('error');
    return false;
  }

  managedBackendPid = backendProcess.pid ?? null;
  lastKnownBackendPid = managedBackendPid;
  backendRestartAttempts = 0;
  console.log(`[Main] Managed backend PID: ${managedBackendPid ?? 'unknown'}`);
  
  backendProcess.stdout?.on('data', (data) => {
    console.log(`[Backend]: ${data}`);
  });

  backendProcess.stderr?.on('data', (data) => {
    console.error(`[Backend Error]: ${data}`);
  });

  (backendProcess as any).on('error', (err: Error) => {
    console.error('[Main] Failed to start backend:', err);
  });
  
  (backendProcess as any).on('exit', (code: number | null, signal: NodeJS.Signals | null) => {
    console.log(`[Main] Backend exited with code ${code} and signal ${signal}`);
    backendProcess = null;
    managedBackendPid = null;

    if (!isQuitting) {
      scheduleBackendRestart();
    }
  });

  notifyRendererBackendStatus('started');
  return true;
}

const scheduleBackendRestart = () => {
  if (backendRestartTimer) {
    clearTimeout(backendRestartTimer);
    backendRestartTimer = null;
  }

  backendRestartAttempts += 1;
  if (backendRestartAttempts > MAX_BACKEND_RESTART_ATTEMPTS) {
    console.error(`[Main] Backend restart limit reached (${MAX_BACKEND_RESTART_ATTEMPTS}). Giving up.`);
    log.error(`[Main] Backend restart limit reached (${MAX_BACKEND_RESTART_ATTEMPTS}). Giving up.`);
    notifyRendererBackendStatus('failed');
    return;
  }

  const delayMs = Math.min(BACKEND_RESTART_DELAY_MS * backendRestartAttempts, 10000);
  console.log(`[Main] Scheduling backend restart attempt ${backendRestartAttempts}/${MAX_BACKEND_RESTART_ATTEMPTS} in ${delayMs}ms`);
  log.info(`[Main] Scheduling backend restart attempt ${backendRestartAttempts}/${MAX_BACKEND_RESTART_ATTEMPTS} in ${delayMs}ms`);

  notifyRendererBackendStatus('restarting');

  backendRestartTimer = setTimeout(async () => {
    backendRestartTimer = null;
    console.log(`[Main] Attempting backend restart (${backendRestartAttempts}/${MAX_BACKEND_RESTART_ATTEMPTS})...`);
    log.info(`[Main] Attempting backend restart (${backendRestartAttempts}/${MAX_BACKEND_RESTART_ATTEMPTS})...`);
    await startBackend();
    if (!backendProcess) {
      scheduleBackendRestart();
    }
  }, delayMs);
};

const cancelBackendRestart = () => {
  if (backendRestartTimer) {
    clearTimeout(backendRestartTimer);
    backendRestartTimer = null;
  }
  backendRestartAttempts = 0;
};

const notifyRendererBackendStatus = (status: 'started' | 'restarting' | 'error' | 'failed') => {
  if (win && !win.isDestroyed()) {
    win.webContents.send('backend-status-changed', { status });
  }
};

// Use local user data for development to avoid locking issues
if (!app.isPackaged) {
  const userDataPath = path.join(__dirname, '../../.cache/electron-userdata')
  app.setPath('userData', userDataPath)
  console.log(`[Main] Development mode: User Data set to ${userDataPath}`)
} else {
  const appDataPath = app.getPath('appData')
  const preferredUserDataPath = path.join(appDataPath, PACKAGED_USER_DATA_DIRNAME)
  const legacyUserDataPath = path.join(appDataPath, app.getName())

  let packagedUserDataPath = preferredUserDataPath
  if (
    legacyUserDataPath !== preferredUserDataPath &&
    fsSync.existsSync(legacyUserDataPath) &&
    !fsSync.existsSync(preferredUserDataPath)
  ) {
    try {
      fsSync.renameSync(legacyUserDataPath, preferredUserDataPath)
      console.log(`[Main] Migrated legacy user data from ${legacyUserDataPath} to ${preferredUserDataPath}`)
    } catch (error) {
      console.warn('[Main] Failed to migrate legacy user data, continuing with legacy path:', error)
      packagedUserDataPath = legacyUserDataPath
    }
  }

  app.setPath('userData', packagedUserDataPath)
  console.log(`[Main] Packaged mode: User Data set to ${packagedUserDataPath}`)
}

// The built directory structure
//
// 鈹溾攢鈹攢 dist
// 鈹?鈹溾攢- index.html
// 鈹?鈹溾攢- assets
// 鈹?鈹斺攢- ...
// 鈹溾攢鈹攢 dist-electron
// 鈹?鈹斺攢- main.js
// 鈹斺攢- ...

process.env.DIST = path.join(__dirname, '../dist')
process.env.VITE_PUBLIC = app.isPackaged ? process.env.DIST : path.join(__dirname, '../public')

// Default workspace to repo root (dev) or resources (prod)
let activeWorkspace = path.resolve(__dirname, '../../');

let win: BrowserWindow | null
let isProgrammaticBoundsChange = false
let programmaticBoundsResetTimer: NodeJS.Timeout | null = null

const VITE_DEV_SERVER_URL = process.env['VITE_DEV_SERVER_URL']

const INITIAL_WINDOW_WIDTH = EXPANDED_WINDOW_WIDTH + APP_SIDEBAR_WIDTH
const DEFAULT_WINDOW_HEIGHT = EXPANDED_WINDOW_HEIGHT
const MIN_WINDOW_WIDTH = 620
const MIN_WINDOW_HEIGHT = COLLAPSED_WINDOW_HEIGHT

type PersistedWindowState = {
  width: number
  height: number
  x?: number
  y?: number
}

type TerminalSession = {
  id: string
  title: string
  cwd: string
  shell: string
  mode: 'pty'
  process: IPty
}

const terminalSessions = new Map<string, TerminalSession>()

const terminalShell = () => {
  if (process.platform === 'win32') {
    return 'powershell.exe'
  }
  return process.env.SHELL || '/bin/bash'
}

const terminalShellArgs = (shell: string) => {
  const lower = shell.toLowerCase()
  if (process.platform === 'win32') {
    if (lower.endsWith('powershell.exe') || lower.endsWith('pwsh.exe') || lower === 'powershell' || lower === 'pwsh') {
      const psReadLineColors = [
        "$colors = @{",
        "Command = 'Black'; Parameter = 'Black'; Operator = 'Black'; Default = 'Black';",
        "Variable = 'DarkBlue'; String = 'DarkGreen'; Number = 'DarkCyan';",
        "Type = 'DarkMagenta'; Member = 'DarkBlue'; Keyword = 'DarkBlue'; Comment = 'DarkGreen'; Error = 'Red'",
        "};",
        "try { Set-PSReadLineOption -Colors $colors } catch {}",
      ].join(' ')
      return ['-NoLogo', '-NoExit', '-Command', psReadLineColors]
    }
    return []
  }
  return lower.includes('bash') || lower.includes('zsh') ? ['-l'] : []
}

const safeTerminalCwd = async (candidate?: string | null) => {
  const target = candidate && candidate.trim() ? candidate.trim() : activeWorkspace
  try {
    const stat = await fs.stat(target)
    if (stat.isDirectory()) {
      return target
    }
  } catch {
    // Fall back below.
  }
  return activeWorkspace
}

const sendTerminalData = (id: string, data: string) => {
  win?.webContents.send('terminal:data', { id, data })
}

const sendTerminalExit = (id: string, code: number | null, signal: NodeJS.Signals | null) => {
  win?.webContents.send('terminal:exit', { id, code, signal })
}

const closeTerminalSession = (id: string) => {
  const session = terminalSessions.get(id)
  if (!session) return
  terminalSessions.delete(id)
  try {
    session.process.kill()
  } catch {
    // Process may have already exited.
  }
}

const closeAllTerminalSessions = () => {
  for (const id of Array.from(terminalSessions.keys())) {
    closeTerminalSession(id)
  }
}

const clampWindowSize = (state: PersistedWindowState): PersistedWindowState => ({
  ...state,
  width: Math.max(MIN_WINDOW_WIDTH, Math.round(state.width)),
  height: Math.max(MIN_WINDOW_HEIGHT, Math.round(state.height)),
})

const getDefaultWindowState = (): PersistedWindowState => {
  const primaryDisplay = screen.getPrimaryDisplay()
  const { x, y, width, height } = primaryDisplay.workArea
  const windowWidth = Math.min(INITIAL_WINDOW_WIDTH, width)
  const windowHeight = Math.min(DEFAULT_WINDOW_HEIGHT, height)

  return {
    width: windowWidth,
    height: windowHeight,
    x: Math.round(x + (width - windowWidth) / 2),
    y: Math.round(y + (height - windowHeight) / 2),
  }
}

const clearWindowStateFile = () => {
  try {
    const windowStateFilePath = path.join(app.getPath('userData'), 'window-state.json')
    if (fsSync.existsSync(windowStateFilePath)) {
      fsSync.unlinkSync(windowStateFilePath)
    }
  } catch (error) {
    console.warn('[Main] Failed to clear persisted window state:', error)
  }
}

const markProgrammaticBoundsChange = () => {
  isProgrammaticBoundsChange = true
  if (programmaticBoundsResetTimer) {
    clearTimeout(programmaticBoundsResetTimer)
  }
  programmaticBoundsResetTimer = setTimeout(() => {
    isProgrammaticBoundsChange = false
    programmaticBoundsResetTimer = null
  }, 300)
}

const ensureWindowStateIsVisible = (state: PersistedWindowState): PersistedWindowState => {
  const display = typeof state.x === 'number' && typeof state.y === 'number'
    ? screen.getDisplayMatching({
        x: state.x,
        y: state.y,
        width: state.width,
        height: state.height,
      })
    : screen.getPrimaryDisplay()
  const workArea = display.workArea
  const clampedSize = clampWindowSize(state)
  const width = Math.min(clampedSize.width, workArea.width)
  const height = Math.min(clampedSize.height, workArea.height)
  const fallbackX = Math.round(workArea.x + (workArea.width - width) / 2)
  const fallbackY = Math.round(workArea.y + (workArea.height - height) / 2)
  const minX = workArea.x
  const minY = workArea.y
  const maxX = workArea.x + workArea.width - width
  const maxY = workArea.y + workArea.height - height

  return {
    ...clampedSize,
    width,
    height,
    x: Math.min(Math.max(typeof state.x === 'number' ? Math.round(state.x) : fallbackX, minX), maxX),
    y: Math.min(Math.max(typeof state.y === 'number' ? Math.round(state.y) : fallbackY, minY), maxY),
  }
}

// Performance & Stability Fixes
app.commandLine.appendSwitch('max-old-space-size', '4096'); // 4GB memory limit
app.commandLine.appendSwitch('disable-gpu-sandbox'); // Disable GPU sandbox for stability
if (process.platform === 'win32' && process.env.AURAMATE_DISABLE_DIRECT_COMPOSITION === '1') {
  // Only keep this escape hatch for machines with known compositor issues.
  // Disabling DirectComposition globally makes frameless window scrolling and animations stutter.
  app.commandLine.appendSwitch('disable-direct-composition')
}
app.commandLine.appendSwitch('enable-features', 'WebAssemblyUnlimitedSyncCompilation'); // Allow large WASM sync compilation used by sherpa-onnx preload

function createWindow() {
  clearWindowStateFile()
  const initialWindowState = ensureWindowStateIsVisible(getDefaultWindowState())

  // Packaged and development icon path
  const iconPath = app.isPackaged
    ? path.join(process.resourcesPath, 'icon.ico')
    : path.resolve(
        process.env.VITE_PUBLIC || path.join(__dirname, '../public'),
        'icon.ico',
      );

  win = new BrowserWindow({
    width: initialWindowState.width,
    height: initialWindowState.height,
    x: initialWindowState.x,
    y: initialWindowState.y,
    icon: iconPath,
    frame: false, 
    transparent: false,
    roundedCorners: true,
    backgroundColor: '#ffffff',
    hasShadow: true, // We want shadow now since it's the actual window
    alwaysOnTop: false,
    resizable: true, // Allow user resizing
    minWidth: MIN_WINDOW_WIDTH,
    minHeight: MIN_WINDOW_HEIGHT,
    webPreferences: {
      preload: path.join(__dirname, 'preload.mjs'),
      nodeIntegration: true,
      contextIsolation: true, // Must be true for contextBridge to work
      webSecurity: false, // TEMPORARY: Allow cross-origin for captcha
    },
    title: "XiaoYing Desktop",
    skipTaskbar: false, 
  })

  const sendWindowMaximizeState = () => {
    if (!win) {
      return;
    }
    win.webContents.send('window-maximize-state-changed', { maximized: win.isMaximized() });
  };
  win.on('maximize', sendWindowMaximizeState);
  win.on('unmaximize', sendWindowMaximizeState);

  // Configure session to spoof headers for captcha
  session.defaultSession.webRequest.onBeforeSendHeaders(
    { urls: ['*://*.alicdn.com/*', '*://*.aliyuncs.com/*', '*://*.alipay.com/*'] },
    (details, callback) => {
      details.requestHeaders['Referer'] = 'https://www.auramate.app/'; // Use a valid domain
      details.requestHeaders['Origin'] = 'https://www.auramate.app';
      callback({ cancel: false, requestHeaders: details.requestHeaders });
    }
  );

  // IPC handlers for resizing and closing
  ipcMain.handle('select-files', async () => {
    if (!win) return [];
    const result = await dialog.showOpenDialog(win, {
      properties: ['openFile', 'multiSelections'],
      title: '选择文件',
    });
    if (result.canceled || result.filePaths.length === 0) {
      return [];
    }
    // Return file info with full paths
    return result.filePaths.map(fp => ({
      name: path.basename(fp),
      path: fp,
      type: 'application/octet-stream',
      size: 0,
    }));
  });

  ipcMain.handle('show-save-dialog', async (_event, options?: {
    title?: string;
    defaultPath?: string;
    filters?: Array<{ name: string; extensions: string[] }>;
  }) => {
    if (!win) {
      return null;
    }

    const result = await dialog.showSaveDialog(win, {
      title: options?.title || 'Save As',
      defaultPath: options?.defaultPath,
      filters: options?.filters?.length
        ? options.filters
        : [{ name: 'Word Document', extensions: ['docx', 'doc'] }],
    });

    if (result.canceled || !result.filePath) {
      return null;
    }

    return result.filePath;
  });

  ipcMain.handle('show-message-box', async (_event, options: {
    type?: 'none' | 'info' | 'error' | 'question' | 'warning';
    buttons?: string[];
    defaultId?: number;
    cancelId?: number;
    title?: string;
    message: string;
    detail?: string;
    noLink?: boolean;
  }) => {
    if (!win) {
      return { response: options.cancelId ?? 0 };
    }

    return dialog.showMessageBox(win, {
      type: options.type ?? 'question',
      buttons: options.buttons ?? ['纭畾'],
      defaultId: options.defaultId,
      cancelId: options.cancelId,
      title: options.title,
      message: options.message,
      detail: options.detail,
      noLink: options.noLink ?? true,
    });
  });

  ipcMain.handle('select-directory', async () => {
    if (!win) return null;
    const result = await dialog.showOpenDialog(win, {
      properties: ['openDirectory']
    });
    if (result.canceled || result.filePaths.length === 0) {
      return null;
    }
    activeWorkspace = result.filePaths[0];
    return activeWorkspace;
  });

  ipcMain.handle('set-workspace', (event, workspacePath) => {
    if (!workspacePath || workspacePath === '.' || !path.isAbsolute(workspacePath)) {
      console.warn('set-workspace rejected invalid path:', workspacePath);
      return activeWorkspace;
    }
    activeWorkspace = workspacePath;
    return activeWorkspace;
  });

  ipcMain.handle('restart-backend', async () => {
    if (isQuitting) {
      return { ok: false, error: 'App is quitting' };
    }
    console.log('[Main] Manual backend restart requested');
    log.info('[Main] Manual backend restart requested');
    cancelBackendRestart();
    if (backendProcess) {
      killBackend();
      await new Promise<void>((resolve) => setTimeout(resolve, 500));
    }
    const started = await startBackend();
    return { ok: started };
  });

  ipcMain.handle('get-backend-status', () => {
    return {
      running: backendProcess !== null && managedBackendPid !== null,
      port: localBackendPort,
      restartAttempts: backendRestartAttempts,
      pid: managedBackendPid,
    };
  });

  ipcMain.handle('open-system-skills-dir', async () => {
    const candidates = app.isPackaged
      ? [
          path.join(process.resourcesPath, 'system_skills'),
        ]
      : [
          path.resolve(__dirname, '../../xiaoying-agent-api/system_skills'),
          path.resolve(process.cwd(), '../xiaoying-agent-api/system_skills'),
          path.resolve(process.cwd(), 'xiaoying-agent-api/system_skills'),
        ];

    let targetDir = '';
    for (const p of candidates) {
      try {
        const stat = await fs.stat(p);
        if (stat.isDirectory()) {
          targetDir = p;
          break;
        }
      } catch {
      }
    }

    if (!targetDir) {
      return { ok: false, error: 'system_skills directory not found' };
    }

    const { shell } = await import('electron');
    const errMsg = await shell.openPath(targetDir);
    if (errMsg) {
      return { ok: false, error: errMsg, path: targetDir };
    }
    return { ok: true, path: targetDir };
  });

  ipcMain.on('resize-window', (event, w, h) => {
    if (win) {
      // Keep it centered? Or just resize?
      // Usually resize down/up from top.
      // win.setSize(w, h, true); // animate
      
      // Only resize if we are programmatically changing state (e.g. expanding)
      // If user manually resized, we might want to respect that width?
      // For now, let's just apply the height change mostly, but if width is provided we use it.
      
      const currentBounds = win.getBounds();
      // If expanding/collapsing, we might want to keep the current width if it was manually adjusted?
      // But "isExpanded" logic in frontend sends a fixed width.
      // Let's trust the frontend intent for now.
      
      const newX = Math.round(currentBounds.x - (w - currentBounds.width) / 2);
      const nextBounds = ensureWindowStateIsVisible({
          x: newX,
          y: currentBounds.y,
          width: w,
          height: h
      });
      markProgrammaticBoundsChange();
      win.setBounds(nextBounds, true); // animate = true
    }
  });

  ipcMain.on('close-window', () => {
    // Hide instead of quit for spotlight feel? Or actually quit?
    // Let's quit for now as we don't have a global shortcut to reopen yet
    if (process.platform !== 'darwin') {
      app.quit();
    } else {
      app.hide();
    }
  });

  ipcMain.on('minimize-window', () => {
    if (win) {
      win.minimize();
    }
  });

  ipcMain.on('toggle-maximize-window', () => {
    if (!win) {
      return;
    }
    if (win.isMaximized()) {
      win.unmaximize();
      return;
    }
    win.maximize();
  });

  ipcMain.on('hide-window', () => {
    if (win) win.hide();
  });

  ipcMain.on('show-notification', (_event, options?: { title?: string; body?: string; silent?: boolean }) => {
    if (!Notification.isSupported()) {
      return;
    }

    const shouldNotify = !win || win.isMinimized() || !win.isVisible() || !win.isFocused();
    if (!shouldNotify) {
      return;
    }

    const notification = new Notification({
      title: typeof options?.title === 'string' && options.title.trim() ? options.title.trim() : 'XiaoYing',
      body: typeof options?.body === 'string' && options.body.trim() ? options.body.trim() : '任务已完成',
      icon: iconPath,
      silent: options?.silent ?? false,
    });

    notification.on('click', () => {
      if (!win) {
        return;
      }
      if (win.isMinimized()) {
        win.restore();
      }
      win.show();
      win.focus();
    });

    notification.show();
  });

  ipcMain.on('expand-window', () => {
    if (win) {
      const bounds = win.getBounds();
      // Only expand if width is small (indicating collapsed state)
      if (bounds.width < EXPANDED_WINDOW_WIDTH) {
        markProgrammaticBoundsChange();
        win.setBounds({
          x: bounds.x,
          y: bounds.y,
          width: EXPANDED_WINDOW_WIDTH,
          height: bounds.height
        }, true);
      }
    }
  });

  ipcMain.on('collapse-window', () => {
    if (win) {
      const bounds = win.getBounds();
      // Only collapse if width is large (indicating expanded state)
      if (bounds.width > COLLAPSED_WINDOW_WIDTH) {
        markProgrammaticBoundsChange();
        win.setBounds({
          x: bounds.x,
          y: bounds.y,
          width: COLLAPSED_WINDOW_WIDTH,
          height: bounds.height
        }, true);
      }
    }
  });

  // --- Terminal Handlers ---
  ipcMain.handle('terminal-create', async (_event, options?: { cwd?: string; cols?: number; rows?: number }) => {
    const id = randomUUID()
    const cwd = await safeTerminalCwd(options?.cwd)
    const shell = terminalShell()
    const args = terminalShellArgs(shell)
    const title = path.basename(cwd) || cwd

    const pty = await import('@lydell/node-pty')
    const terminal = pty.spawn(shell, args, {
      cwd,
      cols: options?.cols || 100,
      rows: options?.rows || 12,
      name: 'xterm-256color',
      env: {
        ...process.env,
        TERM: process.env.TERM || 'xterm-256color',
        COLORTERM: process.env.COLORTERM || 'truecolor',
      },
    })

    terminal.onData((data) => sendTerminalData(id, data))
    terminal.onExit(({ exitCode, signal }) => {
      terminalSessions.delete(id)
      sendTerminalExit(id, exitCode, signal == null ? null : String(signal) as NodeJS.Signals)
    })

    terminalSessions.set(id, {
      id,
      title,
      cwd,
      shell,
      mode: 'pty',
      process: terminal,
    })

    return { id, title, cwd, shell, mode: 'pty' }
  })

  ipcMain.handle('terminal-write', (_event, id: string, data: string) => {
    const session = terminalSessions.get(id)
    if (!session) {
      return { ok: false }
    }
    session.process.write(data)
    return { ok: true }
  })

  ipcMain.handle('terminal-resize', (_event, id: string, cols: number, rows: number) => {
    const session = terminalSessions.get(id)
    if (!session) {
      return { ok: false }
    }
    try {
      session.process.resize(cols, rows)
    } catch {
      // Ignore stale resize events while a terminal is closing.
    }
    return { ok: true, mode: session.mode }
  })

  ipcMain.handle('terminal-close', (_event, id: string) => {
    closeTerminalSession(id)
    return { ok: true }
  })

  // --- File System Handlers ---
  
  ipcMain.handle('fs-list-files', async (event, relPath) => {
    try {
        let targetPath = path.resolve(activeWorkspace, relPath || '.');
        
        // If relPath is absolute (e.g. C:\...) then respect it, don't force it under activeWorkspace
        if (path.isAbsolute(relPath) && relPath !== '.') {
             targetPath = relPath;
        } else {
             // Security check with case-insensitivity for Windows
             const normalize = (p: string) => p.split(path.sep).join('/').toLowerCase();
             const normalizedTarget = normalize(targetPath);
             const normalizedWorkspace = normalize(activeWorkspace);
             
             if (!normalizedTarget.startsWith(normalizedWorkspace)) {
                 // Relaxed check: allow if targetPath is a valid absolute path that exists?
                 // But wait, if relPath is relative, it should be under workspace.
                 // If relPath was absolute, we handled it above.
                 throw new Error(`Access denied: ${targetPath} is not within ${activeWorkspace}`);
             }
        }
        
        const entries = await fs.readdir(targetPath, { withFileTypes: true });
        const nodes = entries
            .filter(e => !e.name.startsWith('.')) // Skip hidden
            .map(e => {
                // If targetPath is outside activeWorkspace (e.g. absolute path elsewhere),
                // we should probably return absolute paths for children too, 
                // OR we need to handle "relative path" carefully.
                // Currently frontend treats 'path' as something to pass back to listFiles/readFile.
                
                // If we are browsing an absolute path external to workspace:
                if (path.isAbsolute(targetPath) && !targetPath.startsWith(activeWorkspace)) {
                    return {
                        name: e.name,
                        path: path.join(targetPath, e.name), // Return absolute path
                        type: e.isDirectory() ? 'dir' : 'file'
                    };
                }
                
                // Default relative behavior for workspace files
                return {
                    name: e.name,
                    path: path.relative(activeWorkspace, path.join(targetPath, e.name)).split(path.sep).join('/'),
                    type: e.isDirectory() ? 'dir' : 'file'
                };
            })
            .sort((a, b) => {
                if (a.type === b.type) return a.name.localeCompare(b.name);
                return a.type === 'dir' ? -1 : 1;
            });
            
        return nodes;
    } catch (err) {
        console.error('fs-list-files error:', err);
        return [];
    }
  });

  ipcMain.handle('fs-read-file', async (event, relPath) => {
    try {
        let targetPath = path.resolve(activeWorkspace, relPath);

        // Allow absolute paths outside workspace
        if (path.isAbsolute(relPath)) {
            targetPath = relPath;
        } else {
            // Security check with case-insensitivity
            const normalize = (p: string) => p.split(path.sep).join('/').toLowerCase();
            if (!normalize(targetPath).startsWith(normalize(activeWorkspace))) {
                 throw new Error("Access denied");
            }
        }
        
        const content = await fs.readFile(targetPath, 'utf-8');
        const ext = path.extname(targetPath).slice(1);
        
        return {
            content,
            path: relPath,
            language: ext
        };
    } catch (err) {
        // Suppress ENOENT errors (file not found) from console spam by returning a special object
        // The frontend should handle this gracefully.
        if ((err as any).code === 'ENOENT') {
            return {
                content: '',
                path: relPath,
                language: 'text',
                error: 'ENOENT'
            };
        }
        
        console.error('fs-read-file error:', err);
        throw err;
    }
  });

  // Removed Git related IPC handlers (git-status, git-diff, git-undo, git-keep) 
  // to prevent auto-init of git repo and remove dependency on git.
  // Frontend now handles diffing using in-memory snapshots.

  if (VITE_DEV_SERVER_URL) {
    win.loadURL(VITE_DEV_SERVER_URL)
  } else if (packagedFrontendURL) {
    win.loadURL(packagedFrontendURL)
  } else {
    win.loadURL('app://auramate.app/')
  }

  // win.setIgnoreMouseEvents(true, { forward: true }); // Initial state: let clicks pass through
  // But we handle this in CSS via pointer-events now, Electron might need explicit ignore for full transparency on some OS.
  // For Windows, `transparent: true` + CSS `pointer-events: none` usually works if the window captures events.
  // However, to be truly click-through on Windows, we might need setIgnoreMouseEvents.
  // Let's start with just visual transparency.

  // Test active push message to Renderer-process.
  win.webContents.on('did-finish-load', () => {
    win?.webContents.send('main-process-message', (new Date).toLocaleString())
  })

  win.webContents.on('did-fail-load', (_event, errorCode, errorDescription, validatedURL) => {
    console.error('[Main] Renderer failed to load:', {
      errorCode,
      errorDescription,
      validatedURL,
    })
  })

  win.webContents.on('render-process-gone', (_event, details) => {
    console.error('[Main] Renderer process gone:', details)
  })

  // Handle open external
  ipcMain.on('open-external', (_event, url) => {
    if (url.startsWith('https:') || url.startsWith('http:')) {
      import('electron').then(({ shell }) => {
        shell.openExternal(url);
      });
    }
  });

  // Handle external links
  win.webContents.setWindowOpenHandler(({ url }) => {
    if (url.startsWith('https:') || url.startsWith('http:')) {
      import('electron').then(({ shell }) => {
        shell.openExternal(url);
      });
      return { action: 'deny' };
    }
    return { action: 'allow' };
  });

}

// Register custom protocol for production
protocol.registerSchemesAsPrivileged([
  { scheme: 'app', privileges: { secure: true, standard: true, supportFetchAPI: true, corsEnabled: true } }
])

// Auto-updater Logic
function setupAutoUpdater() {
  if (!win) return;
  
  // 1. Defer updater work so startup and the first streaming chat stay responsive.
  setTimeout(() => {
    autoUpdater.checkForUpdatesAndNotify();
  }, 30000);

  // Keep periodic checks infrequent enough to avoid unnecessary runtime churn.
  setInterval(() => {
    log.info('[AutoUpdater] Checking for updates (periodic)...');
    autoUpdater.checkForUpdatesAndNotify();
  }, 30 * 60 * 1000);

  // 2. Handle update events
  autoUpdater.on('update-available', (info) => {
    log.info('Update available:', info);
    if (win) win.webContents.send('update-available', info);
  });

  autoUpdater.on('update-not-available', (info) => {
    log.info('Update not available:', info);
  });

  autoUpdater.on('error', (err) => {
    log.error('Error in auto-updater:', err);
    if (win) win.webContents.send('update-error', err.message);
  });

  autoUpdater.on('download-progress', (progressObj) => {
    let log_message = "Download speed: " + progressObj.bytesPerSecond;
    log_message = log_message + ' - Downloaded ' + progressObj.percent + '%';
    log_message = log_message + ' (' + progressObj.transferred + "/" + progressObj.total + ')';
    log.info(log_message);
    if (win) win.webContents.send('download-progress', progressObj);
  });

  autoUpdater.on('update-downloaded', (info) => {
    log.info('Update downloaded:', info);
    if (win) win.webContents.send('update-downloaded', info);
  });

  // 3. Handle install command from renderer
  ipcMain.on('install-update', async () => {
    log.info('Installing update...');
    
    // Kill backend process before updating
    killBackend();
    
    // Create restart flag
    try {
        const tempDir = app.getPath('temp');
        const flagPath = path.join(tempDir, 'auramate_restart_flag');
        await fs.writeFile(flagPath, 'true');
        log.info('Restart flag created at:', flagPath);
    } catch (e) {
        log.error('Failed to create restart flag:', e);
    }
    
    // Wait a bit for file locks to release
    setTimeout(() => {
      autoUpdater.quitAndInstall(false, true);
    }, 1000);
  });
}

// Ignore certificate errors in development
if (process.env.NODE_ENV !== 'production') {
  app.commandLine.appendSwitch('ignore-certificate-errors');
}

const killBackend = () => {
  const pidToKill = managedBackendPid ?? backendProcess?.pid ?? null;
  if (!pidToKill) {
    console.log('[Main] No managed backend PID found. Skip kill.');
    backendProcess = null;
    managedBackendPid = null;
    return;
  }

  console.log(`[Main] Killing managed backend process PID=${pidToKill}...`);
  const prevProcess = backendProcess;
  backendProcess = null;
  managedBackendPid = null;

  if (process.platform === 'win32') {
      if (killProcessTree(pidToKill)) {
        console.log('[Main] Backend killed successfully via taskkill');
      }
  } else {
      try {
        process.kill(pidToKill, 'SIGTERM');
      } catch (e) {
        console.error('[Main] Failed to kill backend:', e);
      }
  }

  if (prevProcess) {
    (prevProcess as any).removeAllListeners('exit');
  }
}

const cleanupOnQuit = (reason = 'quit') => {
  if (isQuitting) return;
  isQuitting = true;
  console.log(`[Main] Cleanup on ${reason}...`);
  cancelBackendRestart();
  closeAllTerminalSessions();
  if (packagedFrontendServer) {
    if (typeof (packagedFrontendServer as any).closeAllConnections === 'function') {
      try {
        (packagedFrontendServer as any).closeAllConnections();
      } catch (error) {
        console.warn('[Main] Failed to close all packaged frontend connections:', error);
      }
    }
    packagedFrontendServer.close();
    packagedFrontendServer = null;
    packagedFrontendURL = null;
  }
  killBackend();
};

// Ensure backend is killed on exit
app.on('before-quit', () => cleanupOnQuit('before-quit'));
app.on('will-quit', () => cleanupOnQuit('will-quit'));
app.on('quit', () => cleanupOnQuit('quit'));
process.on('exit', () => cleanupOnQuit('process-exit'));
process.on('SIGINT', () => {
  cleanupOnQuit('SIGINT');
  process.exit(0);
});
process.on('SIGTERM', () => {
  cleanupOnQuit('SIGTERM');
  process.exit(0);
});
process.on('uncaughtException', (error) => {
  console.error('[Main] Uncaught exception:', error);
  log.error('[Main] Uncaught exception:', error);
  const msg = error?.message ?? '';
  if (
    msg.includes('heap') ||
    msg.includes('CALL_AND_RETRY_LAST') ||
    msg.includes('FATAL ERROR')
  ) {
    cleanupOnQuit('uncaughtException-fatal');
    process.exit(1);
  }
});
process.on('unhandledRejection', (reason) => {
  console.error('[Main] Unhandled rejection:', reason);
});

// Quit when all windows are closed, except on macOS. There, it's common
// for applications and their menu bar to stay active until the user quits
// explicitly with Cmd + Q.
app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') {
    app.quit()
  }
})

app.on('activate', () => {
  // On OS X it's common to re-create a window in the app when the
  // dock icon is clicked and there are no other windows open.
  if (BrowserWindow.getAllWindows().length === 0) {
    createWindow()
  }
})

if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on('second-instance', () => {
    if (win) {
      if (win.isMinimized()) win.restore();
      win.show();
      win.focus();
    }
  });
}

app.whenReady().then(async () => {
  // Register 'app' protocol to serve local files
  protocol.handle('app', async (request) => {
    const url = new URL(request.url);
    const distPath = process.env.DIST || path.join(__dirname, '../dist');
    
    let relPath = decodeURIComponent(url.pathname);
    if (relPath.startsWith('/')) relPath = relPath.slice(1);
    
    // Default to index.html
    if (!relPath || relPath === '/') {
       relPath = 'index.html';
    }

    let filePath = path.join(distPath, relPath);
    
    // Check if file exists, if not, try serving index.html (for client-side routing)
    try {
        // Handle query params in file path? No, path.join handles it? 
        // filePath should be clean path. url.pathname is clean.
        await fs.access(filePath);
    } catch {
        // If file not found, DO NOT serve index.html for static assets or API-like paths
        // This prevents "Unexpected token <" errors when JSON/JS is expected
        const ext = path.extname(relPath);
        
        // Handle API requests by proxying to backend
        if (relPath.startsWith('v1/') || relPath.startsWith('api/')) {
            const apiUrl = `http://localhost:${localBackendPort}/${relPath}`;
            
            // For POST/PUT requests with body, we need to handle the body stream properly
            const fetchOptions: any = {
                method: request.method,
                headers: request.headers,
            };
            
            if (request.body) {
                fetchOptions.body = request.body;
                // Electron net.fetch might need duplex: 'half' for streams
                fetchOptions.duplex = 'half';
            }

            return net.fetch(apiUrl, fetchOptions).catch(err => {
                console.error('API proxy error:', err);
                return new Response(JSON.stringify({ error: 'API Proxy Failed' }), { 
                    status: 502,
                    headers: { 'Content-Type': 'application/json' }
                });
            });
        }

        if (ext) {
            console.error(`[Protocol] File not found: ${filePath}`);
            return new Response('Not Found', { status: 404 });
        }
        
        // Only serve index.html for route-like paths
        filePath = path.join(distPath, 'index.html');
    }

    return net.fetch(pathToFileURL(filePath).toString());
  });

  await startBackend();
  if (!(await waitForLocalTcpPort(localBackendPort))) {
    console.error(`[Main] Backend port ${localBackendPort} is not ready before renderer startup. API proxy may fail.`);
    log.error(`[Main] Backend port ${localBackendPort} is not ready before renderer startup. API proxy may fail.`);
  }
  await startPackagedFrontendServer();
  createWindow();
    setupAutoUpdater();
})

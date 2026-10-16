import { ipcRenderer, contextBridge, webUtils } from 'electron'
import fsSync from 'node:fs'
import path from 'node:path'
type SherpaOnnxModule = {
  createKws: (config: Record<string, unknown>) => {
    createStream: () => {
      acceptWaveform: (sampleRate: number, samples: Float32Array) => void
      free: () => void
    }
    isReady: (stream: unknown) => boolean
    decode: (stream: unknown) => void
    reset: (stream: unknown) => void
    getResult: (stream: unknown) => { keyword?: string; tokens?: string[] }
    free: () => void
  }
}

type SherpaOnnxKeywordSpotter = ReturnType<SherpaOnnxModule['createKws']>
type SherpaOnnxKeywordStream = ReturnType<SherpaOnnxKeywordSpotter['createStream']>

let sherpaOnnx: SherpaOnnxModule | null = null
let sherpaOnnxLoadError: string | null = null
let sherpaOnnxLoadPromise: Promise<SherpaOnnxModule | null> | null = null

const loadSherpaOnnx = async (): Promise<SherpaOnnxModule | null> => {
  if (sherpaOnnx) {
    return sherpaOnnx
  }

  if (sherpaOnnxLoadPromise) {
    return sherpaOnnxLoadPromise
  }

  sherpaOnnxLoadPromise = import('sherpa-onnx')
    .then((module) => {
      sherpaOnnx = ((module as { default?: SherpaOnnxModule }).default || module) as SherpaOnnxModule
      sherpaOnnxLoadError = null
      return sherpaOnnx
    })
    .catch((error) => {
      sherpaOnnx = null
      sherpaOnnxLoadError = error instanceof Error ? error.message : 'sherpa-onnx 加载失败'
      console.error('[WakeWord][Preload] Failed to load sherpa-onnx:', error)
      return null
    })

  return sherpaOnnxLoadPromise
}

// --------- File drop path extraction ---------
// In preload context (privileged), File objects from drag-drop HAVE the 'path' property.
// We intercept drop events here and forward the paths to the renderer.
type FileDropInfo = { name: string; path: string; type: string; size: number }
type FileDropCallback = (files: FileDropInfo[]) => void
type TerminalDataPayload = { id: string; data: string }
type TerminalExitPayload = { id: string; code: number | null; signal: string | null }
let dropCallback: FileDropCallback | null = null
let lastDropFiles: FileDropInfo[] = []

type WakeWordRuntimeState = {
  available: boolean
  ready: boolean
  listening: boolean
  keyword: string
  error: string | null
}

type WakeWordDetectedPayload = {
  keyword: string
  tokens?: string[]
  timestamp: number
}

type WakeWordStateCallback = (state: WakeWordRuntimeState) => void
type WakeWordDetectedCallback = (payload: WakeWordDetectedPayload) => void

const EXPECTED_SAMPLE_RATE = 16000
const WAKE_WORD_LABEL = '你好小英'
const WAKE_WORD_DEBOUNCE_MS = 1500

const wakeWordState: WakeWordRuntimeState = {
  available: false,
  ready: false,
  listening: false,
  keyword: WAKE_WORD_LABEL,
  error: null,
}

const wakeWordStateCallbacks: WakeWordStateCallback[] = []
const wakeWordDetectedCallbacks: WakeWordDetectedCallback[] = []

let wakeWordSpotter: SherpaOnnxKeywordSpotter | null = null
let wakeWordStream: SherpaOnnxKeywordStream | null = null
let wakeWordAudioContext: AudioContext | null = null
let wakeWordMediaStream: MediaStream | null = null
let wakeWordMediaSource: MediaStreamAudioSourceNode | null = null
let wakeWordProcessor: ScriptProcessorNode | null = null
let wakeWordSilenceGain: GainNode | null = null
let wakeWordLastDetectedAt = 0

const cloneWakeWordState = (): WakeWordRuntimeState => ({ ...wakeWordState })

const notifyWakeWordState = () => {
  const snapshot = cloneWakeWordState()
  wakeWordStateCallbacks.forEach((callback) => callback(snapshot))
}

const updateWakeWordState = (patch: Partial<WakeWordRuntimeState>) => {
  Object.assign(wakeWordState, patch)
  notifyWakeWordState()
}

const emitWakeWordDetected = (payload: WakeWordDetectedPayload) => {
  wakeWordDetectedCallbacks.forEach((callback) => callback(payload))
}

const downsampleBuffer = (
  buffer: Float32Array,
  exportSampleRate: number,
  recordSampleRate: number,
): Float32Array => {
  if (exportSampleRate === recordSampleRate) {
    return buffer
  }

  const sampleRateRatio = recordSampleRate / exportSampleRate
  const newLength = Math.round(buffer.length / sampleRateRatio)
  const result = new Float32Array(newLength)
  let offsetResult = 0
  let offsetBuffer = 0

  while (offsetResult < result.length) {
    const nextOffsetBuffer = Math.round((offsetResult + 1) * sampleRateRatio)
    let accum = 0
    let count = 0

    for (let index = offsetBuffer; index < nextOffsetBuffer && index < buffer.length; index += 1) {
      accum += buffer[index]
      count += 1
    }

    result[offsetResult] = count === 0 ? 0 : accum / count
    offsetResult += 1
    offsetBuffer = nextOffsetBuffer
  }

  return result
}

const getWakeWordResourceDir = (): string | null => {
  const candidates = [
    path.join(process.resourcesPath || '', 'kws'),
    path.resolve(process.cwd(), 'xiaoying-agent-api/release_bundle/kws'),
    path.resolve(process.cwd(), '../xiaoying-agent-api/release_bundle/kws'),
    path.resolve(process.cwd(), '../../xiaoying-agent-api/release_bundle/kws'),
  ]

  for (const candidate of candidates) {
    if (!candidate) {
      continue
    }

    if (fsSync.existsSync(path.join(candidate, 'encoder.int8.onnx'))) {
      return candidate
    }
  }

  return null
}

const getWakeWordModelPaths = () => {
  const resourceDir = getWakeWordResourceDir()
  if (!resourceDir) {
    return null
  }

  const paths = {
    encoder: path.join(resourceDir, 'encoder.int8.onnx'),
    decoder: path.join(resourceDir, 'decoder.int8.onnx'),
    joiner: path.join(resourceDir, 'joiner.int8.onnx'),
    tokens: path.join(resourceDir, 'tokens.txt'),
    keywords: path.join(resourceDir, 'keywords.txt'),
  }

  if (Object.values(paths).some((filePath) => !fsSync.existsSync(filePath))) {
    return null
  }

  return paths
}

const readWakeWordKeywords = (keywordsPath: string): string => {
  const keywords = fsSync.readFileSync(keywordsPath, 'utf8').trim()

  if (!keywords) {
    throw new Error('固定唤醒词配置为空')
  }

  return keywords
}

const ensureWakeWordSpotter = async () => {
  if (wakeWordSpotter) {
    return wakeWordSpotter
  }

  const sherpa = await loadSherpaOnnx()
  if (!sherpa) {
    updateWakeWordState({
      available: false,
      ready: false,
      listening: false,
      error: sherpaOnnxLoadError || '固定唤醒词运行时加载失败',
    })
    return null
  }

  const modelPaths = getWakeWordModelPaths()
  if (!modelPaths) {
    updateWakeWordState({
      available: false,
      ready: false,
      listening: false,
      error: '固定唤醒词模型缺失',
    })
    return null
  }

  try {
    const keywords = readWakeWordKeywords(modelPaths.keywords)

    wakeWordSpotter = sherpa.createKws({
      featConfig: {
        samplingRate: EXPECTED_SAMPLE_RATE,
        featureDim: 80,
      },
      modelConfig: {
        transducer: {
          encoder: modelPaths.encoder,
          decoder: modelPaths.decoder,
          joiner: modelPaths.joiner,
        },
        tokens: modelPaths.tokens,
        provider: 'cpu',
        numThreads: 1,
        debug: 0,
        modelingUnit: 'cjkchar',
      },
      maxActivePaths: 4,
      numTrailingBlanks: 1,
      keywordsScore: 1.6,
      keywordsThreshold: 0.35,
      keywords,
    })

    updateWakeWordState({
      available: true,
      ready: true,
      error: null,
    })

    return wakeWordSpotter
  } catch (error) {
    console.error('[WakeWord][Preload] Failed to create keyword spotter:', error)
    updateWakeWordState({
      available: false,
      ready: false,
      listening: false,
      error: error instanceof Error ? error.message : '固定唤醒词初始化失败',
    })
    return null
  }
}

const freeWakeWordStream = () => {
  if (wakeWordStream) {
    wakeWordStream.free()
    wakeWordStream = null
  }
}

const stopWakeWordListening = async (): Promise<WakeWordRuntimeState> => {
  wakeWordProcessor?.disconnect()
  wakeWordMediaSource?.disconnect()
  wakeWordSilenceGain?.disconnect()

  wakeWordProcessor = null
  wakeWordMediaSource = null
  wakeWordSilenceGain = null

  if (wakeWordMediaStream) {
    wakeWordMediaStream.getTracks().forEach((track) => track.stop())
    wakeWordMediaStream = null
  }

  freeWakeWordStream()

  if (wakeWordAudioContext) {
    try {
      await wakeWordAudioContext.close()
    } catch {
      // ignore close failures
    }
    wakeWordAudioContext = null
  }

  updateWakeWordState({ listening: false })
  return cloneWakeWordState()
}

const handleWakeWordAudio = (event: AudioProcessingEvent) => {
  if (!wakeWordSpotter) {
    return
  }

  const sourceSamples = new Float32Array(event.inputBuffer.getChannelData(0))
  const recordSampleRate = wakeWordAudioContext?.sampleRate || EXPECTED_SAMPLE_RATE
  const samples = downsampleBuffer(sourceSamples, EXPECTED_SAMPLE_RATE, recordSampleRate)

  if (!wakeWordStream) {
    wakeWordStream = wakeWordSpotter.createStream()
  }

  wakeWordStream.acceptWaveform(EXPECTED_SAMPLE_RATE, samples)

  while (wakeWordSpotter.isReady(wakeWordStream)) {
    wakeWordSpotter.decode(wakeWordStream)
    const result = wakeWordSpotter.getResult(wakeWordStream)
    const keyword = result.keyword?.trim()

    if (!keyword) {
      continue
    }

    const now = Date.now()
    if (now - wakeWordLastDetectedAt < WAKE_WORD_DEBOUNCE_MS) {
      wakeWordSpotter.reset(wakeWordStream)
      continue
    }

    wakeWordLastDetectedAt = now
    emitWakeWordDetected({
      keyword,
      tokens: result.tokens,
      timestamp: now,
    })
    wakeWordSpotter.reset(wakeWordStream)
  }
}

const initWakeWord = async (): Promise<WakeWordRuntimeState> => {
  await ensureWakeWordSpotter()
  return cloneWakeWordState()
}

const startWakeWordListening = async (): Promise<WakeWordRuntimeState> => {
  const spotter = await ensureWakeWordSpotter()
  if (!spotter) {
    return cloneWakeWordState()
  }

  if (wakeWordState.listening) {
    return cloneWakeWordState()
  }

  if (!navigator.mediaDevices?.getUserMedia) {
    updateWakeWordState({
      listening: false,
      error: '当前环境不支持麦克风监听',
    })
    return cloneWakeWordState()
  }

  try {
    wakeWordMediaStream = await navigator.mediaDevices.getUserMedia({
      audio: {
        channelCount: 1,
        echoCancellation: true,
        noiseSuppression: true,
        autoGainControl: true,
      },
    })

    wakeWordAudioContext = new AudioContext({ sampleRate: EXPECTED_SAMPLE_RATE })
    wakeWordMediaSource = wakeWordAudioContext.createMediaStreamSource(wakeWordMediaStream)
    wakeWordProcessor = wakeWordAudioContext.createScriptProcessor(4096, 1, 1)
    wakeWordSilenceGain = wakeWordAudioContext.createGain()
    wakeWordSilenceGain.gain.value = 0
    wakeWordLastDetectedAt = 0
    freeWakeWordStream()
    wakeWordStream = spotter.createStream()

    wakeWordProcessor.onaudioprocess = handleWakeWordAudio
    wakeWordMediaSource.connect(wakeWordProcessor)
    wakeWordProcessor.connect(wakeWordSilenceGain)
    wakeWordSilenceGain.connect(wakeWordAudioContext.destination)

    updateWakeWordState({
      listening: true,
      ready: true,
      available: true,
      error: null,
    })
  } catch (error) {
    console.error('[WakeWord][Preload] Failed to start listening:', error)
    await stopWakeWordListening()
    updateWakeWordState({
      listening: false,
      error: error instanceof Error ? error.message : '启动语音唤醒失败',
    })
  }

  return cloneWakeWordState()
}

const resolveDropFilePath = (file: File): string => {
  const directPath = (file as any).path
  if (typeof directPath === 'string' && directPath.trim()) {
    return directPath
  }

  try {
    const resolvedPath = webUtils.getPathForFile(file)
    if (typeof resolvedPath === 'string' && resolvedPath.trim()) {
      return resolvedPath
    }
  } catch {
    // ignore
  }

  return file.name
}

window.addEventListener('DOMContentLoaded', () => {
  document.addEventListener('drop', (e) => {
    if (e.dataTransfer && e.dataTransfer.files.length > 0 && dropCallback) {
      const fileInfos: FileDropInfo[] = Array.from(e.dataTransfer.files).map(f => ({
        name: f.name,
        path: resolveDropFilePath(f),
        type: f.type, // Keep empty for folders, don't force octet-stream
        size: f.size,
      }))
      lastDropFiles = fileInfos
      console.log('[Preload] Drop intercepted, paths:', fileInfos.map(f => f.path))
      dropCallback(fileInfos)
    } else if (e.dataTransfer && e.dataTransfer.files.length > 0) {
      // Keep a cache even when callback is not registered yet.
      lastDropFiles = Array.from(e.dataTransfer.files).map(f => ({
        name: f.name,
        path: resolveDropFilePath(f),
        type: f.type, // Keep empty for folders, don't force octet-stream
        size: f.size,
      }))
    }
  }, true) // Use capture phase to get the event before renderer
})

window.addEventListener('beforeunload', () => {
  void stopWakeWordListening()
})

// --------- Expose some API to the Renderer process ---------
contextBridge.exposeInMainWorld('electron', {
  resizeWindow: (width: number, height: number) => ipcRenderer.send('resize-window', width, height),
  minimizeWindow: () => ipcRenderer.send('minimize-window'),
  toggleMaximizeWindow: () => ipcRenderer.send('toggle-maximize-window'),
  onWindowMaximizeStateChanged: (callback: (state: { maximized: boolean }) => void) => {
    const listener = (_event: Electron.IpcRendererEvent, state: { maximized: boolean }) => callback(state)
    ipcRenderer.on('window-maximize-state-changed', listener)
    return () => ipcRenderer.removeListener('window-maximize-state-changed', listener)
  },
  closeWindow: () => ipcRenderer.send('close-window'),
  hideWindow: () => ipcRenderer.send('hide-window'),
  showSaveDialog: (options?: { title?: string; defaultPath?: string; filters?: Array<{ name: string; extensions: string[] }> }) => ipcRenderer.invoke('show-save-dialog', options),
  showMessageBox: (options: { type?: 'none' | 'info' | 'error' | 'question' | 'warning'; buttons?: string[]; defaultId?: number; cancelId?: number; title?: string; message: string; detail?: string; noLink?: boolean }) => ipcRenderer.invoke('show-message-box', options),
  showNotification: (options?: { title?: string; body?: string; silent?: boolean }) => ipcRenderer.send('show-notification', options),
  // Update events
  onUpdateAvailable: (callback: (info: any) => void) => ipcRenderer.on('update-available', (_event, info) => callback(info)),
  onUpdateDownloaded: (callback: (info: any) => void) => ipcRenderer.on('update-downloaded', (_event, info) => callback(info)),
  onDownloadProgress: (callback: (progress: any) => void) => ipcRenderer.on('download-progress', (_event, progress) => callback(progress)),
  onUpdateError: (callback: (err: any) => void) => ipcRenderer.on('update-error', (_event, err) => callback(err)),
  installUpdate: () => ipcRenderer.send('install-update'),
  // Shell
  openExternal: (url: string) => ipcRenderer.send('open-external', url),
  openSystemSkillsDir: () => ipcRenderer.invoke('open-system-skills-dir'),
  // File drop: register callback to receive file paths from drag-drop
  onFileDrop: (callback: FileDropCallback) => { dropCallback = callback },
  consumeDroppedFiles: () => {
    const files = lastDropFiles
    lastDropFiles = []
    return files
  },
  // File selection dialog: returns array of file paths
  selectFiles: () => ipcRenderer.invoke('select-files'),
  wakeWord: {
    init: () => initWakeWord(),
    start: () => startWakeWordListening(),
    stop: () => stopWakeWordListening(),
    getState: () => cloneWakeWordState(),
    onDetected: (callback: WakeWordDetectedCallback) => {
      wakeWordDetectedCallbacks.push(callback)
    },
    onStateChanged: (callback: WakeWordStateCallback) => {
      wakeWordStateCallbacks.push(callback)
      callback(cloneWakeWordState())
    },
  },
})

contextBridge.exposeInMainWorld('electronAPI', {
  expandWindow: () => ipcRenderer.send('expand-window'),
  collapseWindow: () => ipcRenderer.send('collapse-window'),
  // File System Operations
  listFiles: (path: string) => ipcRenderer.invoke('fs-list-files', path),
  readFile: (path: string) => ipcRenderer.invoke('fs-read-file', path),
  // Git Operations
  gitStatus: () => ipcRenderer.invoke('git-status'),
  gitDiff: (path: string) => ipcRenderer.invoke('git-diff', path),
  gitUndo: (path: string) => ipcRenderer.invoke('git-undo', path),
  gitUndoAll: () => ipcRenderer.invoke('git-undo-all'),
  gitKeep: (path: string) => ipcRenderer.invoke('git-keep', path),
  gitKeepAll: () => ipcRenderer.invoke('git-keep-all'),
  selectDirectory: () => ipcRenderer.invoke('select-directory'),
  setWorkspace: (path: string) => ipcRenderer.invoke('set-workspace', path),
  restartBackend: () => ipcRenderer.invoke('restart-backend'),
  getBackendStatus: () => ipcRenderer.invoke('get-backend-status'),
  onBackendStatusChanged: (callback: (payload: { status: string }) => void) => {
    const listener = (_event: Electron.IpcRendererEvent, payload: { status: string }) => callback(payload)
    ipcRenderer.on('backend-status-changed', listener)
    return () => ipcRenderer.removeListener('backend-status-changed', listener)
  },
})

contextBridge.exposeInMainWorld('terminalAPI', {
  create: (options?: { cwd?: string; cols?: number; rows?: number }) => ipcRenderer.invoke('terminal-create', options),
  write: (id: string, data: string) => ipcRenderer.invoke('terminal-write', id, data),
  resize: (id: string, cols: number, rows: number) => ipcRenderer.invoke('terminal-resize', id, cols, rows),
  close: (id: string) => ipcRenderer.invoke('terminal-close', id),
  onData: (callback: (payload: TerminalDataPayload) => void) => {
    const listener = (_event: Electron.IpcRendererEvent, payload: TerminalDataPayload) => callback(payload)
    ipcRenderer.on('terminal:data', listener)
    return () => ipcRenderer.removeListener('terminal:data', listener)
  },
  onExit: (callback: (payload: TerminalExitPayload) => void) => {
    const listener = (_event: Electron.IpcRendererEvent, payload: TerminalExitPayload) => callback(payload)
    ipcRenderer.on('terminal:exit', listener)
    return () => ipcRenderer.removeListener('terminal:exit', listener)
  },
})

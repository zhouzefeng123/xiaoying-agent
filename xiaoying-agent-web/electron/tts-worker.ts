import { parentPort, workerData } from 'node:worker_threads'
import fsSync from 'node:fs'
import path from 'node:path'
import sherpaOnnxModule from 'sherpa-onnx'

type SherpaOnnxModule = typeof sherpaOnnxModule

type SherpaOnnxOfflineTts = ReturnType<SherpaOnnxModule['createOfflineTts']>

type TtsWorkerData = {
  resourcesPath?: string
  cwd: string
}

type TtsSynthesizeRequest = {
  id: string
  kind: 'synthesize'
  text: string
  voice?: string
}

type TtsSynthesisResult = {
  audioBuffer: ArrayBuffer
  mimeType: string
  sampleRate: number
  voice: string
  speakerId: number
}

type TtsWorkerSuccessResponse = {
  id: string
  ok: true
  result: TtsSynthesisResult
}

type TtsWorkerErrorResponse = {
  id: string
  ok: false
  error: string
}

const EXPECTED_SAMPLE_RATE = 24000
const KOKORO_DEFAULT_VOICE = 'zf_xiaobei'
const KOKORO_DEFAULT_SPEAKER_ID = 45

const KOKORO_VOICE_ALIASES: Record<string, number> = {
  '45': 45,
  'zf_xiaobei': 45,
  'female': 45,
  'zh-female': 45,
  'chinese-female': 45,
  '49': 49,
  'zm_yunjian': 49,
  'male': 49,
  'zh-male': 49,
  'chinese-male': 49,
}

const { resourcesPath, cwd } = workerData as TtsWorkerData

let localTts: SherpaOnnxOfflineTts | null = null

const findFirstExistingFile = (resourceDir: string, fileNames: string[]): string | null => {
  for (const fileName of fileNames) {
    const candidate = path.join(resourceDir, fileName)
    if (fsSync.existsSync(candidate)) {
      return candidate
    }
  }

  return null
}

const getLocalTtsResourceDir = (): string | null => {
  const candidates = [
    path.join(resourcesPath || '', 'tts', 'kokoro'),
    path.join(resourcesPath || '', 'kokoro'),
    path.resolve(cwd, 'xiaoying-agent-api/release_bundle/tts/kokoro'),
    path.resolve(cwd, '../xiaoying-agent-api/release_bundle/tts/kokoro'),
    path.resolve(cwd, '../../xiaoying-agent-api/release_bundle/tts/kokoro'),
  ]

  for (const candidate of candidates) {
    if (!candidate) {
      continue
    }

    if (findFirstExistingFile(candidate, ['model.onnx', 'model.int8.onnx'])) {
      return candidate
    }
  }

  return null
}

const getLocalTtsModelPaths = () => {
  const resourceDir = getLocalTtsResourceDir()
  if (!resourceDir) {
    return null
  }

  const model = findFirstExistingFile(resourceDir, ['model.onnx', 'model.int8.onnx'])
  const voices = path.join(resourceDir, 'voices.bin')
  const tokens = path.join(resourceDir, 'tokens.txt')
  const dataDir = path.join(resourceDir, 'espeak-ng-data')

  const lexiconFiles = [
    'lexicon-us-en.txt',
    'lexicon-zh.txt',
  ]
    .map((fileName) => path.join(resourceDir, fileName))
    .filter((filePath) => fsSync.existsSync(filePath))

  if (!model || !fsSync.existsSync(voices) || !fsSync.existsSync(tokens) || !fsSync.existsSync(dataDir)) {
    return null
  }

  if (lexiconFiles.length === 0) {
    return null
  }

  return {
    model,
    voices,
    tokens,
    dataDir,
    lexicon: lexiconFiles.join(','),
  }
}

const resolveKokoroSpeakerId = (voice?: string): number => {
  if (!voice) {
    return KOKORO_DEFAULT_SPEAKER_ID
  }

  const normalizedVoice = voice.trim().toLowerCase().replace(/^kokoro:/, '')
  if (!normalizedVoice) {
    return KOKORO_DEFAULT_SPEAKER_ID
  }

  if (/^\d+$/.test(normalizedVoice)) {
    return Number.parseInt(normalizedVoice, 10)
  }

  return KOKORO_VOICE_ALIASES[normalizedVoice] ?? KOKORO_DEFAULT_SPEAKER_ID
}

const writeWavHeader = (view: DataView, sampleRate: number, dataLength: number) => {
  const writeAscii = (offset: number, value: string) => {
    for (let index = 0; index < value.length; index += 1) {
      view.setUint8(offset + index, value.charCodeAt(index))
    }
  }

  writeAscii(0, 'RIFF')
  view.setUint32(4, 36 + dataLength, true)
  writeAscii(8, 'WAVE')
  writeAscii(12, 'fmt ')
  view.setUint32(16, 16, true)
  view.setUint16(20, 1, true)
  view.setUint16(22, 1, true)
  view.setUint32(24, sampleRate, true)
  view.setUint32(28, sampleRate * 2, true)
  view.setUint16(32, 2, true)
  view.setUint16(34, 16, true)
  writeAscii(36, 'data')
  view.setUint32(40, dataLength, true)
}

const float32ToWavBuffer = (samples: Float32Array, sampleRate: number): ArrayBuffer => {
  const dataLength = samples.length * 2
  const buffer = new ArrayBuffer(44 + dataLength)
  const view = new DataView(buffer)

  writeWavHeader(view, sampleRate, dataLength)

  let offset = 44
  for (let index = 0; index < samples.length; index += 1) {
    const sample = Math.max(-1, Math.min(1, samples[index]))
    const pcmValue = sample < 0 ? sample * 0x8000 : sample * 0x7fff
    view.setInt16(offset, Math.round(pcmValue), true)
    offset += 2
  }

  return buffer
}

const ensureLocalTts = (): SherpaOnnxOfflineTts => {
  if (localTts) {
    return localTts
  }

  const modelPaths = getLocalTtsModelPaths()
  if (!modelPaths) {
    throw new Error('Kokoro 本地语音模型缺失')
  }

  localTts = sherpaOnnxModule.createOfflineTts({
    offlineTtsModelConfig: {
      offlineTtsKokoroModelConfig: {
        model: modelPaths.model,
        voices: modelPaths.voices,
        tokens: modelPaths.tokens,
        dataDir: modelPaths.dataDir,
        lexicon: modelPaths.lexicon,
        lengthScale: 1.0,
      },
      provider: 'cpu',
      numThreads: 1,
      debug: 0,
    },
    ruleFars: '',
    maxNumSentences: 1,
    silenceScale: 0.2,
  })

  return localTts
}

const synthesize = (text: string, voice?: string): TtsSynthesisResult => {
  const normalizedText = text.trim()
  const normalizedVoice = voice?.trim() || KOKORO_DEFAULT_VOICE

  if (!normalizedText) {
    return {
      audioBuffer: new ArrayBuffer(0),
      mimeType: 'audio/wav',
      sampleRate: EXPECTED_SAMPLE_RATE,
      voice: normalizedVoice,
      speakerId: resolveKokoroSpeakerId(normalizedVoice),
    }
  }

  const tts = ensureLocalTts()
  const speakerId = resolveKokoroSpeakerId(normalizedVoice)
  const audio = tts.generate({
    text: normalizedText,
    sid: speakerId,
    speed: 1.0,
  })

  return {
    audioBuffer: float32ToWavBuffer(audio.samples, audio.sampleRate),
    mimeType: 'audio/wav',
    sampleRate: audio.sampleRate,
    voice: normalizedVoice,
    speakerId,
  }
}

const postResponse = (response: TtsWorkerSuccessResponse | TtsWorkerErrorResponse, transferList?: Transferable[]) => {
  parentPort?.postMessage(response, transferList ?? [])
}

parentPort?.on('message', (request: TtsSynthesizeRequest) => {
  if (request.kind !== 'synthesize') {
    return
  }

  try {
    const result = synthesize(request.text, request.voice)
    postResponse({
      id: request.id,
      ok: true,
      result,
    }, [result.audioBuffer])
  } catch (error) {
    postResponse({
      id: request.id,
      ok: false,
      error: error instanceof Error ? error.message : 'Kokoro 本地语音生成失败',
    })
  }
})

process.on('exit', () => {
  localTts?.free()
  localTts = null
})

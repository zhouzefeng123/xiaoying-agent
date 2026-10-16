import { useState, useRef, useCallback } from 'react';
import { toast } from 'sonner';
import { encodeWAV } from '../lib/wav-encoder';

interface UseTranscriptionProps {
    onTranscriptionComplete?: (text: string) => void;
    onInterimResult?: (text: string) => void;
}

interface UseTranscriptionReturn {
    isRecording: boolean;
    startRecording: () => Promise<boolean>;
    stopRecording: () => Promise<string | null>;
    cancelRecording: () => void;
    isTranscribing: boolean;
    analyser: AnalyserNode | null;
}

type SpeechRecognitionResultItem = {
    isFinal: boolean;
    0?: {
        transcript?: string;
    };
};

type SpeechRecognitionResultEventLike = {
    resultIndex: number;
    results: ArrayLike<SpeechRecognitionResultItem>;
};

type BrowserSpeechRecognition = {
    lang: string;
    continuous: boolean;
    interimResults: boolean;
    onresult: ((event: SpeechRecognitionResultEventLike) => void) | null;
    onerror: (() => void) | null;
    onend: (() => void) | null;
    start: () => void;
    stop: () => void;
};

interface UploadAudioOptions {
    signal?: AbortSignal;
    timeoutMs?: number;
    suppressAbortToast?: boolean;
    suppressErrorToast?: boolean;
}

export function useTranscription({ onTranscriptionComplete, onInterimResult }: UseTranscriptionProps = {}): UseTranscriptionReturn {
    const [isRecording, setIsRecording] = useState(false);
    const [isTranscribing, setIsTranscribing] = useState(false);
    const [analyser, setAnalyser] = useState<AnalyserNode | null>(null);
    
    // WAV Recording Refs
    const processorRef = useRef<ScriptProcessorNode | null>(null);
    const pcmChunksRef = useRef<Float32Array[]>([]);
    const sampleRateRef = useRef<number>(16000);

    const audioContextRef = useRef<AudioContext | null>(null);
    const analyserRef = useRef<AnalyserNode | null>(null);
    const sourceRef = useRef<MediaStreamAudioSourceNode | null>(null);
    const silenceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    const hasSpokenRef = useRef(false);
    const streamRef = useRef<MediaStream | null>(null);
    const recognitionRef = useRef<BrowserSpeechRecognition | null>(null);
    const interimFinalRef = useRef('');
    const recordingActiveRef = useRef(false);
    const recordingSessionRef = useRef(0);
    const speechActiveRef = useRef(false);
    const speechFrameCountRef = useRef(0);
    const lastSpeechAtRef = useRef<number | null>(null);
    const noiseFloorRef = useRef(0.012);
    const smoothedVolumeRef = useRef(0);

    // VAD Parameters
    const VAD_START_THRESHOLD = 0.07;
    const VAD_END_THRESHOLD = 0.048;
    const VAD_START_MARGIN = 0.022;
    const VAD_END_MARGIN = 0.012;
    const NOISE_FLOOR_SMOOTHING = 0.05;
    const VOLUME_SMOOTHING = 0.28;
    const SPEECH_CONFIRMATION_FRAMES = 3;
    const SPEECH_HANGOVER_MS = 300;
    const AUTO_STOP_SILENCE_MS = 800;

    const cleanupAudio = useCallback(() => {
        recordingActiveRef.current = false;
        if (streamRef.current) {
            streamRef.current.getTracks().forEach(track => track.stop());
            streamRef.current = null;
        }
        if (processorRef.current) {
            processorRef.current.disconnect();
            processorRef.current.onaudioprocess = null;
            processorRef.current = null;
        }
        if (sourceRef.current) {
            sourceRef.current.disconnect();
            sourceRef.current = null;
        }
        if (analyserRef.current) {
            analyserRef.current.disconnect();
            analyserRef.current = null;
        }
        if (audioContextRef.current) {
            if (audioContextRef.current.state !== 'closed') {
                audioContextRef.current.close();
            }
            audioContextRef.current = null;
        }
        if (silenceTimerRef.current) {
            clearTimeout(silenceTimerRef.current);
            silenceTimerRef.current = null;
        }
        if (recognitionRef.current) {
            try {
                recognitionRef.current.onresult = null;
                recognitionRef.current.onerror = null;
                recognitionRef.current.onend = null;
                recognitionRef.current.stop();
            } catch {
                void 0;
            }
            recognitionRef.current = null;
        }
        speechActiveRef.current = false;
        speechFrameCountRef.current = 0;
        lastSpeechAtRef.current = null;
        noiseFloorRef.current = 0.012;
        smoothedVolumeRef.current = 0;
        setAnalyser(null);
    }, []);

    const uploadAudio = async (audioBlob: Blob, options: UploadAudioOptions = {}): Promise<string> => {
        const {
            signal,
            timeoutMs = 15000,
            suppressAbortToast = false,
            suppressErrorToast = false,
        } = options;
        const formData = new FormData();
        const ext = audioBlob.type.includes('wav') ? 'wav' : 'webm';
        const file = new File([audioBlob], `recording.${ext}`, { 
            type: audioBlob.type || 'audio/wav',
            lastModified: Date.now() 
        });
        formData.append('file', file);

        // 语音输入固定走本地 SenseVoice Small ONNX 方案。
        formData.append('model', 'local-sensevoice');

        const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || '';
        
        try {
            const controller = new AbortController();
            const timeoutId = setTimeout(() => controller.abort(), timeoutMs);
            const abortHandler = () => controller.abort();

            if (signal) {
                if (signal.aborted) {
                    clearTimeout(timeoutId);
                    controller.abort();
                } else {
                    signal.addEventListener('abort', abortHandler, { once: true });
                }
            }

            const response = await fetch(`${apiBaseUrl}/v1/transcribe`, {
                method: 'POST',
                body: formData,
                signal: controller.signal
            });

            clearTimeout(timeoutId);
            if (signal) {
                signal.removeEventListener('abort', abortHandler);
            }

            if (!response.ok) {
                throw new Error(`Upload failed: ${response.statusText}`);
            }

            const data = await response.json();
            return data.text || '';
        } catch (error: any) {
            if (error.name === 'AbortError') {
                console.error('Transcription upload timeout');
                if (!suppressAbortToast) {
                    toast.error('语音转换超时');
                }
            } else {
                console.error('Transcription upload error:', error);
                if (!suppressErrorToast) {
                    toast.error('语音转换失败');
                }
            }
            throw error;
        }
    };

    const stopRecording = useCallback(async (): Promise<string | null> => {
        if (!recordingActiveRef.current) return null;
        recordingActiveRef.current = false;

        return new Promise((resolve) => {
            // Encode captured PCM to WAV
            let audioBlob: Blob;
            if (pcmChunksRef.current.length > 0) {
                // Merge chunks
                const totalLength = pcmChunksRef.current.reduce((acc, chunk) => acc + chunk.length, 0);
                const merged = new Float32Array(totalLength);
                let offset = 0;
                for (const chunk of pcmChunksRef.current) {
                    merged.set(chunk, offset);
                    offset += chunk.length;
                }
                audioBlob = encodeWAV(merged, sampleRateRef.current);
            } else {
                audioBlob = new Blob([], { type: 'audio/wav' });
            }

            cleanupAudio();
            setIsRecording(false);

            if (!hasSpokenRef.current) {
                console.log('[Transcription] No confirmed speech detected, skipping final upload');
                resolve(null);
                return;
            }
            
            if (audioBlob.size < 1000) {
                console.log('[Transcription] Audio too short, skipping');
                resolve(null);
                return;
            }

            setIsTranscribing(true);
            uploadAudio(audioBlob)
                .then(text => {
                    console.log('[Transcription] Result:', text);
                    if (text.trim()) {
                        onTranscriptionComplete?.(text);
                    } else {
                        console.log('[Transcription] Empty result, skipping');
                    }
                    resolve(text);
                })
                .catch(e => {
                    console.warn('Transcription failed or empty:', e);
                    resolve(null);
                })
                .finally(() => {
                    setIsTranscribing(false);
                    speechActiveRef.current = false;
                    speechFrameCountRef.current = 0;
                    lastSpeechAtRef.current = null;
                });
        });
    }, [cleanupAudio, onTranscriptionComplete]);

    const startRecording = useCallback(async () => {
        try {
            // 1. Interrupt playback
            window.speechSynthesis.cancel();
            
            // 2. Get stream
            const stream = await navigator.mediaDevices.getUserMedia({ 
                audio: {
                    echoCancellation: true,
                    noiseSuppression: true,
                    autoGainControl: true,
                    channelCount: 1
                } 
            });
            streamRef.current = stream;
            interimFinalRef.current = '';
            recordingSessionRef.current += 1;

            const speechWindow = window as Window & {
                SpeechRecognition?: new () => BrowserSpeechRecognition;
                webkitSpeechRecognition?: new () => BrowserSpeechRecognition;
            };
            const SpeechRecognitionCtor = speechWindow.SpeechRecognition || speechWindow.webkitSpeechRecognition;
            const isElectronRuntime = navigator.userAgent.toLowerCase().includes('electron');
            const canUseBrowserInterim = Boolean(SpeechRecognitionCtor) && !isElectronRuntime;

            if (canUseBrowserInterim && SpeechRecognitionCtor) {
                const recognition = new SpeechRecognitionCtor();
                recognition.lang = 'zh-CN';
                recognition.continuous = true;
                recognition.interimResults = true;
                recognition.onresult = (event: SpeechRecognitionResultEventLike) => {
                    let interim = '';
                    for (let i = event.resultIndex; i < event.results.length; i += 1) {
                        const transcript = event.results[i]?.[0]?.transcript || '';
                        if (event.results[i].isFinal) {
                            interimFinalRef.current += transcript;
                        } else {
                            interim += transcript;
                        }
                    }
                    const merged = `${interimFinalRef.current}${interim}`.trim();
                    if (merged) {
                        onInterimResult?.(merged);
                    }
                };
                recognition.onerror = () => {
                    void 0;
                };
                recognition.onend = () => {
                    if (!recordingActiveRef.current) {
                        return;
                    }

                    try {
                        recognition.start();
                    } catch {
                        void 0;
                    }
                };
                recognition.start();
                recognitionRef.current = recognition;
            }

            // 3. Setup AudioContext & Processor for WAV recording
            const audioContext = new AudioContext();
            audioContextRef.current = audioContext;
            sampleRateRef.current = audioContext.sampleRate;

            const source = audioContext.createMediaStreamSource(stream);
            sourceRef.current = source;

            // Smaller buffer reduces first-chunk latency for local streaming STT.
            const processor = audioContext.createScriptProcessor(2048, 1, 1);
            processorRef.current = processor;
            pcmChunksRef.current = [];

            processor.onaudioprocess = (e) => {
                if (!hasSpokenRef.current && pcmChunksRef.current.length > 50) {
                     // Ring buffer for pre-speech? For now just simple append
                     // actually we want to keep some buffer before speech detected?
                }
                
                const channelData = e.inputBuffer.getChannelData(0);
                // Clone the data (important!)
                pcmChunksRef.current.push(new Float32Array(channelData));
            };

            // 4. Setup VAD (Reuse same context)
            const analyser = audioContext.createAnalyser();
            analyser.fftSize = 256;
            analyserRef.current = analyser;
            setAnalyser(analyser);

            // Connect graph: Source -> Analyser -> Processor -> Destination
            source.connect(analyser);
            analyser.connect(processor);
            const silentGain = audioContext.createGain();
            silentGain.gain.value = 0;
            processor.connect(silentGain);
            silentGain.connect(audioContext.destination);

            // VAD Logic
            const frequencyBufferLength = analyser.frequencyBinCount;
            const frequencyDataArray = new Uint8Array(frequencyBufferLength);
            const timeDataArray = new Uint8Array(analyser.fftSize);
            hasSpokenRef.current = false;
            speechActiveRef.current = false;
            speechFrameCountRef.current = 0;
            lastSpeechAtRef.current = null;
            noiseFloorRef.current = 0.012;
            smoothedVolumeRef.current = 0;

            const checkVolume = () => {
                if (!analyserRef.current) return;
                
                analyserRef.current.getByteFrequencyData(frequencyDataArray);
                analyserRef.current.getByteTimeDomainData(timeDataArray);
                let sum = 0;
                for (let i = 0; i < frequencyBufferLength; i += 1) {
                    sum += frequencyDataArray[i];
                }
                const frequencyVolume = (sum / frequencyBufferLength) / 255;
                let squareSum = 0;
                for (let i = 0; i < timeDataArray.length; i += 1) {
                    const normalized = (timeDataArray[i] - 128) / 128;
                    squareSum += normalized * normalized;
                }
                const rms = Math.sqrt(squareSum / timeDataArray.length);
                const volume = Math.max(frequencyVolume * 0.6, rms);
                const smoothedVolume = (smoothedVolumeRef.current * (1 - VOLUME_SMOOTHING)) + (volume * VOLUME_SMOOTHING);
                smoothedVolumeRef.current = smoothedVolume;

                const now = Date.now();
                if (!speechActiveRef.current && smoothedVolume < noiseFloorRef.current + VAD_START_MARGIN * 1.6) {
                    noiseFloorRef.current = (noiseFloorRef.current * (1 - NOISE_FLOOR_SMOOTHING)) + (smoothedVolume * NOISE_FLOOR_SMOOTHING);
                }

                const dynamicStartThreshold = Math.max(VAD_START_THRESHOLD, noiseFloorRef.current + VAD_START_MARGIN);
                const dynamicEndThreshold = Math.max(VAD_END_THRESHOLD, noiseFloorRef.current + VAD_END_MARGIN);
                const activeThreshold = speechActiveRef.current ? dynamicEndThreshold : dynamicStartThreshold;
                if (smoothedVolume > activeThreshold) {
                    speechFrameCountRef.current += 1;
                    lastSpeechAtRef.current = now;

                    if (!speechActiveRef.current && speechFrameCountRef.current >= SPEECH_CONFIRMATION_FRAMES) {
                        console.log('[VAD] Speech detected', {
                            smoothedVolume: Number(smoothedVolume.toFixed(4)),
                            noiseFloor: Number(noiseFloorRef.current.toFixed(4)),
                            threshold: Number(dynamicStartThreshold.toFixed(4)),
                        });
                        speechActiveRef.current = true;
                        hasSpokenRef.current = true;
                    }
                } else {
                    speechFrameCountRef.current = 0;
                }

                if (speechActiveRef.current) {
                    if (lastSpeechAtRef.current !== null && now - lastSpeechAtRef.current > SPEECH_HANGOVER_MS) {
                        speechActiveRef.current = false;
                    }
                }

                if (speechActiveRef.current) {
                    if (silenceTimerRef.current) {
                        clearTimeout(silenceTimerRef.current);
                        silenceTimerRef.current = null;
                    }
                } else if (hasSpokenRef.current && lastSpeechAtRef.current !== null) {
                    const silenceElapsed = now - lastSpeechAtRef.current;
                    const remainingSilence = Math.max(AUTO_STOP_SILENCE_MS - silenceElapsed, 0);

                    if (!silenceTimerRef.current) {
                        silenceTimerRef.current = setTimeout(() => {
                            silenceTimerRef.current = null;
                            if (!recordingActiveRef.current || speechActiveRef.current) {
                                return;
                            }

                            void stopRecording();
                        }, remainingSilence);
                    }
                }

                if (audioContextRef.current?.state === 'running') {
                    requestAnimationFrame(checkVolume);
                }
            };

            recordingActiveRef.current = true;
            setIsRecording(true);
            checkVolume();
            return true;

        } catch (error) {
            console.error('Failed to start recording:', error);
            toast.error('无法启动录音');
            cleanupAudio();
            return false;
        }
    }, [AUTO_STOP_SILENCE_MS, SPEECH_CONFIRMATION_FRAMES, SPEECH_HANGOVER_MS, cleanupAudio, stopRecording]);

    const cancelRecording = useCallback(() => {
        recordingActiveRef.current = false;
        cleanupAudio();
        setIsRecording(false);
    }, [cleanupAudio]);

    return {
        isRecording,
        startRecording,
        stopRecording,
        cancelRecording,
        isTranscribing,
        analyser
    };
}

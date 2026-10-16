import { useState, useCallback, useRef, useEffect } from 'react';
import { toast } from 'sonner';

interface UseTTSReturn {
    speak: (text: string, voice?: string, mode?: 'interrupt' | 'append') => Promise<void>;
    stop: () => void;
    isSpeaking: boolean;
}

interface QueueItem {
    text: string;
    voice: string;
    resolve: () => void;
    reject: (e: unknown) => void;
    // New fields for prefetching
    audioUrl?: string;
    fetchPromise?: Promise<string>;
}

export function useTTS(): UseTTSReturn {
    const [isSpeaking, setIsSpeaking] = useState(false);
    const audioRef = useRef<HTMLAudioElement | null>(null);
    const queueRef = useRef<QueueItem[]>([]);
    const processingRef = useRef(false);

    const stop = useCallback(() => {
        // Clear queue
        queueRef.current.forEach(item => {
            // If we have a resolved URL, revoke it to prevent memory leaks
            if (item.audioUrl) {
                URL.revokeObjectURL(item.audioUrl);
            }
            item.resolve(); // Resolve pending promises to unblock
        });
        queueRef.current = [];
        
        // Stop current audio
        if (audioRef.current) {
            audioRef.current.pause();
            audioRef.current.currentTime = 0;
            audioRef.current = null;
        }
        
        processingRef.current = false;
        setIsSpeaking(false);
    }, []);

    const fetchAudio = useCallback(async (text: string, voice: string): Promise<string> => {
        if (!text) return '';

        try {
            const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || '';
            
            const response = await fetch(`${apiBaseUrl}/v1/speech`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({
                    text,
                    voice,
                    provider: 'aliyun',
                    model: 'cosyvoice-v3-flash'
                }),
                // Prevent chunked_data_pipe_upload_data_stream error
                keepalive: true, 
            });

            if (!response.ok) {
                throw new Error(`TTS failed: ${response.statusText}`);
            }

            const blob = await response.blob();
            const url = URL.createObjectURL(blob);
            return url;

        } catch (error) {
            console.error('TTS Fetch error:', error);
            throw error;
        }
    }, []);

    const playAudio = useCallback(async (url: string) => {
        return new Promise<void>((resolve, reject) => {
            const audio = new Audio(url);
            audioRef.current = audio;

            audio.onended = () => {
                URL.revokeObjectURL(url);
                audioRef.current = null;
                resolve();
            };

            audio.onpause = () => {
                // Determine if this was a manual pause/stop or just end
                // Usually for our case, pause means stop was called or we are done
                // We'll let stop() handle the actual cleanup/reject if needed
                // But if it just paused, we treat it as done for this segment
                resolve();
            };

            audio.onerror = (e) => {
                console.error('Audio playback error', e);
                URL.revokeObjectURL(url);
                toast.error('语音播放失败');
                reject(e);
            };

            audio.play().catch(e => {
                // Auto-play policy or other error
                if (e.name !== 'AbortError') {
                    console.error('Play error', e);
                }
                reject(e);
            });
        });
    }, []);

    const processQueue = useCallback(async () => {
        if (processingRef.current || queueRef.current.length === 0) return;

        processingRef.current = true;
        setIsSpeaking(true);

        try {
            while (queueRef.current.length > 0) {
                const item = queueRef.current[0]; // Peek current
                
                // Double check if stopped (queue might be cleared)
                if (!item) break;

                try {
                    // 1. Ensure current item is fetched
                    if (!item.audioUrl && !item.fetchPromise) {
                        item.fetchPromise = fetchAudio(item.text, item.voice)
                            .then(url => { item.audioUrl = url; return url; });
                    }

                    // 2. PREFETCH NEXT: Trigger fetch for the *next* item immediately
                    if (queueRef.current.length > 1) {
                        const nextItem = queueRef.current[1];
                        if (!nextItem.audioUrl && !nextItem.fetchPromise) {
                            // Start fetching next item in background
                            nextItem.fetchPromise = fetchAudio(nextItem.text, nextItem.voice)
                                .then(url => { nextItem.audioUrl = url; return url; })
                                .catch(e => {
                                    console.error('Prefetch failed for next item', e);
                                    // We don't reject the item here, we let it fail when it tries to play
                                    // or handle it gracefully then
                                    return ''; 
                                });
                        }
                    }

                    // 3. Wait for current item audio to be ready
                    const url = item.audioUrl || await item.fetchPromise;

                    // Check if queue was cleared while waiting
                    if (queueRef.current.length === 0 || queueRef.current[0] !== item) {
                        if (url) URL.revokeObjectURL(url); // Cleanup if we won't play
                        break;
                    }

                    // 4. Play current
                    if (url) {
                        await playAudio(url);
                    }
                    
                    item.resolve();

                } catch (e) {
                    console.error('Error processing TTS item', e);
                    item.reject(e);
                } finally {
                    // Remove current from queue
                    if (queueRef.current.length > 0 && queueRef.current[0] === item) {
                        queueRef.current.shift();
                    }
                }
            }
        } finally {
            processingRef.current = false;
            setIsSpeaking(false);
        }
    }, [fetchAudio, playAudio]);

    const speak = useCallback(async (text: string, voice: string = 'longanhuan', mode: 'interrupt' | 'append' = 'interrupt') => {
        if (mode === 'interrupt') {
            stop();
        }

        return new Promise<void>((resolve, reject) => {
            const newItem: QueueItem = { text, voice, resolve, reject };
            queueRef.current.push(newItem);
            processQueue();
        });
    }, [stop, processQueue]);

    // Cleanup
    useEffect(() => {
        return () => {
            stop();
        };
    }, [stop]);

    return {
        speak,
        stop,
        isSpeaking
    };
}

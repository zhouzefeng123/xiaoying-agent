import { useState, useEffect } from 'react';
import { wakeWordService } from '../services/wakeWordService';
import { toast } from 'sonner';

interface UseWakeWordProps {
    onWakeWord?: () => void;
    isEnabled: boolean;
}

export function useWakeWord({ onWakeWord, isEnabled }: UseWakeWordProps) {
    const [isLoaded, setIsLoaded] = useState(false);
    const [state, setState] = useState<WakeWordRuntimeState>(wakeWordService.getState());
    const [error, setError] = useState<Error | null>(null);

    useEffect(() => {
        const init = async () => {
            try {
                await wakeWordService.init();
                setIsLoaded(true);
            } catch (e) {
                console.error('[WakeWord] Init failed:', e);
                setError(e instanceof Error ? e : new Error('Wake word init failed'));
            }
        };
        init();

        const unsubscribe = wakeWordService.subscribe((nextState) => {
            setState(nextState);
        });

        return () => {
            unsubscribe();
        };
    }, []);

    useEffect(() => {
        wakeWordService.setDetectionHandler(onWakeWord);
        return () => {
            wakeWordService.setDetectionHandler();
        };
    }, [onWakeWord]);

    useEffect(() => {
        if (!isLoaded) return;

        const start = async () => {
            try {
                const nextState = await wakeWordService.startListening();
                if (!nextState.available && nextState.error) {
                    toast.error(nextState.error);
                }
            } catch (e) {
                console.error('[WakeWord] Start failed:', e);
                setError(e instanceof Error ? e : new Error('Wake word start failed'));
                toast.error('Wake word listener failed to start');
            }
        };

        const stop = async () => {
            try {
                await wakeWordService.stopListening();
            } catch (e) {
                console.error('[WakeWord] Stop failed:', e);
            }
        };

        if (isEnabled && !state.listening) {
            start();
        } else if (!isEnabled && state.listening) {
            stop();
        }
    }, [isEnabled, isLoaded, state.listening]);

    return {
        isLoaded,
        hasModel: state.available,
        wakeWordError: state.error || error?.message || null,
        isListening: state.listening,
        error
    };
}

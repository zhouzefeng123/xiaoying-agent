import { useState, useEffect } from 'react';
import { wsClient } from '../lib/ws';

export type ConnectionStatus = 'connected' | 'disconnected' | 'connecting';

export function useConnectionStatus() {
    const [status, setStatus] = useState<ConnectionStatus>('disconnected');

    useEffect(() => {
        const unsubscribe = wsClient.addConnectionListener((newStatus) => {
            setStatus(newStatus as ConnectionStatus);
        });
        return unsubscribe;
    }, []);

    return status;
}

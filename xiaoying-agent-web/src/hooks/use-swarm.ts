import { useState, useEffect, useCallback, useRef } from 'react';
import { toast } from 'sonner';

export interface SwarmAgent {
    id: string;
    role: string;
}

export interface ToolCall {
    id: string;
    name: string;
    arguments: string;
}

export interface ToolResult {
    tool_call_id: string;
    content: string;
}

export interface MultiAgentEvent {
    Type: 'message' | 'tool_call' | 'tool_result' | 'thought' | 'spawn' | 'error';
    From: string;
    To: string;
    Content: string;
    ToolCalls?: ToolCall[];
    ToolResult?: ToolResult;
    Timestamp: number;
}

export function useSwarm() {
    const [events, setEvents] = useState<MultiAgentEvent[]>([]);
    const [agents, setAgents] = useState<SwarmAgent[]>([]);
    const [isConnected, setIsConnected] = useState(false);
    const wsRef = useRef<WebSocket | null>(null);

    // Connect to WebSocket on mount
    useEffect(() => {
        const connect = () => {
            const ws = new WebSocket(`${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/v1/swarm/events`);

            ws.onopen = () => {
                setIsConnected(true);
                console.log('Swarm WebSocket connected');
            };

            ws.onclose = () => {
                setIsConnected(false);
                console.log('Swarm WebSocket disconnected, retrying in 3s...');
                setTimeout(connect, 3000);
            };

            ws.onerror = (err) => {
                console.error('Swarm WebSocket error:', err);
                ws.close();
            };

            ws.onmessage = (msg) => {
                try {
                    const event = JSON.parse(msg.data) as MultiAgentEvent;
                    setEvents(prev => [...prev, event]);
                    
                    // If it's a spawn event, update agents list
                    if (event.Type === 'spawn') {
                        const match = event.Content.match(/Agent spawned:\s*(\S+)/);
                        const id = match ? match[1] : event.From;
                        if (id) {
                            setAgents(prev => prev.some(a => a.id === id) ? prev : [...prev, { id, role: 'worker' }]);
                        }
                    }
                } catch (e) {
                    console.error('Failed to parse swarm event:', e);
                }
            };

            wsRef.current = ws;
        };

        connect();

        return () => {
            if (wsRef.current) {
                wsRef.current.close();
            }
        };
    }, []);

    // We don't expose spawnAgent anymore as frontend shouldn't spawn agents manually.
    // The backend handles it. But we keep the type for events processing.
    const spawnAgent = useCallback(async (role: string, systemPrompt: string) => {
         void role;
         void systemPrompt;
         // Deprecated client-side spawn. Kept for type compatibility if needed or removed.
         console.warn("Client-side spawn is deprecated. Use backend /execute endpoint.");
         return "";
    }, []);

    const sendMessage = useCallback(async (to: string, content: string) => {
        // This is for manual messaging, but we primarily use /execute now.
        try {
            const res = await fetch('/v1/swarm/message', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ to, content })
            });

            if (!res.ok) throw new Error('Failed to send message');
        } catch (error) {
            console.error(error);
            toast.error('Failed to send message');
            throw error;
        }
    }, []);

    const broadcast = useCallback((content: string) => {
        // Since we don't have a direct broadcast endpoint in some versions,
        // we can implement it by sending to '*' if backend supports it, 
        // or just 'broadcast' endpoint.
        // Let's assume there is a broadcast endpoint or we use send to '*'
        
        return fetch('/v1/swarm/broadcast', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ content })
        }).then(res => {
            if (!res.ok) throw new Error('Failed to broadcast');
        });
    }, []);

    return {
        events,
        agents,
        isConnected,
        spawnAgent,
        sendMessage,
        broadcast
    };
}

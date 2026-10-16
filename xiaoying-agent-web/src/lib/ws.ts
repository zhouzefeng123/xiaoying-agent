import { toast } from 'sonner';

export type EventType = 
    | 'token' 
    | 'tool_call' 
    | 'tool_result' 
    | 'tool_output' 
    | 'error' 
    | 'done' 
    | 'ping'
    | 'permission_request'
    | 'notification'
    | 'usage'
    | 'thought'
    | 'git_status_update'
    | 'split'
    | 'code_stream'
    | 'progress'
    | 'config_updated'
    | 'context_stats'
    | 'plan_snapshot'
    | 'read_only_updated'
    | 'word_editor_action';

export interface ContextStatsSection {
    key: string;
    label: string;
    tokens: number;
}

export interface ContextAssemblyReport {
    critical_cache?: string;
    dynamic_cache?: string;
    history_window_source?: string;
    history_window_fallback_reason?: string;
    history_window_messages?: number;
    memory_capsule?: boolean;
    turn_context?: boolean;
    capsule_memory_count?: number;
    turn_memory_count?: number;
    warm_context_source?: string;
    warm_context_session_id?: string;
    warm_context_messages?: number;
    warm_context_status?: string;
    implicit_global_fallback_skipped?: boolean;
    working_state?: boolean;
    mutation_journal?: boolean;
    dynamic_suffix?: boolean;
}

export interface ContextStats {
    source: 'estimate' | 'provider' | string;
    confidence?: string;
    model?: string;
    current_tokens: number;
    estimated_tokens?: number;
    observed_prompt_tokens?: number;
    context_window_tokens?: number;
    max_tokens: number;
    hard_limit_tokens?: number;
    soft_limit_tokens?: number;
    response_reserve_tokens?: number;
    remaining_tokens?: number;
    message_count?: number;
    step_count?: number;
    sections?: ContextStatsSection[];
    assembly?: ContextAssemblyReport;
}

export interface ServerEvent {
    type: EventType;
    content?: string;
    message_id?: string;
    session_id?: string;
    tool_execution_id?: string; // Phase 5
    data?: any;
    tool_result?: {
        tool_call_id: string;
        content: string;
        is_error: boolean;
        metadata?: any;
    };
    usage?: {
        input_tokens: number;
        output_tokens: number;
        total_tokens: number;
    };
}

export type ClientMessageType = 'chat' | 'permission_response' | 'stop' | 'cancel_tool' | 'set_read_only';

export interface ClientMessage {
    type: ClientMessageType;
    session_id?: string;
    tool_execution_id?: string; // Phase 5
    content?: string;
    attachments?: { FilePath: string; FileName: string; MimeType: string; Content?: number[] }[];
    enable_subagents?: boolean;
    model?: string;
    mode?: string;
    read_only_mode?: boolean;
    permission_id?: string;
    action?: 'grant' | 'deny';
}

type EventHandler = (event: ServerEvent) => void;
type ConnectionStatus = 'connected' | 'disconnected' | 'connecting';
type ConnectionHandler = (status: ConnectionStatus) => void;

class WebSocketClient {
    private url: string;
    private ws: WebSocket | null = null;
    private listeners: EventHandler[] = [];
    private connectionListeners: ConnectionHandler[] = [];
    private status: ConnectionStatus = 'disconnected';
    private reconnectTimeout: ReturnType<typeof setTimeout> | null = null;
    
    constructor(url: string = `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/v1/ws`) {
        this.url = url;
    }

    connect() {
        if (this.ws?.readyState === WebSocket.OPEN || this.ws?.readyState === WebSocket.CONNECTING) return;

        console.log('Connecting to WebSocket...', this.url);
        this.notifyConnectionListeners('connecting');
        this.ws = new WebSocket(this.url);

        this.ws.onopen = () => {
            console.log('WebSocket connected');
            this.notifyConnectionListeners('connected');
            // toast.success('Connected to server');
        };

        this.ws.onmessage = (event) => {
            try {
                const data: ServerEvent = JSON.parse(event.data);
                this.notifyListeners(data);
            } catch (e) {
                console.error('Failed to parse WebSocket message', e);
            }
        };

        this.ws.onclose = () => {
            console.log('WebSocket disconnected');
            this.notifyConnectionListeners('disconnected');
            // toast.error('Disconnected from server');
            this.scheduleReconnect();
        };

        this.ws.onerror = (error) => {
            console.error('WebSocket error', error);
            this.ws?.close();
        };
    }

    private scheduleReconnect() {
        if (this.reconnectTimeout) clearTimeout(this.reconnectTimeout);
        this.reconnectTimeout = setTimeout(() => {
            console.log('Attempting to reconnect...');
            this.connect();
        }, 3000);
    }

    subscribe(handler: EventHandler) {
        this.listeners.push(handler);
        return () => {
            this.listeners = this.listeners.filter(h => h !== handler);
        };
    }

    addConnectionListener(handler: ConnectionHandler) {
        this.connectionListeners.push(handler);
        handler(this.status);
        return () => {
            this.connectionListeners = this.connectionListeners.filter(h => h !== handler);
        };
    }

    private notifyConnectionListeners(status: ConnectionStatus) {
        this.status = status;
        this.connectionListeners.forEach(handler => handler(status));
    }

    private notifyListeners(event: ServerEvent) {
        this.listeners.forEach(handler => handler(event));
    }

    send(message: ClientMessage) {
        if (this.ws?.readyState === WebSocket.OPEN) {
            this.ws.send(JSON.stringify(message));
        } else {
            const error = new Error('WebSocket is not open, cannot send message');
            console.warn(error.message);
            toast.error('连接丢失。请稍候...');
            throw error;
        }
    }

    disconnect() {
        if (this.reconnectTimeout) clearTimeout(this.reconnectTimeout);
        this.ws?.close();
        this.ws = null;
    }
}

export const wsClient = new WebSocketClient();

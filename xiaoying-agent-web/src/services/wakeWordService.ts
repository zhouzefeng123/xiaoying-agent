type WakeWordStateListener = (state: WakeWordRuntimeState) => void;

const DEFAULT_WAKE_WORD_STATE: WakeWordRuntimeState = {
    available: false,
    ready: false,
    listening: false,
    keyword: '你好小英',
    error: null,
};

export class WakeWordService {
    private static instance: WakeWordService;
    private state: WakeWordRuntimeState = { ...DEFAULT_WAKE_WORD_STATE };
    private listeners = new Set<WakeWordStateListener>();
    private detectionHandler: (() => void) | null = null;
    private eventsBound = false;

    private constructor() {}

    static getInstance(): WakeWordService {
        if (!WakeWordService.instance) {
            WakeWordService.instance = new WakeWordService();
        }
        return WakeWordService.instance;
    }

    private get bridge() {
        return window.electron?.wakeWord;
    }

    private notifyListeners() {
        const snapshot = { ...this.state };
        this.listeners.forEach((listener) => listener(snapshot));
    }

    private bindBridgeEvents() {
        if (this.eventsBound || !this.bridge) {
            return;
        }

        this.bridge.onStateChanged((state) => {
            this.state = state;
            this.notifyListeners();
        });

        this.bridge.onDetected(() => {
            this.detectionHandler?.();
        });

        this.eventsBound = true;
    }

    subscribe(listener: WakeWordStateListener) {
        this.listeners.add(listener);
        listener({ ...this.state });

        return () => {
            this.listeners.delete(listener);
        };
    }

    setDetectionHandler(handler?: () => void) {
        this.detectionHandler = handler || null;
    }

    async init() {
        if (!this.bridge) {
            this.state = {
                ...DEFAULT_WAKE_WORD_STATE,
                error: '当前环境不支持固定唤醒词',
            };
            this.notifyListeners();
            return this.state;
        }

        this.bindBridgeEvents();
        this.state = await this.bridge.init();
        this.notifyListeners();
        return this.state;
    }

    async startListening() {
        if (!this.bridge) {
            return this.state;
        }

        this.bindBridgeEvents();
        this.state = await this.bridge.start();
        this.notifyListeners();
        return this.state;
    }

    async stopListening() {
        if (!this.bridge) {
            return this.state;
        }

        this.state = await this.bridge.stop();
        this.notifyListeners();
        return this.state;
    }

    getState() {
        return { ...this.state };
    }

    hasWakeWordModel() {
        return this.state.available;
    }
}

export const wakeWordService = WakeWordService.getInstance();

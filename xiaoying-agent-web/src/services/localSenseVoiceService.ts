const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || '';

export interface LocalSenseVoiceStatus {
    ready: boolean;
    binaryFound: boolean;
    modelFound: boolean;
    tokensFound: boolean;
    binaryPath?: string;
    modelPath?: string;
    tokensPath?: string;
    binaryError?: string;
    modelError?: string;
    tokensError?: string;
    platform: string;
    runtimeName: string;
    runtimeVersion: string;
    modelName: string;
    modelRelease: string;
    modelFileName: string;
    tokensFileName: string;
    language: string;
    useITN: boolean;
}

async function parseResponse<T>(response: Response): Promise<T> {
    if (!response.ok) {
        const message = await response.text();
        throw new Error(message || '请求失败');
    }
    return response.json() as Promise<T>;
}

export const localSenseVoiceService = {
    async getStatus(): Promise<LocalSenseVoiceStatus> {
        const response = await fetch(`${apiBaseUrl}/v1/stt/local-sensevoice/status`);
        return parseResponse<LocalSenseVoiceStatus>(response);
    }
};
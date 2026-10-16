const BACKEND_HEALTH_URL = '/health';
const DEFAULT_INITIAL_DELAY_MS = 1200;
const DEFAULT_POLL_INTERVAL_MS = 1000;
const DEFAULT_TIMEOUT_MS = 30000;

const delay = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

interface WaitForBackendRecoveryOptions {
    initialDelayMs?: number;
    pollIntervalMs?: number;
    timeoutMs?: number;
}

export async function waitForBackendRecovery(options: WaitForBackendRecoveryOptions = {}) {
    const initialDelayMs = options.initialDelayMs ?? DEFAULT_INITIAL_DELAY_MS;
    const pollIntervalMs = options.pollIntervalMs ?? DEFAULT_POLL_INTERVAL_MS;
    const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
    const deadline = Date.now() + timeoutMs;

    let sawUnavailable = false;
    let healthyStreak = 0;

    await delay(initialDelayMs);

    while (Date.now() < deadline) {
        try {
            const response = await fetch(BACKEND_HEALTH_URL, {
                cache: 'no-store',
                headers: {
                    'Cache-Control': 'no-cache'
                }
            });

            if (response.ok) {
                const body = (await response.text()).trim().toLowerCase();
                if (body === 'ok') {
                    healthyStreak += 1;
                    if ((sawUnavailable && healthyStreak >= 1) || healthyStreak >= 2) {
                        return;
                    }
                } else {
                    healthyStreak = 0;
                }
            } else {
                sawUnavailable = true;
                healthyStreak = 0;
            }
        } catch {
            sawUnavailable = true;
            healthyStreak = 0;
        }

        await delay(pollIntervalMs);
    }

    throw new Error('BACKEND_RECOVERY_TIMEOUT');
}
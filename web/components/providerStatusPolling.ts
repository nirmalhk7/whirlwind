import { api } from "./controlApi";
import type { ProviderStatusSnapshot } from "./useProviderStatus";
type SnapshotListener = (snapshot: ProviderStatusSnapshot) => void;
type ErrorListener = (message: string) => void;
class ProviderStatusPolling {
    private active = true;
    private timer?: ReturnType<typeof setTimeout>;
    private interval = 30;
    constructor(private onSnapshot: SnapshotListener, private onError: ErrorListener) { }
    async check() {
        try {
            const next = await api<ProviderStatusSnapshot>("provider-status");
            if (!this.active)
                return;
            this.accept(next);
        }
        catch (reason) {
            this.reportError(reason);
        }
        finally {
            this.schedule();
        }
    }
    private accept(next: ProviderStatusSnapshot) {
        if (!Array.isArray(next.providers))
            throw new Error("Provider status unavailable. Retry on the next check.");
        this.interval = pollingInterval(next.interval_seconds);
        this.onSnapshot(next);
        this.onError("");
    }
    private reportError(reason: unknown) {
        if (!this.active)
            return;
        this.onError(reason instanceof Error ? reason.message : "Provider status unavailable.");
    }
    private schedule() {
        if (this.active)
            this.timer = setTimeout(() => void this.check(), this.interval * 1000);
    }
    stop() {
        this.active = false;
        clearTimeout(this.timer);
    }
}
function pollingInterval(seconds: number) {
    if (!Number.isFinite(seconds) || seconds <= 0)
        return 30;
    return seconds;
}
export function startProviderStatusPolling(onSnapshot: SnapshotListener, onError: ErrorListener) {
    const polling = new ProviderStatusPolling(onSnapshot, onError);
    void polling.check();
    return () => polling.stop();
}

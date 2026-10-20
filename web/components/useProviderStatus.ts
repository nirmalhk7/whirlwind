import { useEffect, useState } from "react";
import { startProviderStatusPolling } from "./providerStatusPolling";
export type ProviderStatusEntry = {
    id: string;
    name: string;
    kind: string;
    status: string;
    detail: string;
    checked_at?: string;
};
export type ProviderStatusSnapshot = {
    providers: ProviderStatusEntry[];
    interval_seconds: number;
    stopped: boolean;
};
export function useProviderStatus(enabled: boolean, stopped: boolean | null) {
    const [snapshot, setSnapshot] = useState<ProviderStatusSnapshot | null>(null);
    const [error, setError] = useState("");
    useEffect(() => {
        if (!enabled)
            return;
        setSnapshot(null);
        setError("");
        return startProviderStatusPolling(setSnapshot, setError);
    }, [enabled, stopped]);
    return { snapshot, error };
}

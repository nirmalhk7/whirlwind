import type {Page} from '@playwright/test';
export async function installEventSource(page:Page){await page.addInitScript(() => {
        const listeners = new Map<string, Array<() => void>>();
        const source = {
            addEventListener(type: string, listener: () => void) {
                listeners.set(type, [...(listeners.get(type) ?? []), listener]);
            },
            close() { },
            emit(type: string) { for (const listener of listeners.get(type) ?? [])
                listener(); },
        };
        Object.defineProperty(window, "EventSource", { configurable: true, value: class {
                close() { }
                addEventListener = source.addEventListener;
            } });
        (window as any).__whirlwindEventSource = source;
    });
}

"use client";

import { createContext, ReactNode, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";

type ToastKind = "success" | "error";
type ToastItem = { id: number; kind: ToastKind; message: string };
type ToastApi = { success: (message: string) => void; error: (message: string) => void };

const ToastContext = createContext<ToastApi>({ success: () => undefined, error: () => undefined });

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<ToastItem | null>(null);
  const nextId = useRef(0);
  const timeout = useRef<ReturnType<typeof setTimeout> | null>(null);

  const show = useCallback((kind: ToastKind, message: string) => {
    if (timeout.current) clearTimeout(timeout.current);
    setToast({ id: ++nextId.current, kind, message });
    timeout.current = setTimeout(() => setToast(null), 4500);
  }, []);

  useEffect(() => () => {
    if (timeout.current) clearTimeout(timeout.current);
  }, []);

  const api = useMemo(() => ({
    success: (message: string) => show("success", message),
    error: (message: string) => show("error", message),
  }), [show]);

  return <ToastContext.Provider value={api}>
    {children}
    <div className="toast-viewport" aria-live="polite" aria-atomic="true">
      {toast && <div key={toast.id} role={toast.kind === "error" ? "alert" : "status"} className={`toast toast-${toast.kind}`}>
        {toast.message}
      </div>}
    </div>
  </ToastContext.Provider>;
}

export function useToast() {
  return useContext(ToastContext);
}

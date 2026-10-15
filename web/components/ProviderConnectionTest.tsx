"use client";

import { useEffect, useState } from "react";
import { api } from "./controlApi";

type ProviderEntry = { id: string; version: number; data: { name: string } };
type Resource<T> = { data: T };

type ProviderConnectionResult = {
  stage: "running" | "complete" | "failed";
  text?: string;
  error?: string;
  elapsed_ms?: number;
};

function failureMessage(reason: unknown, fallback: string) {
  return reason instanceof Error ? reason.message : fallback;
}

function checkDeadline(deadline: number) {
  if (Date.now() >= deadline) throw new Error("Connection test timed out. Check agent setup and retry.");
}

function pollConnection(operationID: string, setResult: (result: ProviderConnectionResult) => void, onComplete: () => void) {
  let cancelled = false;
  let timer: ReturnType<typeof setTimeout> | null = null;
  const deadline = Date.now() + 75_000;
  const poll = async () => {
    try {
      const operation = await api<Resource<ProviderConnectionResult>>(`operations/${encodeURIComponent(operationID)}`);
      if (cancelled) return;
      setResult(operation.data);
      if (operation.data.stage === "running") {
        checkDeadline(deadline);
        timer = setTimeout(() => void poll(), 750);
      } else {
        onComplete();
      }
    } catch (reason) {
      if (cancelled) return;
      setResult({ stage: "failed", error: failureMessage(reason, "Could not read connection result. Try again.") });
      onComplete();
    }
  };
  void poll();
  return () => {
    cancelled = true;
    if (timer) clearTimeout(timer);
  };
}

function ConnectionResult({ result }: { result: ProviderConnectionResult | null }) {
  return (
      <div aria-live="polite" aria-busy={result?.stage === "running"}>
        {result?.stage === "running" && <p role="status" className="muted">Waiting for agent reply…</p>}
        {result?.stage === "complete" && (
          <>
            <p role="status" className="provider-connection-success">Connected{result.elapsed_ms !== undefined ? ` · ${(result.elapsed_ms / 1000).toFixed(1)}s` : ""}</p>
            <pre aria-label="Agent reply">{result.text}</pre>
          </>
        )}
        {result?.stage === "failed" && <p className="error" role="alert">Connection failed: {result.error} Check agent setup and try again.</p>}
      </div>
  );
}

function useConnectionTest(entry: ProviderEntry) {
  const [result, setResult] = useState<ProviderConnectionResult | null>(null);
  const [operationID, setOperationID] = useState<string | null>(null);

  useEffect(() => {
    if (!operationID) return;
    return pollConnection(operationID, setResult, () => setOperationID(null));
  }, [operationID]);

  const test = async () => {
    setResult({ stage: "running" });
    try {
      const queued = await api<{ operation_id: string }>(`providers/${encodeURIComponent(entry.id)}/test`, "POST", {}, entry.version);
      setOperationID(queued.operation_id);
    } catch (reason) {
      setResult({ stage: "failed", error: failureMessage(reason, "Connection test failed. Check agent setup and retry.") });
    }
  };

  return { result, test };
}

export function ProviderConnectionTest({ entry, disabled }: { entry: ProviderEntry; disabled: boolean }) {
  const { result, test } = useConnectionTest(entry);

  return (
    <div className="provider-connection" role="group" aria-label={`${entry.data.name} connection test`}>
      <div className="whirlwind-actions">
        <button type="button" className="button" disabled={disabled || result?.stage === "running"} onClick={() => void test()}>
          {result?.stage === "running" ? "Testing…" : "Test connection"}
        </button>
        <span className="muted">Sends “Hello World” using this provider.</span>
      </div>
      <ConnectionResult result={result} />
    </div>
  );
}

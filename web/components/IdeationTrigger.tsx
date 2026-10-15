"use client";

import { useEffect, useState } from "react";
import { api } from "./controlApi";

type Result = { stage: "running" | "complete" | "failed"; error?: string };

function pollIdeation(operation: string, setResult: (result: Result) => void, onComplete: () => Promise<void>, onError: () => void) {
  let cancelled = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const poll = async () => {
    try {
      const record = await api<{ data: Result }>(`operations/${encodeURIComponent(operation)}`);
      if (cancelled) return;
      setResult(record.data);
      if (record.data.stage === "running") timer = setTimeout(() => void poll(), 1000);
      else await onComplete();
    } catch (reason) {
      if (!cancelled) {
        setResult({ stage: "failed", error: reason instanceof Error ? reason.message : "Could not read ideation status" });
        onError();
      }
    }
  };
  void poll();
  return () => { cancelled = true; clearTimeout(timer); };
}

export function IdeationTrigger({ projectID, projectName, onComplete }: { projectID: string; projectName: string; onComplete: () => Promise<void> }) {
  const [result, setResult] = useState<Result | null>(null);
  const [operation, setOperation] = useState<string | null>(null);
  useEffect(() => {
    if (!operation) return;
    return pollIdeation(operation, setResult, async () => {
      setOperation(null);
      await onComplete();
    }, () => setOperation(null));
  }, [operation, onComplete]);
  const run = async () => {
    setResult({ stage: "running" });
    try {
      const queued = await api<{ operation_id: string }>(`projects/${encodeURIComponent(projectID)}/ideation`, "POST");
      setOperation(queued.operation_id);
    } catch (reason) {
      setResult({ stage: "failed", error: reason instanceof Error ? reason.message : "Could not start ideation" });
    }
  };
  return <div>
    <button className="button" aria-label={`Run ideation for ${projectName}`} disabled={result?.stage === "running"} onClick={() => void run()}>{result?.stage === "running" ? "Running ideation…" : "Run Ideation"}</button>
    {result?.stage === "running" && <p role="status">Generating directions…</p>}
    {result?.stage === "complete" && <p role="status">Ideation complete. Directions ready.</p>}
    {result?.stage === "failed" && <p role="alert" className="error">Ideation failed: {result.error}</p>}
  </div>;
}

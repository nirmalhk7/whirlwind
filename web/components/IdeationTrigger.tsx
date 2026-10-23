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

export function IdeationTrigger({ projectID, projectName, onComplete, disabled=false }: { projectID: string; projectName: string; onComplete: () => Promise<void>;disabled?:boolean }) {
  const [goal,setGoal]=useState("");
  const [constraints,setConstraints]=useState("");
  const [priorities,setPriorities]=useState("");
  const [maxCandidates,setMaxCandidates]=useState(4);
  const [fresh,setFresh]=useState(false);
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
      const queued = await api<{ operation_id: string }>(`projects/${encodeURIComponent(projectID)}/ideation`, "POST",{goal,constraints,priorities,max_candidates:maxCandidates,fresh});
      setOperation(queued.operation_id);
    } catch (reason) {
      setResult({ stage: "failed", error: reason instanceof Error ? reason.message : "Could not start ideation" });
    }
  };
  return <div>
    <label>Ideation goal<input className="input" value={goal} maxLength={4000} onChange={event=>setGoal(event.target.value)} placeholder="What would make this project more useful?" /></label>
    <label>Constraints<textarea className="input" value={constraints} maxLength={8000} onChange={event=>setConstraints(event.target.value)} placeholder="Priorities, limits, and work to avoid" /></label>
    <label>Priorities<input className="input" value={priorities} maxLength={4000} onChange={event=>setPriorities(event.target.value)} placeholder="For example: fix recovery before adding features" /></label>
    <label>Maximum candidates<input className="input" type="number" min={1} max={12} value={maxCandidates} onChange={event=>setMaxCandidates(Number(event.target.value))}/></label>
    <label><input type="checkbox" checked={fresh} onChange={event=>setFresh(event.target.checked)}/> Explore unchanged evidence again for this run</label>
    <p className="muted">Ideas remain suggestions until you select one. Planning and implementation then proceed within its scope and project policy.</p>
    <button className="button" aria-label={`Run ideation for ${projectName}`} disabled={disabled||result?.stage === "running"} onClick={() => void run()}>{result?.stage === "running" ? "Running ideation…" : "Run Ideation"}</button>
    {result?.stage === "running" && <p role="status">Generating directions…</p>}
    {result?.stage === "complete" && <p role="status">Ideation complete. Directions ready.</p>}
    {result?.stage === "failed" && <p role="alert" className="error">Ideation failed: {result.error}</p>}
  </div>;
}

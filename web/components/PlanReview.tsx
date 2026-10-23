"use client";

import { useCallback, useEffect, useState } from "react";
import { api } from "./controlApi";
import { PlanMarkdown } from "./PlanMarkdown";

type PlanDocument = {
  path: string;
  hash: string;
  content: string;
  plan: unknown;
  approved_hash?: string;
};
type Props = {
  id: string;
  version: number;
  stage: string;
  busy: boolean;
  perform: (action: () => Promise<unknown>, message: string) => Promise<void>;
};
type Decision = "approve" | "revise" | "reject";
const decisionMessages = { approve: "Plan approved; coding authorized", revise: "Plan revision requested", reject: "Plan rejected" };

function planError(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

async function readDocument(path: string): Promise<PlanDocument> {
  const value = await api<PlanDocument>(`${path}/plan-document`);
  const complete = value && typeof value.content === "string" && value.hash && value.path;
  if (!complete) throw new Error("Plan document is incomplete. Reload the plan before reviewing it.");
  return value;
}

function usePlanDocument(path: string, version: number, refresh: number) {
  const [document, setDocument] = useState<PlanDocument | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  useEffect(() => { setDocument(null); }, [path]);
  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    void readDocument(path).then(value => {
      if (active) setDocument(value);
    }).catch(reason => {
      if (active) setError(planError(reason, "Plan document could not be read. Reload the plan to try again."));
    }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [path, version, refresh]);
  return { document, loading, error, setError };
}

type DecisionRequest = { path: string; version: number; hash: string; action: Decision; feedback: string };
async function saveDecision(request: DecisionRequest, setError: (value: string) => void) {
  const { path, version, hash, action, feedback } = request;
  try {
    return await api(`${path}/plan-review`, "POST", {
      action, hash, ...(action === "revise" ? { feedback: feedback.trim() } : {}),
    }, version);
  } catch (reason) {
    setError(planError(reason, "Plan decision could not be saved. Your document and feedback are preserved."));
    throw reason;
  }
}

function PlanContent({ document }: { document: PlanDocument }) {
  const changed = document.approved_hash && document.approved_hash !== document.hash;
  return <>
    <p>Plan file: <code>{document.path}</code></p>
    <PlanMarkdown content={document.content} />
    <details><summary>Normalized execution plan</summary><pre>{JSON.stringify(document.plan, null, 2)}</pre></details>
    {changed && <p className="muted">Document changed since approval. Review and approve this version before coding continues.</p>}
  </>;
}

type ActionsProps = {
  feedback: string;
  disabled: boolean;
  setFeedback: (value: string) => void;
  decide: (action: Decision) => Promise<void>;
};
function PlanActions({ feedback, disabled, setFeedback, decide }: ActionsProps) {
  return <>
    <label className="whirlwind-form">Revision feedback<textarea className="input" value={feedback} maxLength={8000} disabled={disabled} onChange={event => setFeedback(event.target.value)} /></label>
    <div className="whirlwind-actions">
      <button className="button primary" type="button" disabled={disabled} onClick={() => void decide("approve")}>Approve plan</button>
      <button className="button" type="button" disabled={disabled || !feedback.trim()} onClick={() => void decide("revise")}>Request revision</button>
      <button className="button" type="button" disabled={disabled} onClick={() => void decide("reject")}>Reject plan</button>
    </div>
  </>;
}

export function PlanReview({ id, version, stage, busy, perform }: Props) {
  const [feedback, setFeedback] = useState("");
  const [refresh, setRefresh] = useState(0);
  const path = `tasks/${encodeURIComponent(id)}`;
  const { document, loading, error, setError } = usePlanDocument(path, version, refresh);
  useEffect(() => { setFeedback(""); }, [id]);
  const disabled = busy || loading;
  const decide = useCallback(async (action: Decision) => {
    if (!document || disabled || stage !== "plan_approval") return;
    setError("");
    await perform(() => saveDecision({ path, version, hash: document.hash, action, feedback }, setError), decisionMessages[action]);
  }, [document, disabled, stage, perform, path, feedback, version, setError]);
  return <section className="list" aria-label="Local plan review">
    <div className="row"><h3>Local specification</h3><button className="button" type="button" disabled={disabled} onClick={() => setRefresh(value => value + 1)}>Reload plan</button></div>
    <p className="muted">Review the current document before approving coding. Manual file edits require reviewing the new version.</p>
    {loading && <p role="status">Reading plan document…</p>}
    {error && <p className="error" role="alert">{error}</p>}
    {document && <PlanContent document={document} />}
    {document && stage === "plan_approval" && <PlanActions feedback={feedback} disabled={disabled} setFeedback={setFeedback} decide={decide} />}
  </section>;
}

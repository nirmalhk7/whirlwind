"use client";

import { useEffect, useState } from "react";

type Step = { id: string; objective: string; acceptance: string[]; depends_on: string[]; expected_outcome: string; status: string };
type Campaign = {
  id: string; version: number; project: string; status: string; objective: string; acceptance: string[];
  steps: Step[]; max_steps: number; max_runtime_seconds: number; max_external_items: number;
  approved_digest?: string | null; stop_reason?: string | null;
};
type CampaignDecision = { action: "ready" | "wait" | "completed" | "stop"; step_id?: string | null; reason: string };

export function ProjectCampaigns({ project }: { project: string }) {
  const [items, setItems] = useState<Campaign[]>([]);
  const [objective, setObjective] = useState("");
  const [acceptance, setAcceptance] = useState("");
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [objectiveDrafts, setObjectiveDrafts] = useState<Record<string, string>>({});
  const [history, setHistory] = useState<Record<string, Campaign[]>>({});
  const [decisions, setDecisions] = useState<Record<string, CampaignDecision>>({});

  async function load() {
    if (!project) { setItems([]); return; }
    const response = await fetch(`/api/v1/projects/${encodeURIComponent(project)}/campaigns`);
    const data = await response.json();
    if (!response.ok) throw new Error(data.error?.message ?? "Could not load campaigns");
    setItems(data as Campaign[]);
  }
  useEffect(() => { void load().catch((reason) => setError(reason instanceof Error ? reason.message : "Could not load campaigns")); }, [project]);

  async function createCampaign() {
    const criteria = acceptance.split("\n").map((value) => value.trim()).filter(Boolean);
    if (!objective.trim() || !criteria.length) { setError("Add an objective and at least one measurable acceptance criterion."); return; }
    setBusy("create"); setError(""); setNotice("");
    const now = new Date().toISOString();
    const record = { id: crypto.randomUUID(), project, version: 1, status: "draft", objective: objective.trim(),
      acceptance: criteria, steps: [{ id: "step-1", objective: objective.trim(), acceptance: criteria,
        depends_on: [], expected_outcome: "pr_ready", status: "pending" }], max_steps: 1,
      max_runtime_seconds: 3600, max_external_items: 0, active_step_id: null, approved_digest: null,
      stop_reason: null, created_at: now, updated_at: now };
    try {
      const response = await fetch(`/api/v1/projects/${encodeURIComponent(project)}/campaigns`, {
        method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(record),
      });
      const data = await response.json();
      if (!response.ok) throw new Error(data.error?.message ?? "Campaign draft failed");
      setObjective(""); setAcceptance(""); setNotice("Campaign contract saved as a draft."); await load();
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Campaign draft failed"); }
    finally { setBusy(""); }
  }

  async function revise(item: Campaign, changes: Record<string, unknown>, label: string) {
    setBusy(item.id); setError(""); setNotice("");
    try {
      const response = await fetch(`/api/v1/campaigns/${encodeURIComponent(item.id)}`, {
        method: "PATCH", headers: { "content-type": "application/json" },
        body: JSON.stringify({ expected_version: item.version, changes }),
      });
      const data = await response.json();
      if (!response.ok) throw new Error(data.error?.message ?? "Campaign update failed");
      setItems((current) => current.map((entry) => entry.id === item.id ? data : entry)); setNotice(label);
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Campaign update failed"); }
    finally { setBusy(""); }
  }

  async function approve(item: Campaign) {
    setBusy(item.id); setError(""); setNotice("");
    try {
      const response = await fetch(`/api/v1/campaigns/${encodeURIComponent(item.id)}/approvals`, {
        method: "POST", headers: { "content-type": "application/json" },
        body: JSON.stringify({ expected_version: item.version }),
      });
      const data = await response.json();
      if (!response.ok) throw new Error(data.error?.message ?? "Campaign approval failed");
      setItems((current) => current.map((entry) => entry.id === item.id ? data : entry));
      setNotice("Campaign contract approved. Progression remains disabled until its execution and completion gates are implemented.");
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Campaign approval failed"); }
    finally { setBusy(""); }
  }

  async function showHistory(item: Campaign) {
    setBusy(`history:${item.id}`); setError("");
    try {
      const response = await fetch(`/api/v1/campaigns/${encodeURIComponent(item.id)}/history`);
      const data = await response.json();
      if (!response.ok) throw new Error(data.error?.message ?? "Could not load campaign history");
      setHistory((current) => ({ ...current, [item.id]: data as Campaign[] }));
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load campaign history"); }
    finally { setBusy(""); }
  }

  async function evaluate(item: Campaign) {
    setBusy(`evaluate:${item.id}`); setError("");
    try {
      const response = await fetch(`/api/v1/campaigns/${encodeURIComponent(item.id)}/progress`);
      const data = await response.json();
      if (!response.ok) throw new Error(data.error?.message ?? "Could not evaluate campaign progress");
      setDecisions((current) => ({ ...current, [item.id]: data as CampaignDecision }));
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not evaluate campaign progress"); }
    finally { setBusy(""); }
  }

  if (!project) return null;
  return <section className="list" aria-label="Project campaigns">
    <h3>Campaign contracts</h3>
    <p className="muted">Draft and review a measurable objective, simple dependency plan, and bounded budget. Approval records the contract; it does not start work.</p>
    <div className="form-grid">
      <label>Campaign objective<input className="input" value={objective} maxLength={4000} required
        onChange={(event) => setObjective(event.target.value)} placeholder="Improve reliability across the import flow" /></label>
      <label>Acceptance criteria, one per line<textarea className="input textarea" value={acceptance} maxLength={12000} rows={3} required
        onChange={(event) => setAcceptance(event.target.value)} placeholder="Import failures preserve the original error\nFocused tests cover retry behavior" /></label>
      <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void createCampaign()}>{busy === "create" ? "Saving…" : "Save campaign draft"}</button>
    </div>
    {notice && <p className="success" role="status">{notice}</p>}{error && <p className="error" role="alert">{error}</p>}
    {items.map((item) => <article className="campaign-row" key={item.id}>
      <div className="row-between"><strong>{item.objective}</strong><span className="badge">{item.status} · v{item.version}</span></div>
      <p className="muted">{item.steps.length} step · up to {item.max_runtime_seconds}s · {item.max_external_items} external items</p>
      <ol>{item.steps.map((step) => <li key={step.id}>{step.objective} · {step.expected_outcome.replaceAll("_", " ")}</li>)}</ol>
      <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void showHistory(item)}>
        {busy === `history:${item.id}` ? "Loading history…" : "Show campaign history"}
      </button>
      {history[item.id] && <ol aria-label={`History for campaign ${item.id}`}>
        {history[item.id].map((version) => <li key={version.version}>v{version.version} · {version.status} · {version.objective}</li>)}
      </ol>}
      {item.status === "approved" && <button className="button secondary" type="button" disabled={Boolean(busy)}
        onClick={() => void evaluate(item)}>{busy === `evaluate:${item.id}` ? "Checking…" : "Evaluate next step"}</button>}
      {decisions[item.id] && <p className="muted" role="status">{decisions[item.id].action}{decisions[item.id].step_id ? ` · step ${decisions[item.id].step_id}` : ""} · {decisions[item.id].reason} Progression is advisory; no work is dispatched.</p>}
      {(item.status === "draft" || item.status === "approved" || item.status === "paused") && <details className="plan-edit">
        <summary>Edit campaign contract</summary>
        <label>Objective<input className="input" value={objectiveDrafts[item.id] ?? item.objective} maxLength={4000}
          onChange={(event) => setObjectiveDrafts((current) => ({ ...current, [item.id]: event.target.value }))} /></label>
        <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() =>
          void revise(item, { objective: objectiveDrafts[item.id] ?? item.objective }, "Campaign revision saved; review approval again.")}>Save contract revision</button>
      </details>}
      {item.status === "draft" && <div className="decision-buttons">
        <button className="button" type="button" disabled={Boolean(busy)} onClick={() => void approve(item)}>Approve contract</button>
        <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void revise(item, { status: "stopped", stop_reason: "Stopped during draft review." }, "Draft campaign stopped.")}>Stop draft</button>
      </div>}
      {item.status === "approved" && <div className="decision-buttons">
        <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void revise(item, { status: "paused" }, "Campaign contract paused.")}>Pause</button>
        <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void revise(item, { status: "stopped", stop_reason: "Stopped by project owner." }, "Campaign stopped.")}>Stop</button>
      </div>}
      {item.status === "paused" && <div className="decision-buttons">
        <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void revise(item, { status: "approved" }, "Campaign contract restored to approved.")}>Restore approval</button>
        <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void revise(item, { status: "stopped", stop_reason: "Stopped by project owner." }, "Campaign stopped.")}>Stop</button>
      </div>}
    </article>)}
  </section>;
}

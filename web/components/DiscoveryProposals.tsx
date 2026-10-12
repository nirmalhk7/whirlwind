"use client";

import { useEffect, useState } from "react";

type Proposal = {
  id: string; version: number; project: string; status: string; category: string;
  problem: string; benefit: string; impact: string; confidence: string; uncertainty: string[];
  scope: string[]; risks: string[]; dependencies: string[]; acceptance: string[];
  recommended_next_action: string; evidence: Array<{ kind: string; path: string; title: string; line?: number | null }>;
  planning_task_id?: string | null;
};

export function DiscoveryProposals({ project }: { project: string }) {
  const [items, setItems] = useState<Proposal[]>([]);
  const [busy, setBusy] = useState("");
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [edits, setEdits] = useState<Record<string, { problem: string; benefit: string; acceptance: string }>>({});
  const [history, setHistory] = useState<Record<string, Proposal[]>>({});

  async function load() {
    if (!project) { setItems([]); return; }
    const response = await fetch(`/api/v1/projects/${encodeURIComponent(project)}/discovery-proposals`);
    const data = await response.json();
    if (!response.ok) throw new Error(data.error?.message ?? "Could not load proposals");
    setItems(data as Proposal[]);
  }

  useEffect(() => { void load().catch((reason) => setError(reason instanceof Error ? reason.message : "Could not load proposals")); }, [project]);

  async function synthesize() {
    setBusy("synthesize"); setNotice(""); setError("");
    try {
      const response = await fetch(`/api/v1/projects/${encodeURIComponent(project)}/discovery-proposals`, {
        method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ action: "synthesize" }),
      });
      const data = await response.json();
      if (!response.ok) throw new Error(data.error?.message ?? "Proposal synthesis failed");
      await load();
      setNotice(`${data.filter((item: any) => item.inserted).length} new proposal${data.length === 1 ? "" : "s"} saved from the latest scan.`);
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Proposal synthesis failed"); }
    finally { setBusy(""); }
  }

  async function revise(item: Proposal, changes: Record<string, unknown>, label: string) {
    setBusy(item.id); setNotice(""); setError("");
    try {
      const response = await fetch(`/api/v1/discovery-proposals/${encodeURIComponent(item.id)}`, {
        method: "PATCH", headers: { "content-type": "application/json" },
        body: JSON.stringify({ expected_version: item.version, changes }),
      });
      const data = await response.json();
      if (!response.ok) throw new Error(data.error?.message ?? "Proposal update failed");
      setItems((current) => current.map((entry) => entry.id === item.id ? data : entry));
      setNotice(label);
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Proposal update failed"); }
    finally { setBusy(""); }
  }

  async function approve(item: Proposal) {
    setBusy(item.id); setNotice(""); setError("");
    try {
      const response = await fetch(`/api/v1/discovery-proposals/${encodeURIComponent(item.id)}/approvals`, {
        method: "POST", headers: { "content-type": "application/json", "idempotency-key": crypto.randomUUID() },
        body: JSON.stringify({ expected_version: item.version, objective: item.problem, acceptance_criteria: item.acceptance }),
      });
      const data = await response.json();
      if (!response.ok) throw new Error(data.error?.message ?? "Proposal approval failed");
      setItems((current) => current.map((entry) => entry.id === item.id ? data.proposal : entry));
      setNotice(`Planning draft created for task ${data.proposal.planning_task_id}. Review it in Plans before any run is approved.`);
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Proposal approval failed"); }
    finally { setBusy(""); }
  }

  async function showHistory(item: Proposal) {
    setBusy(`history:${item.id}`); setError("");
    try {
      const response = await fetch(`/api/v1/discovery-proposals/${encodeURIComponent(item.id)}/history`);
      const data = await response.json();
      if (!response.ok) throw new Error(data.error?.message ?? "Could not load proposal history");
      setHistory((current) => ({ ...current, [item.id]: data as Proposal[] }));
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load proposal history"); }
    finally { setBusy(""); }
  }

  if (!project) return null;
  return <section className="list" aria-label="Discovery proposals">
    <div className="row-between"><h3>Evidence-backed proposals</h3>
      <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void synthesize()}>
        {busy === "synthesize" ? "Reviewing evidence…" : "Synthesize from latest scan"}
      </button>
    </div>
    <p className="muted">Local, deterministic proposals use safe scan evidence and project priorities. Approval creates a Planning draft only.</p>
    {notice && <p className="success" role="status">{notice}</p>}
    {error && <p className="error" role="alert">{error}</p>}
    {items.length === 0 ? <p className="muted">No proposals yet. Run a scan, then synthesize proposals.</p> : items.map((item) => <article className="card" key={item.id}>
      <div className="row-between"><strong>{item.category} · {item.status}</strong><span className="muted">v{item.version} · {item.confidence} confidence</span></div>
      <p>{item.problem}</p><p className="muted">{item.benefit}</p>
      <p className="muted">Evidence: {item.evidence.map((evidence) => `${evidence.path}${evidence.line ? `:${evidence.line}` : ""} (${evidence.title})`).join("; ")}</p>
      {item.scope.length > 0 && <p className="muted">Scope: {item.scope.join(", ")}</p>}
      {item.planning_task_id && <p className="success">Planning task: {item.planning_task_id}</p>}
      <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void showHistory(item)}>
        {busy === `history:${item.id}` ? "Loading history…" : "Show proposal history"}
      </button>
      {history[item.id] && <ol aria-label={`History for proposal ${item.id}`}>
        {history[item.id].map((version) => <li key={version.version}>v{version.version} · {version.status} · {version.problem}</li>)}
      </ol>}
      {(item.status === "proposed" || item.status === "deferred") && <div className="decision-buttons">
        <button className="button" type="button" disabled={Boolean(busy)} onClick={() => void approve(item)}>Approve to Planning</button>
        {item.status === "proposed"
          ? <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void revise(item, { status: "deferred" }, "Proposal deferred.")}>Defer</button>
          : <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void revise(item, { status: "proposed" }, "Proposal restored.")}>Restore</button>}
        <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void revise(item, { status: "dismissed" }, "Proposal dismissed.")}>Dismiss</button>
        <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void revise(item, { status: "rejected" }, "Fingerprint rejected until relevant evidence changes.")}>Reject</button>
        <details className="plan-edit"><summary>Edit proposal</summary>
          <label>Problem<input className="input" value={edits[item.id]?.problem ?? item.problem} maxLength={4000}
            onChange={(event) => setEdits((current) => ({ ...current, [item.id]: { problem: event.target.value,
              benefit: current[item.id]?.benefit ?? item.benefit, acceptance: current[item.id]?.acceptance ?? item.acceptance.join("\n") } }))} /></label>
          <label>Benefit<input className="input" value={edits[item.id]?.benefit ?? item.benefit} maxLength={2000}
            onChange={(event) => setEdits((current) => ({ ...current, [item.id]: { problem: current[item.id]?.problem ?? item.problem,
              benefit: event.target.value, acceptance: current[item.id]?.acceptance ?? item.acceptance.join("\n") } }))} /></label>
          <label>Acceptance criteria, one per line<textarea className="input textarea" rows={3} maxLength={12000}
            value={edits[item.id]?.acceptance ?? item.acceptance.join("\n")}
            onChange={(event) => setEdits((current) => ({ ...current, [item.id]: { problem: current[item.id]?.problem ?? item.problem,
              benefit: current[item.id]?.benefit ?? item.benefit, acceptance: event.target.value } }))} /></label>
          <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => {
            const edit = edits[item.id] ?? { problem: item.problem, benefit: item.benefit, acceptance: item.acceptance.join("\n") };
            const acceptance = edit.acceptance.split("\n").map((value) => value.trim()).filter(Boolean).slice(0, 30);
            void revise(item, { problem: edit.problem, benefit: edit.benefit, acceptance }, "Proposal edited and saved as a new version.");
          }}>Save revision</button>
        </details>
      </div>}
    </article>)}
  </section>;
}

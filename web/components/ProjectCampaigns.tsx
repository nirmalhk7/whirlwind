"use client";

import { useEffect, useState } from "react";
import { CampaignRow, type Campaign, type CampaignDecision } from "./CampaignRow";

async function campaignRequest<T>(url: string, fallback: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, init);
  const data = await response.json();
  if (!response.ok) throw new Error(data.error?.message ?? fallback);
  return data as T;
}

function errorMessage(reason: unknown, fallback: string) {
  return reason instanceof Error ? reason.message : fallback;
}

function useCampaigns(project: string) {
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
    setItems(await campaignRequest<Campaign[]>(`/api/v1/projects/${encodeURIComponent(project)}/campaigns`, "Could not load campaigns"));
  }
  useEffect(() => { void load().catch((reason) => setError(errorMessage(reason, "Could not load campaigns"))); }, [project]);

  async function run(fallback: string, action: () => Promise<void>) {
    try { await action(); }
    catch (reason) { setError(errorMessage(reason, fallback)); }
    finally { setBusy(""); }
  }

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
    await run("Campaign draft failed", async () => {
      await campaignRequest<Campaign>(`/api/v1/projects/${encodeURIComponent(project)}/campaigns`, "Campaign draft failed", {
        method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(record),
      });
      setObjective(""); setAcceptance(""); setNotice("Campaign contract saved as a draft."); await load();
    });
  }

  async function revise(item: Campaign, changes: Record<string, unknown>, label: string) {
    setBusy(item.id); setError(""); setNotice("");
    await run("Campaign update failed", async () => {
      const data = await campaignRequest<Campaign>(`/api/v1/campaigns/${encodeURIComponent(item.id)}`, "Campaign update failed", {
        method: "PATCH", headers: { "content-type": "application/json" },
        body: JSON.stringify({ expected_version: item.version, changes }),
      });
      setItems((current) => current.map((entry) => entry.id === item.id ? data : entry)); setNotice(label);
    });
  }

  async function approve(item: Campaign) {
    setBusy(item.id); setError(""); setNotice("");
    await run("Campaign approval failed", async () => {
      const data = await campaignRequest<Campaign>(`/api/v1/campaigns/${encodeURIComponent(item.id)}/approvals`, "Campaign approval failed", {
        method: "POST", headers: { "content-type": "application/json" },
        body: JSON.stringify({ expected_version: item.version }),
      });
      setItems((current) => current.map((entry) => entry.id === item.id ? data : entry));
      setNotice("Campaign contract approved. Progression remains disabled until its execution and completion gates are implemented.");
    });
  }

  async function showHistory(item: Campaign) {
    setBusy(`history:${item.id}`); setError("");
    await run("Could not load campaign history", async () => {
      const data = await campaignRequest<Campaign[]>(`/api/v1/campaigns/${encodeURIComponent(item.id)}/history`, "Could not load campaign history");
      setHistory((current) => ({ ...current, [item.id]: data as Campaign[] }));
    });
  }

  async function evaluate(item: Campaign) {
    setBusy(`evaluate:${item.id}`); setError("");
    await run("Could not evaluate campaign progress", async () => {
      const data = await campaignRequest<CampaignDecision>(`/api/v1/campaigns/${encodeURIComponent(item.id)}/progress`, "Could not evaluate campaign progress");
      setDecisions((current) => ({ ...current, [item.id]: data as CampaignDecision }));
    });
  }

  return { items, objective, setObjective, acceptance, setAcceptance, notice, error, busy,
    objectiveDrafts, setObjectiveDrafts, history, decisions, createCampaign, revise, approve, showHistory, evaluate };
}

export function ProjectCampaigns({ project }: { project: string }) {
  const { items, objective, setObjective, acceptance, setAcceptance, notice, error, busy,
    objectiveDrafts, setObjectiveDrafts, history, decisions, createCampaign, revise, approve, showHistory, evaluate } = useCampaigns(project);
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
    {items.map((item) => <CampaignRow key={item.id} item={item} busy={busy}
      history={history[item.id]} decision={decisions[item.id]} objectiveDraft={objectiveDrafts[item.id]}
      setObjectiveDraft={(value) => setObjectiveDrafts((current) => ({ ...current, [item.id]: value }))}
      showHistory={showHistory} evaluate={evaluate} revise={revise} approve={approve} />)}
  </section>;
}

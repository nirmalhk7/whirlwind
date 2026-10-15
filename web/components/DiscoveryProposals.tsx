"use client";

import { useEffect, useState } from "react";

import { DiscoveryProposalCard, type Proposal, type ProposalEdit } from "./DiscoveryProposalCard";

async function proposalRequest<T>(url: string, fallback: string, options?: RequestInit): Promise<T> {
  const response = await fetch(url, options);
  const data = await response.json();
  if (!response.ok) throw new Error(data.error?.message ?? fallback);
  return data as T;
}

function proposalError(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

function replaceProposal(items: Proposal[], id: string, updated: Proposal): Proposal[] {
  return items.map((item) => item.id === id ? updated : item);
}

function useProposalFeedback() {
  const [busy, setBusy] = useState("");
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");

  async function run(id: string, fallback: string, action: () => Promise<void>, clearNotice = true) {
    setBusy(id); setError("");
    if (clearNotice) setNotice("");
    try { await action(); }
    catch (reason) { setError(proposalError(reason, fallback)); }
    finally { setBusy(""); }
  }

  return { busy, notice, error, setNotice, setError, run };
}

export function DiscoveryProposals({ project }: { project: string }) {
  const [items, setItems] = useState<Proposal[]>([]);
  const { busy, notice, error, setNotice, setError, run } = useProposalFeedback();
  const [edits, setEdits] = useState<Record<string, ProposalEdit>>({});
  const [history, setHistory] = useState<Record<string, Proposal[]>>({});

  async function load() {
    if (!project) { setItems([]); return; }
    const data = await proposalRequest<Proposal[]>(`/api/v1/projects/${encodeURIComponent(project)}/discovery-proposals`, "Could not load proposals");
    setItems(data);
  }

  useEffect(() => { void load().catch((reason) => setError(proposalError(reason, "Could not load proposals"))); }, [project]);

  async function synthesize() {
    await run("synthesize", "Proposal synthesis failed", async () => {
      const data = await proposalRequest<Array<{ inserted: boolean }>>(`/api/v1/projects/${encodeURIComponent(project)}/discovery-proposals`, "Proposal synthesis failed", {
        method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ action: "synthesize" }),
      });
      await load();
      setNotice(`${data.filter((item) => item.inserted).length} new proposal${data.length === 1 ? "" : "s"} saved from the latest scan.`);
    });
  }

  async function revise(item: Proposal, changes: Record<string, unknown>, label: string) {
    await run(item.id, "Proposal update failed", async () => {
      const data = await proposalRequest<Proposal>(`/api/v1/discovery-proposals/${encodeURIComponent(item.id)}`, "Proposal update failed", {
        method: "PATCH", headers: { "content-type": "application/json" },
        body: JSON.stringify({ expected_version: item.version, changes }),
      });
      setItems((current) => replaceProposal(current, item.id, data));
      setNotice(label);
    });
  }

  async function approve(item: Proposal) {
    await run(item.id, "Proposal approval failed", async () => {
      const data = await proposalRequest<{ proposal: Proposal }>(`/api/v1/discovery-proposals/${encodeURIComponent(item.id)}/approvals`, "Proposal approval failed", {
        method: "POST", headers: { "content-type": "application/json", "idempotency-key": crypto.randomUUID() },
        body: JSON.stringify({ expected_version: item.version, objective: item.problem, acceptance_criteria: item.acceptance }),
      });
      setItems((current) => replaceProposal(current, item.id, data.proposal));
      setNotice(`Planning draft created for task ${data.proposal.planning_task_id}. Review it in Plans before any run is approved.`);
    });
  }

  async function showHistory(item: Proposal) {
    await run(`history:${item.id}`, "Could not load proposal history", async () => {
      const data = await proposalRequest<Proposal[]>(`/api/v1/discovery-proposals/${encodeURIComponent(item.id)}/history`, "Could not load proposal history");
      setHistory((current) => ({ ...current, [item.id]: data }));
    }, false);
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
    {items.length === 0 ? <p className="muted">No proposals yet. Run a scan, then synthesize proposals.</p> : items.map((item) => <DiscoveryProposalCard key={item.id} item={item} busy={busy}
      edits={edits} setEdits={setEdits} history={history[item.id]}
      approve={approve} revise={revise} showHistory={showHistory} />)}
  </section>;
}

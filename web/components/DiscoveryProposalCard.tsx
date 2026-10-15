import type { Dispatch, SetStateAction } from "react";

export type Proposal = {
  id: string; version: number; project: string; status: string; category: string;
  problem: string; benefit: string; impact: string; confidence: string; uncertainty: string[];
  scope: string[]; risks: string[]; dependencies: string[]; acceptance: string[];
  recommended_next_action: string; evidence: Array<{ kind: string; path: string; title: string; line?: number | null }>;
  planning_task_id?: string | null;
};

export type ProposalEdit = { problem: string; benefit: string; acceptance: string };
type ReviseProposal = (item: Proposal, changes: Record<string, unknown>, label: string) => Promise<void>;
type EditorProps = {
  item: Proposal;
  busy: string;
  edits: Record<string, ProposalEdit>;
  setEdits: Dispatch<SetStateAction<Record<string, ProposalEdit>>>;
  revise: ReviseProposal;
};
type CardProps = EditorProps & {
  history?: Proposal[];
  approve: (item: Proposal) => Promise<void>;
  showHistory: (item: Proposal) => Promise<void>;
};

function proposalEdit(item: Proposal): ProposalEdit {
  return { problem: item.problem, benefit: item.benefit, acceptance: item.acceptance.join("\n") };
}

function ProposalEditor({ item, busy, edits, setEdits, revise }: EditorProps) {
  const edit = edits[item.id] ?? proposalEdit(item);
  function update(field: keyof ProposalEdit, value: string) {
    setEdits((current) => ({ ...current, [item.id]: { ...(current[item.id] ?? proposalEdit(item)), [field]: value } }));
  }
  function save() {
    const acceptance = edit.acceptance.split("\n").map((value) => value.trim()).filter(Boolean).slice(0, 30);
    void revise(item, { problem: edit.problem, benefit: edit.benefit, acceptance }, "Proposal edited and saved as a new version.");
  }
  return <details className="plan-edit"><summary>Edit proposal</summary>
    <label>Problem<input className="input" value={edit.problem} maxLength={4000}
      onChange={(event) => update("problem", event.target.value)} /></label>
    <label>Benefit<input className="input" value={edit.benefit} maxLength={2000}
      onChange={(event) => update("benefit", event.target.value)} /></label>
    <label>Acceptance criteria, one per line<textarea className="input textarea" rows={3} maxLength={12000}
      value={edit.acceptance} onChange={(event) => update("acceptance", event.target.value)} /></label>
    <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={save}>Save revision</button>
  </details>;
}

function ProposalDecisions({ approve, ...props }: EditorProps & { approve: CardProps["approve"] }) {
  const { item, busy, revise } = props;
  if (item.status !== "proposed" && item.status !== "deferred") return null;
  return <div className="decision-buttons">
    <button className="button" type="button" disabled={Boolean(busy)} onClick={() => void approve(item)}>Approve to Planning</button>
    {item.status === "proposed"
      ? <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void revise(item, { status: "deferred" }, "Proposal deferred.")}>Defer</button>
      : <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void revise(item, { status: "proposed" }, "Proposal restored.")}>Restore</button>}
    <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void revise(item, { status: "dismissed" }, "Proposal dismissed.")}>Dismiss</button>
    <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void revise(item, { status: "rejected" }, "Fingerprint rejected until relevant evidence changes.")}>Reject</button>
    <ProposalEditor {...props} />
  </div>;
}

export function DiscoveryProposalCard({ history, showHistory, ...props }: CardProps) {
  const { item, busy } = props;
  return <article className="card">
    <div className="row-between"><strong>{item.category} · {item.status}</strong><span className="muted">v{item.version} · {item.confidence} confidence</span></div>
    <p>{item.problem}</p><p className="muted">{item.benefit}</p>
    <p className="muted">Evidence: {item.evidence.map((evidence) => `${evidence.path}${evidence.line ? `:${evidence.line}` : ""} (${evidence.title})`).join("; ")}</p>
    {item.scope.length > 0 && <p className="muted">Scope: {item.scope.join(", ")}</p>}
    {item.planning_task_id && <p className="success">Planning task: {item.planning_task_id}</p>}
    <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void showHistory(item)}>
      {busy === `history:${item.id}` ? "Loading history…" : "Show proposal history"}
    </button>
    {history && <ol aria-label={`History for proposal ${item.id}`}>
      {history.map((version) => <li key={version.version}>v{version.version} · {version.status} · {version.problem}</li>)}
    </ol>}
    <ProposalDecisions {...props} />
  </article>;
}

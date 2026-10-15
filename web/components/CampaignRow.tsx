type Step = { id: string; objective: string; acceptance: string[]; depends_on: string[]; expected_outcome: string; status: string };
export type Campaign = {
  id: string; version: number; project: string; status: string; objective: string; acceptance: string[];
  steps: Step[]; max_steps: number; max_runtime_seconds: number; max_external_items: number;
  approved_digest?: string | null; stop_reason?: string | null;
};
export type CampaignDecision = { action: "ready" | "wait" | "completed" | "stop"; step_id?: string | null; reason: string };

type CampaignRowProps = {
  item: Campaign;
  busy: string;
  history?: Campaign[];
  decision?: CampaignDecision;
  objectiveDraft?: string;
  setObjectiveDraft: (objective: string) => void;
  showHistory: (item: Campaign) => Promise<void>;
  evaluate: (item: Campaign) => Promise<void>;
  revise: (item: Campaign, changes: Record<string, unknown>, label: string) => Promise<void>;
  approve: (item: Campaign) => Promise<void>;
};

export function CampaignRow({ item, busy, history, decision, objectiveDraft, setObjectiveDraft, showHistory, evaluate, revise, approve }: CampaignRowProps) {
  return <article className="campaign-row" key={item.id}>
      <div className="row-between"><strong>{item.objective}</strong><span className="badge">{item.status} · v{item.version}</span></div>
      <p className="muted">{item.steps.length} step · up to {item.max_runtime_seconds}s · {item.max_external_items} external items</p>
      <ol>{item.steps.map((step) => <li key={step.id}>{step.objective} · {step.expected_outcome.replaceAll("_", " ")}</li>)}</ol>
      <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void showHistory(item)}>
        {busy === `history:${item.id}` ? "Loading history…" : "Show campaign history"}
      </button>
      {history && <ol aria-label={`History for campaign ${item.id}`}>
        {history.map((version) => <li key={version.version}>v{version.version} · {version.status} · {version.objective}</li>)}
      </ol>}
      {item.status === "approved" && <button className="button secondary" type="button" disabled={Boolean(busy)}
        onClick={() => void evaluate(item)}>{busy === `evaluate:${item.id}` ? "Checking…" : "Evaluate next step"}</button>}
      {decision && <p className="muted" role="status">{decision.action}{decision.step_id ? ` · step ${decision.step_id}` : ""} · {decision.reason} Progression is advisory; no work is dispatched.</p>}
      {(item.status === "draft" || item.status === "approved" || item.status === "paused") && <details className="plan-edit">
        <summary>Edit campaign contract</summary>
        <label>Objective<input className="input" value={objectiveDraft ?? item.objective} maxLength={4000}
          onChange={(event) => setObjectiveDraft(event.target.value)} /></label>
        <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() =>
          void revise(item, { objective: objectiveDraft ?? item.objective }, "Campaign revision saved; review approval again.")}>Save contract revision</button>
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
  </article>;

}

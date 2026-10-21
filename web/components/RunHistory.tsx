"use client";

import { RunEvidence } from "./RunEvidence";
import type { LearningSnapshot } from "./RunLearning";
import { compareRuns, runAvailability, runModel, runOutcome, runProvider, runThinking, runTitle, runTransport } from "./runHistoryPresentation";

export type RunRecord = {
  id: string;
  updated_at?: string;
  data: {
    learning?: LearningSnapshot;
    attempt_id?: string; status?: string; purpose?: string; unit_id?: string; operation_id?: string; previous_attempt_id?: string; usage?: { scope:string; total_tokens?:number|null; input_tokens?:number|null; output_tokens?:number|null; cost?:number|null } | null;
    task: string; project_id?: string; role: string; provider: string; provider_name?: string;
    model?: string; resolved_model?: string; transport?: string;
    thinking?: { config_id: string; value: string } | null;
    effective_thinking?: { config_id: string; value: string } | null;
    started_at?: string; finished_at?: string; elapsed_ms?: number;
    finished: boolean; success?: boolean; error?: string;
  };
};
export function RunHistory({ runs, projectName }: { runs: RunRecord[]; projectName: (id: string) => string }) {
  const ordered = [...runs].sort(compareRuns);
  return <section className="run-history" aria-label="Execution history">
    <h2>Execution history</h2>
    {!ordered.length && <p className="muted">No agent runs yet. Run ideation from Inbox to get started.</p>}
    {ordered.map((record) => <RunRow key={record.id} record={record} projectName={projectName} />)}
  </section>;
}

function RunRow({ record: { id, updated_at, data: run }, projectName }: { record: RunRecord; projectName: (id: string) => string }) {
  return <article className="run-row" id={`run-${id}`}>
    <div className="row"><h3>{runTitle(run)}</h3><span className="badge" data-availability={runAvailability(run)}>{runOutcome(run)}</span></div>
    <RunMetadata run={run} projectName={projectName} />
    {run.error && <p className="error">{run.error}</p>}
    <RunEvidence id={id} captured={Boolean(run.attempt_id)} usage={run.usage} task={run.task} updatedAt={updated_at} learning={run.learning} />
  </article>;
}

function RunMetadata({ run, projectName }: { run: RunRecord["data"]; projectName: (id: string) => string }) {
  return <dl className="run-metadata">
    <div><dt>Provider</dt><dd>{runProvider(run)}</dd></div>
    <div><dt>Model</dt><dd>{runModel(run)}</dd></div>
    <div><dt>Thinking level</dt><dd>{runThinking(run)}</dd></div>
    <div><dt>Transport</dt><dd>{runTransport(run)}</dd></div>
    <div><dt>Project</dt><dd>{run.project_id ? projectName(run.project_id) : "Unknown"}</dd></div>
    <div><dt>Task</dt><dd>{run.task || "Unknown"}</dd></div>
    <div><dt>Started</dt><dd>{run.started_at ? <time dateTime={run.started_at}>{new Date(run.started_at).toLocaleString()}</time> : "Unknown"}</dd></div>
    <div><dt>Duration</dt><dd>{run.elapsed_ms !== undefined ? `${(run.elapsed_ms / 1000).toFixed(1)}s` : "Unknown"}</dd></div>
    {run.unit_id && <div><dt>Unit</dt><dd>{run.unit_id}</dd></div>}
    {run.previous_attempt_id && <div><dt>Previous attempt</dt><dd><a href={`#run-${run.previous_attempt_id}`}>{run.previous_attempt_id}</a></dd></div>}
  </dl>;
}

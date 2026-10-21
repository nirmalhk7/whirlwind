"use client";

import { useEffect, useState } from "react";
import {RunRuntime,type RuntimeSnapshot} from "./RunRuntime";
import { RunLearning, type LearningSnapshot } from "./RunLearning";

type Artifact = { id: string; kind: string; bytes: number; stored_bytes: number; truncated: boolean };
type Linked = { kind: string; record: { id:string; data:unknown } };
type Event = { metadata?: Record<string,unknown>; sequence: number; kind: string; replay: boolean; created_at: string; artifact?: Artifact };
type Page = { items: Event[]; next_after: number };
type Usage = { scope: string; total_tokens?: number | null; input_tokens?: number | null; output_tokens?: number | null; cost?: number | null };

function evidenceError(reason: unknown) {
  return reason instanceof Error ? reason.message : "Evidence unavailable";
}

async function fetchEvidence(id: string, signal: AbortSignal) {
  const root = `/api/v1/sessions/${encodeURIComponent(id)}`;
  const [eventsResponse, evidenceResponse] = await Promise.all([fetch(`${root}/events`, { signal }), fetch(`${root}/evidence`, { signal })]);
  if (!eventsResponse.ok || !evidenceResponse.ok) throw new Error("Run evidence unavailable");
  const [page, evidence]: [Page, { items?: Linked[] }] = await Promise.all([eventsResponse.json(), evidenceResponse.json()]);
  return { page, linked: evidence.items ?? [] };
}

function ArtifactContent({ artifact }: { artifact: Artifact }) {
  const [content, setContent] = useState<string>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  async function load() {
    setLoading(true); setError("");
    try {
      const response = await fetch(`/api/v1/artifacts/${encodeURIComponent(artifact.id)}/content`);
      if (!response.ok) throw new Error("Evidence content unavailable");
      setContent(await response.text());
    } catch (reason) { setError(evidenceError(reason)); }
    finally { setLoading(false); }
  }
  return <div className="run-artifact">
    {artifact.truncated && <p className="muted">Truncated: {artifact.stored_bytes} of {artifact.bytes} bytes</p>}
    {content === undefined ? <button className="button secondary" type="button" disabled={loading} onClick={() => void load()}>{loading ? "Loading…" : `Show ${artifact.kind}`}</button>
      : <pre className="run-content">{content || "No content retained."}</pre>}
    {error && <p className="error" role="alert">{error}</p>}
  </div>;
}

function useEvidence(id: string, open: boolean, captured: boolean, updatedAt?: string) {
  const [items, setItems] = useState<Event[]>([]);
  const [linked, setLinked] = useState<Linked[]>([]);
  const [cursor, setCursor] = useState(0);
  const [more, setMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (!open || !captured) return;
    const controller = new AbortController();
    setLoading(true); setError("");
    void fetchEvidence(id, controller.signal).then(({ page, linked }) => {
      setItems(page.items); setCursor(page.next_after); setMore(page.items.length === 100); setLinked(linked);
    }).catch(reason => { if (!controller.signal.aborted) setError(evidenceError(reason)); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [id, open, captured, updatedAt]);
  async function loadMore() {
    setLoading(true); setError("");
    try {
      const response = await fetch(`/api/v1/sessions/${encodeURIComponent(id)}/events?after=${cursor}`);
      if (!response.ok) throw new Error("Run evidence unavailable");
      const page: Page = await response.json();
      setItems(previous => [...previous, ...page.items]); setCursor(page.next_after); setMore(page.items.length === 100);
    } catch (reason) { setError(evidenceError(reason)); }
    finally { setLoading(false); }
  }
  return { items, linked, more, loading, error, loadMore };
}

export function RunEvidence({ id, captured, usage, task, updatedAt, learning, runtime }: { id: string; captured: boolean; usage?: Usage | null; task: string; updatedAt?: string; learning?: LearningSnapshot; runtime?:RuntimeSnapshot }) {
  const [open, setOpen] = useState(false);
  const { items, linked, more, loading, error, loadMore } = useEvidence(id, open, captured, updatedAt);
  return <div className="run-evidence">
    <button className="button secondary" type="button" aria-expanded={open} onClick={() => setOpen(value => !value)}>{open ? "Hide evidence" : "Inspect run"}</button>
    {open && <section aria-label="Run evidence">
      <RunRuntime runtime={runtime} />
      {usage && <UsageSummary usage={usage} />}
      {learning && <RunLearning learning={learning} />}
      {!captured && <p className="muted">Detailed evidence unavailable for historical run.</p>}
      {error && <p className="error" role="alert">{error}</p>}
      {loading && <p role="status">Loading evidence…</p>}
      <ol className="run-timeline">{items.map(event => <TimelineEvent key={event.sequence} event={event} />)}</ol>
      {more && <button className="button secondary" type="button" disabled={loading} onClick={() => void loadMore()}>Load more evidence</button>}
      {linked.length > 0 && <LinkedEvidence linked={linked} />}
      {captured && task && <p><a href="/tasks">Inspect task checks and reviews</a></p>}
    </section>}
  </div>;
}

function UsageSummary({ usage }: { usage: Usage }) {
  return <p className="muted">{usage.scope === "session" ? "Session cumulative" : "Attempt"} · {usage.total_tokens ?? "Unknown"} tokens{usage.cost != null ? ` · $${usage.cost}` : " · Cost unknown"}</p>;
}

function TimelineEvent({ event }: { event: Event }) {
  return <li>
    <strong>{event.kind}</strong>{event.replay && <span className="badge">Replayed history</span>}
    <time dateTime={event.created_at}>{new Date(event.created_at).toLocaleString()}</time>
    {event.metadata && Object.keys(event.metadata).length > 0 && <details><summary>Identifiers and outcome</summary><pre className="run-content">{JSON.stringify(event.metadata,null,2)}</pre></details>}
    {event.artifact && <ArtifactContent artifact={event.artifact} />}
  </li>;
}

function LinkedEvidence({ linked }: { linked: Linked[] }) {
  return <div aria-label="Linked checks and decisions"><h4>Checks, reviews, and decisions</h4>
    {linked.map(item => <details key={`${item.kind}:${item.record.id}`}><summary>{({verification:"Checks",reviews:"Review",review_parts:"Review assessment",checkpoints:"Checkpoint",permissions:"Permission",questions:"Questions",audit:"Decision",quota_observations:"Quota"} as Record<string,string>)[item.kind] ?? item.kind} · {item.record.id}</summary><pre className="run-content">{JSON.stringify(item.record.data,null,2)}</pre></details>)}
    {linked.length === 100 && <p className="muted">Showing latest 100 linked records.</p>}
  </div>;
}

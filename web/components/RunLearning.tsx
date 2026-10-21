"use client";

import { useState } from "react";
import {RunMemorySearches} from "./RunMemorySearches";
import { useRunLearning, type LessonRecord } from "./useRunLearning";

export type LearningSnapshot = {
  recalled: { id: string; version: number; status: string; match?: string }[];
  search_calls?: number;
  search_bytes?: number;
  searches?: {query:string;bytes:number;reason:string;lessons:{id:string;version:number}[]}[];
  created_ids?: string[];
  feedback?: { lesson_id: string; verdict: string; reason: string }[];
  warnings?: string[];
  bytes: number;
  estimated_tokens: number;
};
export function RunLearning({ learning }: { learning: LearningSnapshot }) {
  const ids = learningLessonIds(learning);
  const { lessons, error, loading, retire } = useRunLearning(ids);
  return <section aria-label="Agent learning" className="run-learning">
    <h4>Learning</h4>
    <p className="muted">Automatic memory: {learning.bytes} bytes · {learning.estimated_tokens} estimated tokens</p>
    <RunMemorySearches searches={learning.searches} calls={learning.search_calls} bytes={learning.search_bytes} />
    {!ids.length && <p className="muted">No lessons recalled or recorded.</p>}
    {learning.warnings?.map((warning, index) => <p className="muted" key={index}>{warning}</p>)}
    {loading && <p role="status">Loading lessons…</p>}
    {error && <p role="alert" className="error">{error}</p>}
    {lessons.map(record => <Lesson key={record.id} record={record} recalled={learning.recalled?.find(ref => ref.id === record.id)} learned={learning.created_ids?.includes(record.id)} onRetire={retire} />)}
    {learning.feedback?.map((feedback, index) => <p key={index}>Feedback: {feedback.verdict} · {feedback.reason}</p>)}
  </section>;
}

function learningLessonIds(learning: LearningSnapshot): string[] {
  return [...new Set([...(learning.recalled ?? []).map(item => item.id), ...(learning.created_ids ?? [])])].slice(0, 9);
}

function Lesson({ record, recalled, learned, onRetire }: {
  record: LessonRecord; recalled?: { version: number; status: string;match?:string }; learned?: boolean;
  onRetire: (record: LessonRecord) => Promise<void>;
}) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  async function retire() {
    setPending(true); setError("");
    try { await onRetire(record); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not retire lesson. Refresh and retry."); }
    finally { setPending(false); }
  }
  const lesson = record.data;
  return <article className="run-lesson">
    <p className="muted">{recalled && <>Recalled v{recalled.version} · {recalled.status} · </>}{learned && <>Recorded this run · </>}Current: {lesson.status}</p>
    {recalled?.match && <p className="muted">Selected: {recalled.match}</p>}
    <dl>
      <div><dt>When</dt><dd>{lesson.trigger}</dd></div>
      <div><dt>Avoid</dt><dd>{lesson.mistake}</dd></div>
      <div><dt>Instead</dt><dd>{lesson.correction}</dd></div>
    </dl>
    {lesson.paths?.length ? <p className="muted">Sources: {lesson.paths.join(", ")}</p> : null}
    {lesson.evidence_ids?.length ? <details><summary>Evidence references</summary><ul>{lesson.evidence_ids.map(id => <li key={id}><a href={`/api/v1/artifacts/${encodeURIComponent(id)}/content`} target="_blank" rel="noreferrer">{id}</a></li>)}</ul></details> : null}
    {lesson.reason && <p className="muted">{lesson.reason}</p>}
    {lesson.status !== "retired" && <button className="button secondary" type="button" disabled={pending} onClick={() => void retire()}>{pending ? "Retiring…" : "Retire lesson"}</button>}
    {error && <p className="error" role="alert">{error}</p>}
  </article>;
}

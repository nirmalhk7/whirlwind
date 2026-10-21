"use client";

import { useEffect, useState } from "react";
import { api } from "./controlApi";

export type LearningSnapshot = {
  recalled: { id: string; version: number; status: string }[];
  created_ids?: string[];
  feedback?: { lesson_id: string; verdict: string; reason: string }[];
  warnings?: string[];
  bytes: number;
  estimated_tokens: number;
};
type LessonRecord = { id: string; version: number; data: {
  status: string; role: string; trigger: string; mistake: string; correction: string;
  paths?: string[]; evidence_ids?: string[]; reason?: string;
} };

export function RunLearning({ learning }: { learning: LearningSnapshot }) {
  const ids = [...new Set([...(learning.recalled ?? []).map(item => item.id), ...(learning.created_ids ?? [])])].slice(0, 5);
  const key = ids.join(",");
  const [lessons, setLessons] = useState<LessonRecord[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  useEffect(() => {
    if (!key) { setLessons([]); return; }
    const controller = new AbortController();
    setLoading(true); setError("");
    void Promise.all(key.split(",").map(async id => {
      const response = await fetch(`/api/v1/lessons/${encodeURIComponent(id)}`, { signal: controller.signal });
      if (!response.ok) throw new Error("Learning details unavailable. Refresh and retry.");
      return response.json() as Promise<LessonRecord>;
    })).then(records => { if (!controller.signal.aborted) setLessons(records); })
      .catch(reason => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : "Learning details unavailable."); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [key]);
  async function retire(record: LessonRecord) {
    const updated = await api<LessonRecord>(`lessons/${encodeURIComponent(record.id)}/retire`, "POST", {}, record.version);
    setLessons(previous => previous.map(item => item.id === updated.id ? updated : item));
  }
  return <section aria-label="Agent learning" className="run-learning">
    <h4>Learning</h4>
    <p className="muted">Recalled memory: {learning.bytes} bytes · {learning.estimated_tokens} estimated tokens</p>
    {!ids.length && <p className="muted">No lessons recalled or recorded.</p>}
    {learning.warnings?.map((warning, index) => <p className="muted" key={index}>{warning}</p>)}
    {loading && <p role="status">Loading lessons…</p>}
    {error && <p role="alert" className="error">{error}</p>}
    {lessons.map(record => <Lesson key={record.id} record={record} recalled={learning.recalled?.find(ref => ref.id === record.id)} learned={learning.created_ids?.includes(record.id)} onRetire={retire} />)}
    {learning.feedback?.map((feedback, index) => <p key={index}>Feedback: {feedback.verdict} · {feedback.reason}</p>)}
  </section>;
}

function Lesson({ record, recalled, learned, onRetire }: {
  record: LessonRecord; recalled?: { version: number; status: string }; learned?: boolean;
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

"use client";

import { useEffect, useState } from "react";
import { api } from "./controlApi";

export type LessonRecord = { id: string; version: number; data: {
  status: string; role: string; trigger: string; mistake: string; correction: string;
  paths?: string[]; evidence_ids?: string[]; reason?: string;
} };

type LearningDetails = {
  lessons: LessonRecord[];
  error: string;
  loading: boolean;
  retire: (record: LessonRecord) => Promise<void>;
};

async function fetchLearningLesson(id: string, signal: AbortSignal): Promise<LessonRecord> {
  const response = await fetch(`/api/v1/lessons/${encodeURIComponent(id)}`, { signal });
  if (!response.ok) throw new Error("Learning details unavailable. Refresh and retry.");
  return response.json() as Promise<LessonRecord>;
}

function useLearningDetails(key: string) {
  const [lessons, setLessons] = useState<LessonRecord[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  useEffect(() => {
    if (!key) { setLessons([]); return; }
    const controller = new AbortController();
    setLoading(true); setError("");
    void Promise.all(key.split(",").map(id => fetchLearningLesson(id, controller.signal)))
      .then(records => { if (!controller.signal.aborted) setLessons(records); })
      .catch(reason => { if (!controller.signal.aborted) setError(learningDetailsError(reason)); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [key]);
  return { lessons, error, loading, setLessons };
}

function learningDetailsError(reason: unknown): string {
  return reason instanceof Error ? reason.message : "Learning details unavailable.";
}

export function useRunLearning(ids: string[]): LearningDetails {
  const { lessons, error, loading, setLessons } = useLearningDetails(ids.join(","));
  async function retire(record: LessonRecord): Promise<void> {
    const updated = await api<LessonRecord>(`lessons/${encodeURIComponent(record.id)}/retire`, "POST", {}, record.version);
    setLessons(previous => previous.map(item => item.id === updated.id ? updated : item));
  }
  return { lessons, error, loading, retire };
}

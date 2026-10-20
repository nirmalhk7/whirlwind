import { useCallback, useEffect, useState } from "react";
import { api } from "./controlApi";
import type { SettingsFileView } from "./SettingsFileEditor";
import type { RunRecord } from "./RunHistory";
import { empty, type Resource, type Inventory, type View, type Review, type Permission, type NativeQuestions } from "./controlTypes";

export function useControlInventory(view: View) {
 const [inventory, setInventory] = useState<Inventory>(empty);
 const [error, setError] = useState("");
 const [loaded, setLoaded] = useState(false);
 const [processing, setProcessing] = useState(false);
 const [workersStopped, setWorkersStopped] = useState<boolean | null>(null);
 const [runs, setRuns] = useState<RunRecord[]>([]);
 const [reviews, setReviews] = useState<Resource<Review>[]>([]);
 const [permissions, setPermissions] = useState<Resource<Permission>[]>([]);
 const [nativeQuestions, setNativeQuestions] = useState<Resource<NativeQuestions>[]>([]);
 const [startupSettings, setStartupSettings] = useState<SettingsFileView | null>(null);
  const refresh = useCallback(async () => {
    try {
      const runStatus = api<{ processing: boolean }>("runners/status").then(status => status.processing === true);
      const stopStatus = api<Resource<{ stopped?: boolean }>>("settings")
        .then(record => record.data ? record.data.stopped === true : null)
        .catch(() => null);
      if (view === "settings") {
        const [snapshot, questions, permissions, inbox, stopped, activeRun] = await Promise.all([
          api<SettingsFileView>("settings/file"),
          api<Resource<NativeQuestions>[]>("questions"),
          api<Resource<Permission>[]>("permissions"),
          api<Inventory["inbox"]>("inbox"),
          stopStatus,
          runStatus,
        ]);
        setStartupSettings(snapshot);
        setWorkersStopped(stopped);
        setProcessing(activeRun);
        setNativeQuestions(questions);
        setPermissions(permissions);
        setInventory(current => ({ ...current, inbox }));
        setError("");
        setLoaded(true);
        return;
      }
      const [data, reviewData, permissionData, questionData, runData, stopped, activeRun] =
        await Promise.all([
          api<Inventory>("overview"),
          api<Resource<Review>[]>("reviews"),
          api<Resource<Permission>[]>("permissions"),
          api<Resource<NativeQuestions>[]>("questions"),
          view === "runs" ? api<RunRecord[]>("sessions") : Promise.resolve([]),
          stopStatus,
          runStatus,
        ]);
      setInventory(data);
      setWorkersStopped(stopped);
      setProcessing(activeRun);
      setError("");
      setLoaded(true);
      setReviews(reviewData);
      setPermissions(permissionData);
      setNativeQuestions(questionData);
      setRuns(runData);
    } catch (reason) {
      setProcessing(false);
      setWorkersStopped(null);
      setError(
        reason instanceof Error ? reason.message : "Backend unavailable.",
      );
      setLoaded(true);
    }
  }, [view]);
 useEffect(() => subscribeUpdates(refresh), [refresh]);
 return {inventory, setInventory, error, loaded, processing, runs, reviews, permissions, nativeQuestions, startupSettings, workersStopped, refresh};
}

function subscribeUpdates(refresh: () => Promise<void>) {
    void refresh();
    const stream = new EventSource("/api/v1/events");
    let timer: ReturnType<typeof setTimeout> | null = null;
    stream.addEventListener("update", () => {
      if (!timer)
        timer = setTimeout(() => {
          timer = null;
          void refresh();
        }, 500);
    });
    const fallback = setInterval(() => {
      if (stream.readyState !== EventSource.OPEN) void refresh();
    }, 10_000);
    return () => {
      stream.close();
      clearInterval(fallback);
      if (timer) clearTimeout(timer);
    };
}

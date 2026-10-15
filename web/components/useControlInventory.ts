import { useCallback, useEffect, useState } from "react";
import { api } from "./controlApi";
import type { Config as AgentConfig } from "./AgentConfiguration";
import type { WorkerSettings } from "./SettingsForm";
import type { RunRecord } from "./RunHistory";
import { empty, type Resource, type Inventory, type View, type Review, type Permission, type NativeQuestions } from "./controlTypes";

export function useControlInventory(view: View) {
 const [inventory, setInventory] = useState<Inventory>(empty);
 const [error, setError] = useState("");
 const [loaded, setLoaded] = useState(false);
 const [runs, setRuns] = useState<RunRecord[]>([]);
 const [reviews, setReviews] = useState<Resource<Review>[]>([]);
 const [permissions, setPermissions] = useState<Resource<Permission>[]>([]);
 const [nativeQuestions, setNativeQuestions] = useState<Resource<NativeQuestions>[]>([]);
 const [agentConfigs, setAgentConfigs] = useState<Resource<AgentConfig>[]>([]);
 const [settings, setSettings] = useState<Resource<WorkerSettings> | null>(null);
  const refresh = useCallback(async () => {
    try {
      const [data, reviewData, permissionData, currentSettings, questionData, runData, agentData] =
        await Promise.all([
          api<Inventory>("overview"),
          api<Resource<Review>[]>("reviews"),
          api<Resource<Permission>[]>("permissions"),
          view === "settings"
            ? api<Resource<WorkerSettings>>("settings")
            : Promise.resolve(null),
          api<Resource<NativeQuestions>[]>("questions"),
          view === "runs" ? api<RunRecord[]>("sessions") : Promise.resolve([]),
 view === "settings" ? api<Resource<AgentConfig>[]>("agents") : Promise.resolve([]),
        ]);
      setInventory(data);
      setError("");
      setLoaded(true);
      setReviews(reviewData);
      setPermissions(permissionData);
      if (currentSettings?.data) setSettings(currentSettings);
 if (view === "settings") setAgentConfigs(agentData);
      setNativeQuestions(questionData);
      setRuns(runData);
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Backend unavailable.",
      );
      setLoaded(true);
    }
  }, [view]);
 useEffect(() => subscribeUpdates(refresh), [refresh]);
 return {inventory, setInventory, error, loaded, runs, reviews, permissions, nativeQuestions, agentConfigs, settings, refresh};
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

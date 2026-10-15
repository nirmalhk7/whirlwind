"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "./controlApi";

import { AgentEditor, catalogKey, type Catalog, type Choice, type Config, type Operation, type Resource, type Source } from "./AgentConfigurationEditor";
export type { Config } from "./AgentConfigurationEditor";

async function waitForOperation(id: string, signal: AbortSignal): Promise<Operation> {
  const deadline = Date.now() + 75_000;
  while (!signal.aborted) {
    const result = await api<Resource<Operation>>(`operations/${encodeURIComponent(id)}`);
    if (result.data.stage !== "running") return result.data;
    if (Date.now() >= deadline) throw new Error("Agent request timed out. Check provider setup and try again.");
    await new Promise((resolve) => setTimeout(resolve, 750));
  }
  throw new Error("Agent request cancelled");
}

function presentField(key: string, value: unknown): Record<string, unknown> {
  return value ? { [key]: value } : {};
}

async function providerOperation(source: Resource<Source>, action: string, body: unknown, signal: AbortSignal, fallback: string): Promise<Operation> {
  try {
    const queued = await api<{ operation_id: string }>(`providers/${encodeURIComponent(source.id)}/${action}`, "POST", body, source.version);
    return await waitForOperation(queued.operation_id, signal);
  } catch (reason) {
    return { stage: "failed", error: reason instanceof Error ? reason.message : fallback };
  }
}

function modelCatalog(operation: Operation): Catalog {
  if (operation.stage === "failed") return { loading: false, models: [], thinkingOptions: [], error: new Error(operation.error).message };
  return { loading: false, models: operation.models ?? [], thinkingOptions: operation.thinking_options ?? [] };
}

function useAgentRecords(configurations?: Resource<Config>[]) {
  const [records, setRecords] = useState<Resource<Config>[]>([]);
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(false);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => {
    const active = new AbortController();
    controller.current = active;
    if (configurations === undefined) void api<Resource<Config>[]>("agents").then((data) => {
      if (!active.signal.aborted) { setRecords(data); setLoaded(true); }
    }).catch((reason) => { if (!active.signal.aborted) { setError(String(reason.message ?? reason)); setLoaded(true); } });
    return () => active.abort();
  }, []);

  useEffect(() => { if (configurations) { setRecords(configurations); setLoaded(true); } }, [configurations]);
  return { records, setRecords, error, loaded, controller };
}

function useProviderChanges({ providers, cache, requests, setCatalogs, setTests }: {
  providers: Resource<Source>[];
  cache: { current: Record<string, Catalog> };
  requests: { current: Record<string, number> };
  setCatalogs: React.Dispatch<React.SetStateAction<Record<string, Catalog>>>;
  setTests: React.Dispatch<React.SetStateAction<Record<string, Operation>>>;
}) {
  const sourceSignatures = useRef<Record<string, string>>({});
  useEffect(() => {
    const next = Object.fromEntries(providers.map(source => [source.id, JSON.stringify([source.data.kind, source.data.command, source.data.model_command])]));
    const changed = new Set(Object.keys(sourceSignatures.current).filter(id => sourceSignatures.current[id] !== next[id]));
    sourceSignatures.current = next;
    if (!changed.size) return;
    for (const key of Object.keys(cache.current)) if (changed.has(JSON.parse(key)[0])) { delete cache.current[key]; requests.current[key] = (requests.current[key] ?? 0) + 1; }
    setCatalogs(current => Object.fromEntries(Object.entries(current).filter(([key]) => !changed.has(JSON.parse(key)[0]))));
    setTests(current => Object.fromEntries(Object.entries(current).filter(([key]) => !changed.has(JSON.parse(key)[1]))));
  }, [providers]);
  return sourceSignatures;
}

export function AgentConfiguration({ providers, configurations }: { providers: Resource<Source>[]; configurations?: Resource<Config>[] }) {
  const [catalogs, setCatalogs] = useState<Record<string, Catalog>>({});
  const [tests, setTests] = useState<Record<string, Operation>>({});
  const cache = useRef<Record<string, Catalog>>({});
  const requests = useRef<Record<string, number>>({});
  const sourceSignatures = useProviderChanges({ providers, cache, requests, setCatalogs, setTests });
  const { records, setRecords, error, loaded, controller } = useAgentRecords(configurations);

  const loadModels = useCallback(async (id: string, model = "", force = false) => {
    const source = providers.find((item) => item.id === id);
    const signal = controller.current?.signal;
    const key = catalogKey(id, model);
    if (!source || !signal || (!force && cache.current[key])) return;
    const request = (requests.current[key] ?? 0) + 1;
    requests.current[key] = request;
    const loading = { loading: true, models: cache.current[key]?.models ?? [], thinkingOptions: cache.current[key]?.thinkingOptions ?? [] };
    cache.current[key] = loading;
    setCatalogs((current) => ({ ...current, [key]: loading }));
    const operation = await providerOperation(source, "models", presentField("model", model), signal, "Could not load options");
    const result = modelCatalog(operation);
    if (signal.aborted || requests.current[key] !== request) return;
    cache.current[key] = result;
    setCatalogs((current) => ({ ...current, [key]: result }));
  }, [providers]);

  const test = async (key: string, choice: Choice) => {
    const source = providers.find((item) => item.id === choice.provider);
    const signal = controller.current?.signal;
    if (!source || !choice.model || !signal) return;
    const signature = sourceSignatures.current[source.id];
    setTests((current) => ({ ...current, [key]: { stage: "running" } }));
    const result = await providerOperation(source, "test", { model: choice.model, ...presentField("thinking", choice.thinking) }, signal, "Connection test failed");
    if (!signal.aborted && signature === sourceSignatures.current[source.id]) setTests((current) => ({ ...current, [key]: result }));
  };

  return (
    <section className="agent-configuration" aria-label="Agent configuration">
      <div className="agent-configuration-intro">
        <p>Primary choice runs first. Quota exhaustion moves work to the next available provider/model in your order. If every choice is exhausted, work pauses for your decision.</p>
        <a className="button" href="#providers">Manage providers</a>
      </div>
      {!loaded && <p role="status">Loading agents…</p>}
      {error && <p className="error" role="alert">{error}</p>}
      {records.map((record) => (
        <AgentEditor key={record.id} record={record} sources={providers} catalogs={catalogs} tests={tests} loadModels={loadModels} test={test}
          onSaved={(saved) => setRecords((current) => current.map((item) => item.id === saved.id ? saved : item))} />
      ))}
    </section>
  );
}

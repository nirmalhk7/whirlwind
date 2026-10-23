"use client";

import { useEffect, useState, type Dispatch, type SetStateAction } from "react";
import { api } from "./controlApi";
import type { Provider, Resource } from "./controlTypes";

export type WorkflowConfiguration = {
  active_mode: string;
  configured_mode: string;
  restart_required: boolean;
  integrations?: {
    discovery?: string;
    sources?: { provider: string; ref: string; scopes?: string[] }[];
    publication?: Record<string, { provider: string; ref: string }>;
  };
};
export type Connection = {
  provider: string;
  ref: string;
  name: string;
  kind: string;
  enabled: boolean;
  callable: boolean;
  error?: string;
};
export type Catalog = { provider: string; integrations: Connection[]; error?: string };
export type ProviderIdentity = { id: string; version: number; name: string; kind: string };
export type WorkflowSettingsProps = {
  providers: Resource<Provider>[];
  busy: boolean;
  perform: (action: () => Promise<unknown>, message: string) => Promise<void>;
};
type Catalogs = Record<string, Catalog>;
type SetCatalogs = Dispatch<SetStateAction<Catalogs>>;

const describeError = (reason: unknown) => reason instanceof Error ? reason.message : "Integration status is unavailable. Try refreshing the provider.";
const providerIdentities = (records: Resource<Provider>[]) => records.map(({ id, version, data }) => ({ id, version, name: data.name, kind: data.kind }));
const inventoryRoute = (id: string) => `providers/${encodeURIComponent(id)}/integrations`;

async function loadWorkflowConfiguration() {
  const record = await api<Resource<WorkflowConfiguration>>("workflow_config/global");
  if (!record.data?.active_mode || !record.data?.configured_mode) throw new Error("Workflow configuration is unavailable. Reload status to try again.");
  return record.data;
}

function useWorkflowConfiguration(reload: number) {
  const [workflow, setWorkflow] = useState<WorkflowConfiguration | null>(null);
  const [workflowError, setWorkflowError] = useState("");
  useEffect(() => {
    let active = true;
    setWorkflowError("");
    void loadWorkflowConfiguration().then(configuration => {
      if (active) setWorkflow(configuration);
    }).catch(reason => { if (active) setWorkflowError(describeError(reason)); });
    return () => { active = false; };
  }, [reload]);
  return { workflow, workflowError };
}

async function loadProviderIdentities(signature: string) {
  const configured = JSON.parse(signature) as ProviderIdentity[];
  if (configured.length) return configured;
  return providerIdentities(await api<Resource<Provider>[]>("providers"));
}

function settledCatalog(snapshot: PromiseSettledResult<Catalog>, id: string): Catalog {
  if (snapshot.status === "fulfilled" && Array.isArray(snapshot.value.integrations)) return snapshot.value;
  const error = snapshot.status === "rejected" ? describeError(snapshot.reason) : "Integration inventory is unavailable. Refresh this provider to try again.";
  return { provider: id, integrations: [], error };
}

async function loadCatalogs(providers: ProviderIdentity[]): Promise<Catalogs> {
  const snapshots = await Promise.allSettled(providers.map(provider => api<Catalog>(inventoryRoute(provider.id))));
  return Object.fromEntries(snapshots.map((snapshot, index) => {
    const id = providers[index].id;
    return [id, settledCatalog(snapshot, id)];
  }));
}

function useIntegrationInventory(signature: string, reload: number) {
  const [inventoryError, setInventoryError] = useState("");
  const [identities, setIdentities] = useState<ProviderIdentity[]>([]);
  const [catalogs, setCatalogs] = useState<Catalogs>({});
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    let active = true;
    setLoading(true);
    setInventoryError("");
    void loadProviderIdentities(signature).then(async list => {
      if (!active) return;
      setIdentities(list);
      const next = await loadCatalogs(list);
      if (active) setCatalogs(next);
    }).catch(reason => { if (active) setInventoryError(describeError(reason)); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [signature, reload]);
  return { inventoryError, identities, catalogs, setCatalogs, loading };
}

async function refreshCatalog(provider: ProviderIdentity) {
  const route = inventoryRoute(provider.id);
  await api(route, "POST", {}, provider.version);
  const snapshot = await api<Catalog>(route);
  if (!Array.isArray(snapshot.integrations)) throw new Error("Integration inventory is unavailable after refresh.");
  return snapshot;
}

function catalogRefreshError(previous: Catalogs, provider: ProviderIdentity, reason: unknown): Catalogs {
  const catalog = previous[provider.id] ?? { provider: provider.id, integrations: [] };
  return { ...previous, [provider.id]: { ...catalog, error: describeError(reason) } };
}

async function updateProviderCatalog(provider: ProviderIdentity, setCatalogs: SetCatalogs) {
  try {
    const snapshot = await refreshCatalog(provider);
    setCatalogs(previous => ({ ...previous, [provider.id]: snapshot }));
  } catch (reason) {
    setCatalogs(previous => catalogRefreshError(previous, provider, reason));
    throw reason;
  }
}

function useProviderRefresh(busy: boolean, perform: WorkflowSettingsProps["perform"], setCatalogs: SetCatalogs) {
  const [refreshing, setRefreshing] = useState("");
  async function refreshProvider(provider: ProviderIdentity) {
    if (busy || refreshing) return;
    setRefreshing(provider.id);
    try {
      await perform(() => updateProviderCatalog(provider, setCatalogs), `${provider.name || provider.id} integration inventory refreshed`);
    } finally { setRefreshing(""); }
  }
  return { refreshing, refreshProvider };
}

export function useWorkflowSettings({ providers, busy, perform }: WorkflowSettingsProps) {
  const [reload, setReload] = useState(0);
  const signature = JSON.stringify(providerIdentities(providers));
  const configuration = useWorkflowConfiguration(reload);
  const inventory = useIntegrationInventory(signature, reload);
  const refresh = useProviderRefresh(busy, perform, inventory.setCatalogs);
  return { ...configuration, ...inventory, ...refresh, reloadStatus: () => setReload(value => value + 1) };
}

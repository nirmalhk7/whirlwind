import type { Dispatch, SetStateAction } from "react";

export type DeskSettings = {
  revision: string;
  global: Record<string, any>;
};

export type ProviderStatus = {
  provider: string;
  executable: string;
  available: boolean;
  version: string | null;
  limitations: string[];
};

type ApiError = { error?: { message?: string } };
const globalFields = ["codex_model", "codex_reasoning_effort", "opencode_enabled", "opencode_model",
  "agent_runtime_timeout_seconds", "agent_idle_timeout_seconds", "max_concurrent_runs"];
const providerFields = ["codex_bin", "claude_bin", "cursor_agent_bin"] as const;


export function deskError(reason: unknown, fallback: string) {
 return reason instanceof Error ? reason.message : fallback;
}

function requireDeskResponse(response: Response, data: ApiError, fallback: string) {
 if (!response.ok) throw new Error(data.error?.message ?? fallback);
}

export async function deskRequest(url: string, fallback: string, init?: RequestInit) {
 const response = await fetch(url, init);
 const data = await response.json();
 requireDeskResponse(response, data, fallback);
 return data;
}

export async function loadDeskConfiguration() {
 const [settingsResponse, providersResponse] = await Promise.all([
  fetch("/api/v1/settings", { cache: "no-store" }),
  fetch("/api/v1/providers", { cache: "no-store" }),
 ]);
 const settings = await settingsResponse.json();
 const providers = await providersResponse.json();
 requireDeskResponse(settingsResponse, settings, "Settings are unavailable.");
 requireDeskResponse(providersResponse, providers, "Provider status is unavailable.");
 return { settings: settings as DeskSettings, providers: providers as ProviderStatus[] };
}

function mergeFields(fields: readonly string[], previous: Record<string, any>, incoming: Record<string, any>, draft: Record<string, any>, result: { merged: Record<string, any>; conflicts: string[] }) {
 for (const field of fields) {
  if (draft[field] !== previous[field]) {
   if (incoming[field] !== previous[field]) result.conflicts.push(field);
   result.merged[field] = draft[field];
  }
 }
}

export function mergeDeskDrafts(next: DeskSettings, previous: DeskSettings | null, reset: boolean, global: Record<string, any>, providers: Record<string, string>) {
 const mergedGlobal = { ...next.global };
 const mergedProviders: Record<string, string> = {
  codex_bin: next.global.codex_bin ?? "codex",
  claude_bin: next.global.claude_bin ?? "claude",
  cursor_agent_bin: next.global.cursor_agent_bin ?? "cursor-agent",
 };
 const conflicts: string[] = [];
 if (previous && !reset) {
  mergeFields(globalFields, previous.global, next.global, global, { merged: mergedGlobal, conflicts });
  mergeFields(providerFields, previous.global, next.global, providers, { merged: mergedProviders, conflicts });
 }
 return { mergedGlobal, mergedProviders, conflicts };
}

export async function saveDeskConfiguration(url: string, method: string, body: unknown, { revision, fallback, operation }: { revision: string; fallback: string; operation: string }) {
 const data = await deskRequest(url, fallback, {
  method,
  headers: { "content-type": "application/json", "idempotency-key": crypto.randomUUID(), "if-match": `"${revision}"` },
  body: JSON.stringify(body),
 });
 if (data.state !== "succeeded") throw new Error(data.error ?? `${operation} did not complete (state ${data.state}).`);
}

export function runtimeChanges(global: Record<string, any>) {
 return { global: {
  codex_model: global.codex_model,
  codex_reasoning_effort: global.codex_reasoning_effort,
  opencode_enabled: global.opencode_enabled,
  opencode_model: global.opencode_model || null,
  agent_runtime_timeout_seconds: Number(global.agent_runtime_timeout_seconds),
  agent_idle_timeout_seconds: Number(global.agent_idle_timeout_seconds),
  max_concurrent_runs: Number(global.max_concurrent_runs),
 } };
}

export function RuntimeFields({ global, setGlobalDraft }: { global: Record<string, any>; setGlobalDraft: Dispatch<SetStateAction<Record<string, any>>> }) {
 return <>
          <label>Codex model<input className="input" value={global.codex_model ?? ""} required
            onChange={(event) => setGlobalDraft((old) => ({ ...old, codex_model: event.target.value }))} /></label>
          <label>Reasoning effort<select className="input" value={global.codex_reasoning_effort ?? "high"}
            onChange={(event) => setGlobalDraft((old) => ({ ...old, codex_reasoning_effort: event.target.value }))}>
            <option>low</option><option>medium</option><option>high</option><option>xhigh</option>
          </select></label>
          <label>Maximum active executions<input className="input" type="number" min="1" max="64" value={global.max_concurrent_runs ?? 3}
            onChange={(event) => setGlobalDraft((old) => ({ ...old, max_concurrent_runs: event.target.value }))} /></label>
          <label>Runtime limit, seconds<input className="input" type="number" min="1" value={global.agent_runtime_timeout_seconds ?? 2700}
            onChange={(event) => setGlobalDraft((old) => ({ ...old, agent_runtime_timeout_seconds: event.target.value }))} /></label>
          <label>Idle limit, seconds<input className="input" type="number" min="1" value={global.agent_idle_timeout_seconds ?? 600}
            onChange={(event) => setGlobalDraft((old) => ({ ...old, agent_idle_timeout_seconds: event.target.value }))} /></label>
          <label className="check-row"><input type="checkbox" checked={Boolean(global.opencode_enabled)}
            onChange={(event) => setGlobalDraft((old) => ({ ...old, opencode_enabled: event.target.checked }))} /> Enable experimental OpenCode adapter</label>
          <label>OpenCode model<input className="input" value={global.opencode_model ?? ""}
            onChange={(event) => setGlobalDraft((old) => ({ ...old, opencode_model: event.target.value }))} /></label>
 </>;
}

export function ProviderFields({ providers, providerDraft, setProviderDraft }: { providers: ProviderStatus[]; providerDraft: Record<string, string>; setProviderDraft: Dispatch<SetStateAction<Record<string, string>>> }) {
 return <>
          {(["codex_bin", "claude_bin", "cursor_agent_bin"] as const).map((field) => {
            const provider = providers.find((item) => item.provider === field.replace("_bin", "").replace("_agent", "-agent"));
            return <label key={field}>{field === "cursor_agent_bin" ? "Cursor Agent" : field === "claude_bin" ? "Claude Code" : "Codex"} executable
              <input className="input" value={providerDraft[field] ?? ""} required
                onChange={(event) => setProviderDraft((old) => ({ ...old, [field]: event.target.value }))} />
              <span className="muted">{provider?.available ? provider.version ?? "Version unavailable" : "CLI not found"}</span>
              {provider?.limitations.map((limitation) => <span className="muted" key={limitation}>{limitation}</span>)}
            </label>;
          })}
 </>;
}

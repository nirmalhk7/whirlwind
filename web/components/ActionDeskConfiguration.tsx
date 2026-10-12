"use client";

import { FormEvent, useCallback, useEffect, useRef, useState } from "react";

type DeskSettings = {
  revision: string;
  global: Record<string, any>;
};

type ProviderStatus = {
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

export function ActionDeskConfiguration({ onUpdated, configurationRevision }: {
  onUpdated: () => void;
  configurationRevision: string;
}) {
  const [settings, setSettings] = useState<DeskSettings | null>(null);
  const settingsRef = useRef<DeskSettings | null>(null);
  const configurationRef = useRef<HTMLDetailsElement>(null);
  const [newProjectName, setNewProjectName] = useState("");
  const [newProjectPath, setNewProjectPath] = useState("");
  const [globalDraft, setGlobalDraft] = useState<Record<string, any>>({});
  const globalDraftRef = useRef(globalDraft);
  globalDraftRef.current = globalDraft;
  const [providerDraft, setProviderDraft] = useState<Record<string, string>>({});
  const providerDraftRef = useRef(providerDraft);
  providerDraftRef.current = providerDraft;
  const [providers, setProviders] = useState<ProviderStatus[]>([]);
  const [busy, setBusy] = useState("");
  const [revisionConflict, setRevisionConflict] = useState(false);
  const revisionConflictRef = useRef(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const load = useCallback(async (resetDrafts = false) => {
    const [settingsResponse, providersResponse] = await Promise.all([
      fetch("/api/v1/settings", { cache: "no-store" }),
      fetch("/api/v1/providers", { cache: "no-store" }),
    ]);
    const nextSettings = await settingsResponse.json();
    const nextProviders = await providersResponse.json();
    if (!settingsResponse.ok) throw new Error((nextSettings as ApiError).error?.message ?? "Settings are unavailable.");
    if (!providersResponse.ok) throw new Error((nextProviders as ApiError).error?.message ?? "Provider status is unavailable.");
    const nextProviderDraft: Record<string, string> = {
      codex_bin: nextSettings.global.codex_bin ?? "codex",
      claude_bin: nextSettings.global.claude_bin ?? "claude",
      cursor_agent_bin: nextSettings.global.cursor_agent_bin ?? "cursor-agent",
    };
    const previous = settingsRef.current;
    const conflicts: string[] = [];
    const mergedGlobal = { ...nextSettings.global };
    const mergedProviders = { ...nextProviderDraft };
    if (previous && !resetDrafts) {
      for (const field of globalFields) {
        const isDirty = globalDraftRef.current[field] !== previous.global[field];
        if (isDirty) {
          if (nextSettings.global[field] !== previous.global[field]) conflicts.push(field);
          mergedGlobal[field] = globalDraftRef.current[field];
        }
      }
      for (const field of providerFields) {
        const isDirty = providerDraftRef.current[field] !== previous.global[field];
        if (isDirty) {
          if (nextSettings.global[field] !== previous.global[field]) conflicts.push(field);
          mergedProviders[field] = providerDraftRef.current[field];
        }
      }
    }
    settingsRef.current = nextSettings;
    setSettings(nextSettings);
    setGlobalDraft(mergedGlobal);
    setProviderDraft(mergedProviders);
    setProviders(nextProviders);
    if (resetDrafts || !previous) {
      revisionConflictRef.current = false;
      setRevisionConflict(false);
      setError("");
    } else if (conflicts.length) {
      revisionConflictRef.current = true;
      setRevisionConflict(true);
      setError(`Configuration changed elsewhere for ${[...new Set(conflicts)].join(", ")}. Your drafts are preserved; reload setup to discard them.`);
    } else if (!revisionConflictRef.current) {
      setError("");
    }
  }, []);

  useEffect(() => {
    void load().catch((reason) => setError(reason instanceof Error ? reason.message : "Settings are unavailable."));
  }, [load]);

  useEffect(() => {
    if (settings && configurationRevision && configurationRevision !== settings.revision) {
      void load().catch((reason) => setError(reason instanceof Error ? reason.message : "Settings are unavailable."));
    }
  }, [configurationRevision, load, settings]);

  useEffect(() => {
    if (window.location.hash === "#action-desk-configuration" && configurationRef.current) {
      configurationRef.current.open = true;
    }
  }, []);

  async function saveGlobalSettings(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!settings) return;
    setBusy("global"); setError(""); setNotice("");
    try {
      const changes = { global: {
        codex_model: globalDraft.codex_model,
        codex_reasoning_effort: globalDraft.codex_reasoning_effort,
        opencode_enabled: globalDraft.opencode_enabled,
        opencode_model: globalDraft.opencode_model || null,
        agent_runtime_timeout_seconds: Number(globalDraft.agent_runtime_timeout_seconds),
        agent_idle_timeout_seconds: Number(globalDraft.agent_idle_timeout_seconds),
        max_concurrent_runs: Number(globalDraft.max_concurrent_runs),
      } };
      const response = await fetch("/api/v1/settings", {
        method: "PATCH",
        headers: { "content-type": "application/json", "idempotency-key": crypto.randomUUID(), "if-match": `"${settings.revision}"` },
        body: JSON.stringify({ changes }),
      });
      const data = await response.json();
      if (!response.ok) throw new Error((data as ApiError).error?.message ?? "Runtime settings could not be saved.");
      if (data.state !== "succeeded") throw new Error(data.error ?? `Save did not complete (state ${data.state}).`);
      setNotice("Runtime settings saved. New executions use the updated configuration.");
      await load(true); onUpdated();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Runtime settings could not be saved.");
    } finally { setBusy(""); }
  }

  async function saveProviderPaths(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!settings) return;
    setBusy("providers"); setError(""); setNotice("");
    try {
      const response = await fetch("/api/v1/settings", {
        method: "PATCH",
        headers: { "content-type": "application/json", "idempotency-key": crypto.randomUUID(), "if-match": `"${settings.revision}"` },
        body: JSON.stringify({ changes: { global: providerDraft } }),
      });
      const data = await response.json();
      if (!response.ok) throw new Error((data as ApiError).error?.message ?? "Provider CLI paths could not be saved.");
      if (data.state !== "succeeded") throw new Error(data.error ?? `Save did not complete (state ${data.state}).`);
      setNotice("Provider paths saved.");
      await load(true); onUpdated();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Provider CLI paths could not be saved.");
    } finally { setBusy(""); }
  }

  async function registerProject(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!settings) return;
    setBusy("register"); setError(""); setNotice("");
    try {
      const response = await fetch("/api/v1/projects", {
        method: "POST",
        headers: { "content-type": "application/json", "idempotency-key": crypto.randomUUID(), "if-match": `"${settings.revision}"` },
        body: JSON.stringify({ name: newProjectName.trim(), path: newProjectPath.trim(), revision: settings.revision }),
      });
      const data = await response.json();
      if (!response.ok) throw new Error((data as ApiError).error?.message ?? "Project could not be registered.");
      if (data.state !== "succeeded") throw new Error(data.error ?? `Registration did not complete (state ${data.state}).`);
      setNotice(`Project registered: ${newProjectName.trim()}`);
      setNewProjectName(""); setNewProjectPath("");
      await load(); onUpdated();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Project could not be registered.");
    } finally { setBusy(""); }
  }

  async function chooseFolder() {
    setBusy("folder"); setError(""); setNotice("");
    try {
      const response = await fetch("/api/v1/projects/folder-picker", { method: "POST" });
      const data = await response.json();
      if (!response.ok) throw new Error((data as ApiError).error?.message ?? "Folder selection failed.");
      if (!data.cancelled) {
        setNewProjectPath(data.path);
        setNewProjectName((current) => current || data.name || "");
      }
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Folder selection failed.");
    } finally { setBusy(""); }
  }

  async function refreshProviderStatus() {
    setBusy("refresh"); setError("");
    try {
      const response = await fetch("/api/v1/providers", { cache: "no-store" });
      const data = await response.json();
      if (!response.ok) throw new Error((data as ApiError).error?.message ?? "Provider status is unavailable.");
      setProviders(data);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Provider status is unavailable.");
    } finally { setBusy(""); }
  }

  const global = globalDraft;
  return <details className="card action-desk-configuration" id="action-desk-configuration" ref={configurationRef}>
    <summary>Project and runtime setup</summary>
    <p className="muted">Register repositories and edit project, provider, and runtime settings without leaving the Action Desk.</p>
    {error && <p className="error" role="alert">{error}</p>}
    {notice && <p className="success" role="status">{notice}</p>}
    {revisionConflict && <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void load(true)}>
      Discard unsaved configuration drafts and reload
    </button>}
    {!settings ? <p className="muted">Loading configuration…</p> : <>
      <section className="list">
        <h3>Register local repository</h3>
        <p className="muted">Registration inspects Git metadata and known build files. It does not run repository commands.</p>
        <form className="form-grid" onSubmit={(event) => void registerProject(event)}>
          <label>New project name<input className="input" value={newProjectName} maxLength={64} required
            onChange={(event) => setNewProjectName(event.target.value)} /></label>
          <label>Repository root path<div className="input-row">
            <input className="input" value={newProjectPath} required placeholder="/Users/you/Projects/example"
              onChange={(event) => setNewProjectPath(event.target.value)} />
            <button className="button secondary" type="button" onClick={() => void chooseFolder()} disabled={Boolean(busy)}>
              {busy === "folder" ? "Opening…" : "Choose folder"}
            </button>
          </div></label>
          <button className="button" type="submit" disabled={Boolean(busy) || revisionConflict}>
            {busy === "register" ? "Registering…" : "Register project"}
          </button>
        </form>
      </section>
      <section className="list">
        <h3>Runtime settings</h3>
        <form className="form-grid" onSubmit={(event) => void saveGlobalSettings(event)}>
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
          <button className="button" type="submit" disabled={Boolean(busy) || revisionConflict}>
            {busy === "global" ? "Saving…" : "Save runtime settings"}
          </button>
        </form>
      </section>
      <section className="list">
        <div className="row"><div><h3>Provider CLI paths</h3><p className="muted">Availability checks report executable versions only; they do not run models or check sign-in state.</p></div>
          <button className="button secondary" type="button" disabled={Boolean(busy)} onClick={() => void refreshProviderStatus()}>
            {busy === "refresh" ? "Refreshing…" : "Refresh CLI status"}
          </button>
        </div>
        <form className="form-grid" onSubmit={(event) => void saveProviderPaths(event)}>
          {(["codex_bin", "claude_bin", "cursor_agent_bin"] as const).map((field) => {
            const provider = providers.find((item) => item.provider === field.replace("_bin", "").replace("_agent", "-agent"));
            return <label key={field}>{field === "cursor_agent_bin" ? "Cursor Agent" : field === "claude_bin" ? "Claude Code" : "Codex"} executable
              <input className="input" value={providerDraft[field] ?? ""} required
                onChange={(event) => setProviderDraft((old) => ({ ...old, [field]: event.target.value }))} />
              <span className="muted">{provider?.available ? provider.version ?? "Version unavailable" : "CLI not found"}</span>
              {provider?.limitations.map((limitation) => <span className="muted" key={limitation}>{limitation}</span>)}
            </label>;
          })}
          <button className="button" type="submit" disabled={Boolean(busy) || revisionConflict}>
            {busy === "providers" ? "Saving…" : "Save provider CLI paths"}
          </button>
        </form>
      </section>
    </>}
  </details>;
}

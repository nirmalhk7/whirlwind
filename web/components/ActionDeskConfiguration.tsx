"use client";

import { FormEvent, useCallback, useEffect, useRef, useState } from "react";

import { deskError, deskRequest, loadDeskConfiguration, mergeDeskDrafts, runtimeChanges, saveDeskConfiguration, RuntimeFields, ProviderFields, type DeskSettings, type ProviderStatus } from "./ActionDeskConfigurationFields";

function useDeskSettings(configurationRevision: string) {
  const [settings, setSettings] = useState<DeskSettings | null>(null);
  const settingsRef = useRef<DeskSettings | null>(null);
  const [globalDraft, setGlobalDraft] = useState<Record<string, any>>({});
  const globalDraftRef = useRef(globalDraft);
  globalDraftRef.current = globalDraft;
  const [providerDraft, setProviderDraft] = useState<Record<string, string>>({});
  const providerDraftRef = useRef(providerDraft);
  providerDraftRef.current = providerDraft;
  const [providers, setProviders] = useState<ProviderStatus[]>([]);
  const [revisionConflict, setRevisionConflict] = useState(false);
  const revisionConflictRef = useRef(false);
  const [error, setError] = useState("");

  const load = useCallback(async (resetDrafts = false) => {
    const { settings: nextSettings, providers: nextProviders } = await loadDeskConfiguration();
    const previous = settingsRef.current;
    const { mergedGlobal, mergedProviders, conflicts } = mergeDeskDrafts(nextSettings, previous, resetDrafts, globalDraftRef.current, providerDraftRef.current);
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
    void load().catch((reason) => setError(deskError(reason, "Settings are unavailable.")));
  }, [load]);

  useEffect(() => {
    if (settings && configurationRevision && configurationRevision !== settings.revision) {
      void load().catch((reason) => setError(deskError(reason, "Settings are unavailable.")));
    }
  }, [configurationRevision, load, settings]);

  return { settings, globalDraft, setGlobalDraft, providerDraft, setProviderDraft, providers, setProviders, revisionConflict, error, setError, load };
}

function useDeskActions(configuration: ReturnType<typeof useDeskSettings>, onUpdated: () => void) {
  const { settings, globalDraft, providerDraft, setProviders, setError, load } = configuration;
  const [newProjectName, setNewProjectName] = useState("");
  const [newProjectPath, setNewProjectPath] = useState("");
  const [busy, setBusy] = useState("");
  const [notice, setNotice] = useState("");

  async function perform(operation: string, fallback: string, action: () => Promise<void>, clearNotice = true) {
    setBusy(operation); setError("");
    if (clearNotice) setNotice("");
    try { await action(); }
    catch (reason) { setError(deskError(reason, fallback)); }
    finally { setBusy(""); }
  }

  async function saveSettings(event: FormEvent<HTMLFormElement>, section: "global" | "providers") {
    event.preventDefault();
    if (!settings) return;
    const messages = {
      global: ["Runtime settings could not be saved.", "Runtime settings saved. New executions use the updated configuration."],
      providers: ["Provider CLI paths could not be saved.", "Provider paths saved."],
    };
    const [fallback, notice] = messages[section];
    await perform(section, fallback, async () => {
      const changes = section === "global" ? runtimeChanges(globalDraft) : { global: providerDraft };
      await saveDeskConfiguration("/api/v1/settings", "PATCH", { changes }, { revision: settings.revision, fallback, operation: "Save" });
      setNotice(notice);
      await load(true); onUpdated();
    });
  }

  async function registerProject(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!settings) return;
    await perform("register", "Project could not be registered.", async () => {
      await saveDeskConfiguration("/api/v1/projects", "POST", { name: newProjectName.trim(), path: newProjectPath.trim(), revision: settings.revision }, { revision: settings.revision, fallback: "Project could not be registered.", operation: "Registration" });
      setNotice(`Project registered: ${newProjectName.trim()}`);
      setNewProjectName(""); setNewProjectPath("");
      await load(); onUpdated();
    });
  }

  async function chooseFolder() {
    await perform("folder", "Folder selection failed.", async () => {
      const data = await deskRequest("/api/v1/projects/folder-picker", "Folder selection failed.", { method: "POST" });
      if (!data.cancelled) {
        setNewProjectPath(data.path);
        setNewProjectName((current) => current || data.name || "");
      }
    });
  }

  async function refreshProviderStatus() {
    await perform("refresh", "Provider status is unavailable.", async () => {
      setProviders(await deskRequest("/api/v1/providers", "Provider status is unavailable.", { cache: "no-store" }));
    }, false);
  }

  return { newProjectName, setNewProjectName, newProjectPath, setNewProjectPath, busy, notice, saveSettings, registerProject, chooseFolder, refreshProviderStatus };
}

export function ActionDeskConfiguration({ onUpdated, configurationRevision }: {
  onUpdated: () => void;
  configurationRevision: string;
}) {
  const configuration = useDeskSettings(configurationRevision);
  const { settings, globalDraft: global, setGlobalDraft, providerDraft, setProviderDraft, providers, revisionConflict, error, load } = configuration;
  const { newProjectName, setNewProjectName, newProjectPath, setNewProjectPath, busy, notice, saveSettings, registerProject, chooseFolder, refreshProviderStatus } = useDeskActions(configuration, onUpdated);
  const configurationRef = useRef<HTMLDetailsElement>(null);
  useEffect(() => {
    if (window.location.hash === "#action-desk-configuration" && configurationRef.current) {
      configurationRef.current.open = true;
    }
  }, []);
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
        <form className="form-grid" onSubmit={(event) => void saveSettings(event, "global")}>
          <RuntimeFields global={global} setGlobalDraft={setGlobalDraft} />
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
        <form className="form-grid" onSubmit={(event) => void saveSettings(event, "providers")}>
          <ProviderFields providers={providers} providerDraft={providerDraft} setProviderDraft={setProviderDraft} />
          <button className="button" type="submit" disabled={Boolean(busy) || revisionConflict}>
            {busy === "providers" ? "Saving…" : "Save provider CLI paths"}
          </button>
        </form>
      </section>
    </>}
  </details>;
}

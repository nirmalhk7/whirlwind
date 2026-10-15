"use client";

import { useEffect, useRef, useState } from "react";
import { CommandIcon } from "./CommandShell";

type DirectoryListing = {
  path: string;
  parent: string;
  entries: { name: string; path: string }[];
};

async function readDirectory(path: string, signal: AbortSignal): Promise<DirectoryListing> {
  const query = path ? `?path=${encodeURIComponent(path)}` : "";
  const response = await fetch(`/api/v1/directories${query}`, { signal, cache: "no-store" });
  const result = await response.json();
  if (!response.ok) throw new Error(result.error?.message ?? "Folder unavailable. Choose another location.");
  return result;
}

function useDirectoryBrowser(initialPath: string) {
  const [request, setRequest] = useState({ path: initialPath });
  const [draft, setDraft] = useState(initialPath);
  const [directory, setDirectory] = useState<DirectoryListing | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError("");
    setDirectory(null);
    void readDirectory(request.path, controller.signal)
      .then((result) => {
        if (controller.signal.aborted) return;
        setDirectory(result);
        setDraft(result.path);
      })
      .catch((reason) => {
        if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : "Folder unavailable.");
      })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [request]);

  const browse = (path: string) => setRequest({ path });
  return { draft, setDraft, directory, loading, error, browse };
}

function FolderPicker({ initialPath, onSelect, onCancel }: {
  initialPath: string;
  onSelect: (path: string) => void;
  onCancel: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const { draft, setDraft, directory, loading, error, browse } = useDirectoryBrowser(initialPath);
  useEffect(() => {
    const element = dialog.current;
    element?.showModal();
    return () => element?.close();
  }, []);

  const cancel = () => { dialog.current?.close(); onCancel(); };
  const select = () => {
    if (!directory) return;
    dialog.current?.close();
    onSelect(directory.path);
  };

  return (
    <dialog ref={dialog} className="folder-dialog" aria-labelledby="folder-picker-title" onCancel={cancel}>
      <header className="folder-dialog-header">
        <h2 id="folder-picker-title">Select repository folder</h2>
        <p>Choose a folder on this machine.</p>
      </header>
      <div className="folder-navigation">
        <button className="button" type="button" disabled={loading} onClick={() => browse("")}>Home</button>
        <button className="button" type="button" disabled={loading || !directory || directory.path === directory.parent} onClick={() => directory && browse(directory.parent)}>Up one level</button>
      </div>
      <div className="folder-location">
        <label htmlFor="folder-location">Folder location</label>
        <div className="folder-input-row">
          <input id="folder-location" value={draft} disabled={loading} onChange={(event) => setDraft(event.target.value)} onKeyDown={(event) => {
            if (event.key === "Enter") { event.preventDefault(); browse(draft); }
          }} />
          <button className="button" type="button" disabled={loading} onClick={() => browse(draft)}>Go</button>
        </div>
      </div>
      <div className="folder-list" aria-busy={loading}>
        {loading && <p role="status">Loading folders…</p>}
        {error && <p className="error" role="alert">{error}</p>}
        {directory && <>
          <ul aria-label="Folders">
            {directory.entries.map((entry) => <li key={entry.path}>
              <button type="button" onClick={() => browse(entry.path)}><CommandIcon name="projects" /><span>{entry.name}</span><CommandIcon name="arrow" /></button>
            </li>)}
          </ul>
          {directory.entries.length === 0 && <p>No subfolders. You can select this folder.</p>}
        </>}
      </div>
      <footer className="folder-dialog-footer">
        <p>Current folder <code>{directory?.path ?? "Choose a location"}</code></p>
        <div className="whirlwind-actions">
          <button className="button" type="button" onClick={cancel}>Cancel</button>
          <button className="button primary" type="button" disabled={loading || !directory} onClick={select}>Use this folder</button>
        </div>
      </footer>
    </dialog>
  );
}

export function RepositoryFolderField({ disabled, onPathChange }: { disabled: boolean; onPathChange?: (path: string) => void }) {
  const [value, setValue] = useState("");
  const [open, setOpen] = useState(false);
  const changePath = (path: string) => { setValue(path); onPathChange?.(path); };
  return (
    <div className="repository-folder-field">
      <label htmlFor="repository-path">Repository path</label>
      <div className="folder-input-row">
        <input id="repository-path" name="path" placeholder="Choose a repository folder" value={value} onChange={(event) => changePath(event.target.value)} required />
        <button className="button" type="button" disabled={disabled} onClick={() => setOpen(true)}><CommandIcon name="projects" />Choose folder</button>
      </div>
      {open && <FolderPicker initialPath={value} onCancel={() => setOpen(false)} onSelect={(path) => { changePath(path); setOpen(false); }} />}
    </div>
  );
}

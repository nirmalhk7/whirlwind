"use client";

import { useCallback, useLayoutEffect, useRef, useState } from "react";
import { YamlIndentGuides } from "./YamlIndentGuides";
import { api } from "./controlApi";

export type SettingsFileView = { path: string; content: string; loaded_at?: string; revision?: string };
type Props = {
  startup: SettingsFileView;
  busy: boolean;
  perform: (action: () => Promise<unknown>, message: string) => Promise<void>;
};

export function SettingsFileEditor({ startup, busy, perform }: Props) {
  const [editing, setEditing] = useState(false);
  const [file, setFile] = useState<SettingsFileView | null>(null);
  const [draft, setDraft] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const dirty = file !== null && draft !== file.content;
  const filename = startup.path.split(/[\\/]/).at(-1) || "settings.yaml";

  async function openFile() {
    setEditing(true);
    setLoading(true);
    setError("");
    setSaved(false);
    try {
      const current = await api<SettingsFileView>("settings/file?view=file");
      setFile(current);
      setDraft(current.content);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Could not read file. Try reloading it.");
    } finally { setLoading(false); }
  }

  async function save() {
    if (!file || !dirty) return;
    setError("");
    setSaved(false);
    await perform(async () => {
      try {
        await api("settings/file", "PATCH", { content: draft, revision: file.revision });
        // Return to the immutable startup view. Reopening reads the saved file's new revision.
        setEditing(false);
        setFile(null);
        setSaved(true);
      } catch (reason) {
        setError(reason instanceof Error ? reason.message : "Could not save file. Your edits are preserved.");
        throw reason;
      }
    }, "YAML saved. Startup snapshot unchanged.");
  }

  return <section className="yaml-settings" aria-labelledby="yaml-heading">
    <SettingsFileHeading filename={filename} editing={editing} busy={busy} loading={loading} dirty={dirty} openFile={openFile} cancel={() => { setEditing(false); setError(""); setFile(null); }} />
    <SettingsFileProvenance startup={startup} editing={editing} />
    <SettingsFileDescription editing={editing} />
    <SettingsFileFeedback saved={saved} error={error} />
    {editing
      ? <SettingsFileForm busy={busy} loading={loading} dirty={dirty} file={file} draft={draft} setDraft={setDraft} clearSaved={() => setSaved(false)} openFile={openFile} save={save} />
      : <SettingsFileSnapshot startup={startup} />}
  </section>;
}

function SettingsFileHeading({ filename, editing, busy, loading, dirty, openFile, cancel }: {
  filename: string; editing: boolean; busy: boolean; loading: boolean; dirty: boolean;
  openFile: () => Promise<void>; cancel: () => void;
}) {
  return <div className="yaml-heading-row">
    <div className="yaml-file-title"><h2 id="yaml-heading">{filename}</h2><span className="badge">{editing ? "Editing file" : "Startup snapshot"}</span></div>
    {!editing && <button className="button primary" type="button" disabled={busy} onClick={() => void openFile()}>Edit file</button>}
    {editing && <button className="button" type="button" disabled={busy || loading} onClick={cancel}>{dirty ? "Discard edits" : "Back to snapshot"}</button>}
  </div>;
}

function SettingsFileProvenance({ startup, editing }: { startup: SettingsFileView; editing: boolean }) {
  return <dl className="yaml-provenance">
    <div><dt>Source</dt><dd><code>{startup.path}</code></dd></div>
    {!editing && startup.loaded_at && <div><dt>Loaded</dt><dd><time dateTime={startup.loaded_at}>{new Date(startup.loaded_at).toLocaleString()}</time></dd></div>}
  </dl>;
}

function SettingsFileFeedback({ saved, error }: { saved: boolean; error: string }) {
  return <>
    {saved && <p className="yaml-saved" role="status">File saved. Showing the original startup snapshot.</p>}
    {error && <div className="error" role="alert"><p>{error}</p><p>Your edits are preserved. Copy them before reloading the file.</p></div>}
  </>;
}

function SettingsFileDescription({ editing }: { editing: boolean }) {
  const text = editing
    ? "Editing the current file at this path. Valid saves apply without restart. The startup snapshot stays unchanged."
    : "Exact file contents captured when this process started. Later file edits and saves do not change this snapshot.";
  return <p className="yaml-explanation" id="yaml-explanation">{text}</p>;
}

function SettingsFileForm({ busy, loading, dirty, file, draft, setDraft, clearSaved, openFile, save }: {
  busy: boolean; loading: boolean; dirty: boolean; file: SettingsFileView | null; draft: string;
  setDraft: (value: string) => void; clearSaved: () => void; openFile: () => Promise<void>; save: () => Promise<void>;
}) {
  const editor = useRef<HTMLTextAreaElement>(null);
  const guides = useRef<HTMLPreElement>(null);
  const syncGuides = useCallback(() => {
    if (!editor.current || !guides.current) return;
    const field = editor.current;
    const layer = guides.current;
    layer.style.width = `${field.clientWidth}px`;
    const lines = layer.firstElementChild as HTMLElement | null;
    if (lines) lines.style.width = `${field.scrollWidth - parseFloat(getComputedStyle(field).paddingLeft)}px`;
    layer.scrollTop = field.scrollTop;
    layer.scrollLeft = field.scrollLeft;
  }, []);
  useLayoutEffect(syncGuides, [draft, syncGuides]);
  useLayoutEffect(() => {
    if (!editor.current) return;
    const observer = new ResizeObserver(syncGuides);
    observer.observe(editor.current);
    return () => observer.disconnect();
  }, [file, loading, syncGuides]);
  return <form onSubmit={event => { event.preventDefault(); void save(); }}>
    {loading ? <div className="yaml-loading" role="status">Reading source file…</div> : file && <>
      <label className="yaml-editor-label" htmlFor="settings-yaml">YAML file contents</label>
      <div className="yaml-editor-shell">
        <pre className="yaml-editor-guides" aria-hidden="true" ref={guides}><YamlIndentGuides content={draft} trailingLine /></pre>
        <textarea id="settings-yaml" className="yaml-editor" aria-describedby="yaml-explanation" ref={editor} value={draft} disabled={busy} wrap="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" onScroll={syncGuides} onChange={event => { setDraft(event.target.value); clearSaved(); }} />
      </div>
    </>}
    <div className="yaml-editor-actions">
      <span role="status">{loading ? "Loading file" : dirty ? "Unsaved changes" : "No unsaved changes"}</span>
      <div><button className="button" type="button" disabled={busy || loading} onClick={() => void openFile()}>{dirty ? "Reload file (discard edits)" : "Reload file"}</button><button className="button primary" type="submit" disabled={busy || loading || !dirty}>{busy ? "Saving…" : "Save file"}</button></div>
    </div>
  </form>;
}

function SettingsFileSnapshot({ startup }: { startup: SettingsFileView }) {
  const lineCount = startup.content.split("\n").length - (startup.content.endsWith("\n") ? 1 : 0);
  return <div className="yaml-snapshot">
    <pre aria-label="Startup YAML" tabIndex={0}><YamlIndentGuides content={startup.content} /><code>{startup.content}</code></pre>
    <div className="yaml-snapshot-footer"><span>Read-only startup snapshot</span><span>{lineCount} lines · YAML</span></div>
  </div>;
}

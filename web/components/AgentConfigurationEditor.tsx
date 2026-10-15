import { useEffect, useRef, useState, type FormEvent } from "react";
import { api } from "./controlApi";

export type Source = { id: string; name: string; kind: string; capabilities: string[]; available: boolean; quota: string; command?: string[]; model_command?: string[] };
export type Resource<T> = { id: string; version: number; data: T };
type Thinking = { config_id: string; value: string };
export type Choice = { provider: string; model: string; thinking?: Thinking };
type ThinkingOption = { config_id: string; name: string; current: string; options: Model[] };
export const catalogKey = (provider: string, model = "") => JSON.stringify([provider, model]);
const thinkingValue = (thinking?: Thinking) => thinking ? JSON.stringify({ config_id: thinking.config_id, value: thinking.value }) : "";
const testKey = (role: string, choice: Choice) => JSON.stringify([role, choice.provider, choice.model, thinkingValue(choice.thinking)]);
export type Config = { role: string; chain: Choice[] };
type Model = { id: string; name: string };
export type Operation = { stage: "running" | "complete" | "failed"; models?: Model[]; thinking_options?: ThinkingOption[]; text?: string; error?: string; elapsed_ms?: number };
export type Catalog = { loading: boolean; models: Model[]; thinkingOptions: ThinkingOption[]; error?: string };
const names: Record<string, string> = { ideate: "Ideation Agent", plan: "Planner Agent", code: "Coding Agent", review: "Reviewer Agent" };

type EditorProps = {
  record: Resource<Config>; sources: Resource<Source>[]; catalogs: Record<string, Catalog>; tests: Record<string, Operation>;
  loadModels: (id: string, model?: string, force?: boolean) => Promise<void>;
  test: (key: string, choice: Choice) => Promise<void>; onSaved: (saved: Resource<Config>) => void;
};
type ChoiceProps = Pick<EditorProps, "catalogs" | "tests" | "loadModels" | "test"> & {
  role: string; choice: Choice; index: number; count: number; providers: Resource<Source>[];
  update: (index: number, value: Partial<Choice>) => void;
  move: (index: number, offset: number) => void; remove: (index: number) => void;
};

function catalogReady(catalog?: Catalog): boolean {
  return Boolean(catalog && !catalog.loading && !catalog.error);
}

function advertisedThinking(catalog: Catalog | undefined, thinking?: Thinking): boolean {
  return Boolean(catalog?.thinkingOptions.some((option) => option.config_id === thinking?.config_id && option.options.some((value) => value.id === thinking?.value)));
}

function invalidThinking(choice: Choice, catalog?: Catalog): boolean {
  if (choice.thinking === undefined) return false;
  return !catalogReady(catalog) || !advertisedThinking(catalog, choice.thinking);
}

function useModelOptions(chain: Choice[], loadModels: EditorProps["loadModels"]) {
  useEffect(() => {
    const timer = setTimeout(() => {
      for (const choice of chain) {
        if (choice.provider && choice.model.trim()) void loadModels(choice.provider, choice.model.trim());
      }
    }, 300);
    return () => clearTimeout(timer);
  }, [chain, loadModels]);
}

function useAgentEditor({ record, onSaved, loadModels }: Pick<EditorProps, "record" | "onSaved" | "loadModels">) {
  const initialChain = () => record.data.chain.length ? record.data.chain : [{ provider: "", model: "" }];
  const [chain, setChain] = useState<Choice[]>(initialChain);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const observedVersion = useRef(record.version);
  useEffect(() => {
    if (observedVersion.current === record.version) return;
    observedVersion.current = record.version;
    setChain(initialChain()); setSaved(false); setError("");
  }, [record.version, record.data.chain]);
  const update = (index: number, value: Partial<Choice>) => {
    setSaved(false);
    setChain((current) => current.map((choice, n) => n === index ? { ...choice, ...value } : choice));
  };
  useModelOptions(chain, loadModels);
  const move = (index: number, offset: number) => {
    setSaved(false);
    setChain((current) => { const next = [...current]; [next[index], next[index + offset]] = [next[index + offset], next[index]]; return next; });
  };
  const remove = (index: number) => { setSaved(false); setChain((current) => current.filter((_, n) => n !== index)); };
  const add = () => { setSaved(false); setChain((current) => [...current, { provider: "", model: "" }]); };
  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); setBusy(true); setError("");
    try {
      const result = await api<Resource<Config>>(`agents/${record.id}`, "PATCH", { chain }, record.version);
      observedVersion.current = result.version; onSaved(result); setChain(result.data.chain); setSaved(true);
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not save agent"); }
    finally { setBusy(false); }
  };
  return { chain, busy, error, saved, update, move, remove, add, save };
}

function ThinkingSelect({ label, choice, catalog, update }: { label: string; choice: Choice; catalog?: Catalog; update: (value: Partial<Choice>) => void }) {
  const disabled = [!choice.model.trim(), catalog?.loading, ![catalog?.thinkingOptions.length, choice.thinking].some(Boolean)].some(Boolean);
  const savedOption = Boolean(choice.thinking) && !advertisedThinking(catalog, choice.thinking);
  return <label>{label} thinking level
    <select aria-label={`${label} thinking level`} value={thinkingValue(choice.thinking)} disabled={disabled}
      onChange={(event) => update({ thinking: event.target.value ? JSON.parse(event.target.value) as Thinking : undefined })}>
      <option value="">Provider default</option>
      {savedOption && <option value={thinkingValue(choice.thinking)}>{choice.thinking?.value} (saved)</option>}
      {catalog?.thinkingOptions.map((option) => <optgroup key={option.config_id} label={option.name || "Thinking level"}>
        {option.options.map((value) => <option key={value.id} value={thinkingValue({ config_id: option.config_id, value: value.id })}>{value.name || value.id}</option>)}
      </optgroup>)}
    </select>
  </label>;
}

function thinkingStatus(catalog?: Catalog): string {
  if (!catalog) return "Choose a model to discover thinking levels.";
  if (catalog.loading) return "Loading thinking levels…";
  if (catalog.error) return "Thinking levels unavailable. Reload options to retry.";
  return catalog.thinkingOptions.length
    ? "Provider default keeps its current setting. Explicit thinking uses ACP."
    : "Provider does not advertise thinking levels for this model.";
}

function CatalogStatus({ choice, catalog }: { choice: Choice; catalog?: Catalog }) {
  const ready = catalogReady(catalog);
  const unavailable = ready && invalidThinking(choice, catalog);
  return <>
    <p className="muted" role="status">{thinkingStatus(catalog)}</p>
    {unavailable && <p className="error" role="alert">Saved thinking level is unavailable for this model. Choose an advertised level or Provider default.</p>}
  </>;
}

function ModelStatus({ catalog }: { catalog?: Catalog }) {
  const ready = catalogReady(catalog);
  const hasModels = Boolean(catalog?.models.length);
  return <>
    {catalog?.error && <p className="error" role="alert">Could not load models: {catalog.error}</p>}
    {catalog && !catalog.loading && hasModels && <p className="muted" role="status">{catalog.models.length} models reported by source. Choose one in the model field.</p>}
    {ready && !hasModels && <p className="muted">Source did not advertise models. Enter model ID and test it before saving.</p>}
  </>;
}

function TestResult({ result, label }: { result?: Operation; label: string }) {
  return <div aria-live="polite">
    {result?.stage === "running" && <p className="muted" role="status">Sending “Hello World” to selected model…</p>}
    {result?.stage === "complete" && <><p className="provider-connection-success" role="status">Connected · {((result.elapsed_ms ?? 0) / 1000).toFixed(1)}s</p><pre aria-label={`${label} model reply`}>{result.text}</pre></>}
    {result?.stage === "failed" && <p className="error" role="alert">Connection failed: {result.error}</p>}
  </div>;
}

function ChoiceActions({ choice, index, count, ...props }: ChoiceProps & { catalog?: Catalog; result?: Operation; label: string; testID: string }) {
  const { catalog, result, label, loadModels, test, testID, move, remove } = props;
  const testing = result?.stage === "running";
  const testDisabled = [!choice.provider, !choice.model.trim(), invalidThinking(choice, catalog), testing].some(Boolean);
  return <div className="whirlwind-actions">
    <button type="button" className="button" disabled={!choice.provider || catalog?.loading} onClick={() => void loadModels(choice.provider, choice.model.trim(), true)}>{catalog?.loading ? "Loading models…" : "Load models"}</button>
    <button type="button" className="button" disabled={testDisabled} onClick={() => void test(testID, choice)}>{testing ? "Testing…" : "Test model"}</button>
    {count > 1 && <>
      <button type="button" className="button" disabled={index === 0} aria-label={`Move ${label} up`} onClick={() => move(index, -1)}>Move up</button>
      <button type="button" className="button" disabled={index === count - 1} aria-label={`Move ${label} down`} onClick={() => move(index, 1)}>Move down</button>
      <button type="button" className="button" aria-label={`Remove ${label}`} onClick={() => remove(index)}>Remove</button>
    </>}
  </div>;
}

function providerLabel(source: Resource<Source>): string {
  if (!source.data.available) return `${source.data.name} — unavailable`;
  if (source.data.quota === "exhausted") return `${source.data.name} — quota exhausted`;
  return source.data.name;
}

function AgentChoice(props: ChoiceProps) {
  const { choice, index, role, catalogs, providers, tests, update } = props;
  const label = index === 0 ? "Primary" : `Fallback ${index}`;
  const catalog = catalogs[catalogKey(choice.provider, choice.model.trim())];
  const modelOptions = catalog?.models ?? catalogs[catalogKey(choice.provider)]?.models ?? [];
  const key = testKey(role, choice);
  const result = tests[key];
  const listID = `models-${role}-${index}`;
  return <div className="agent-choice">
    <h3>{label}</h3>
    <div className="agent-choice-fields">
      <label>{label} provider
        <select required value={choice.provider} onChange={(event) => update(index, { provider: event.target.value, model: "", thinking: undefined })}>
          <option value="">Choose provider</option>
          {providers.map((source) => <option key={source.id} value={source.id}>{providerLabel(source)}</option>)}
        </select>
      </label>
      <label>{label} model
        <input required value={choice.model} list={listID} placeholder="Choose or enter model ID" onChange={(event) => update(index, { model: event.target.value, thinking: undefined })} />
        <datalist id={listID}>{modelOptions.map((model) => <option key={model.id} value={model.id}>{model.name}</option>)}</datalist>
      </label>
      <ThinkingSelect label={label} choice={choice} catalog={catalog} update={(value) => update(index, value)} />
    </div>
    <CatalogStatus choice={choice} catalog={catalog} />
    <ChoiceActions {...props} catalog={catalog} result={result} label={label} testID={key} />
    <ModelStatus catalog={catalog} />
    <TestResult result={result} label={label} />
  </div>;
}

export function AgentEditor(props: EditorProps) {
  const { record, sources, catalogs } = props;
  const state = useAgentEditor(props);
  const providers = sources.filter((item) => item.data.kind !== "unavailable" && item.data.capabilities?.includes(record.id));
  const configured = record.version > 0;
  const saveDisabled = !providers.length || state.chain.some((choice) => invalidThinking(choice, catalogs[catalogKey(choice.provider, choice.model.trim())]));
  return <form className="agent-config whirlwind-form" onSubmit={state.save} aria-label={names[record.id]}>
    <div className="row"><h3>{names[record.id]}</h3><span className="badge" data-availability={configured ? "available" : "unavailable"}>{configured ? "Configured" : "Not configured"}</span></div>
    {record.version === 0 && <p className="muted">Existing project routing stays active until you save this agent.</p>}
    {!providers.length && <p className="error">No providers support this role. Add or update a source in Providers.</p>}
    <fieldset disabled={state.busy}>
      {state.chain.map((choice, index) => <AgentChoice key={index} {...props} {...state} role={record.id} providers={providers} choice={choice} index={index} count={state.chain.length} />)}
      <div className="whirlwind-actions agent-config-actions">
        <button type="button" className="button" onClick={state.add}>Add fallback</button>
        <button className="button primary" disabled={saveDisabled}>{state.busy ? "Saving…" : "Save agent"}</button>
        {state.saved && <span className="provider-connection-success" role="status">Agent saved</span>}
      </div>
    </fieldset>
    {state.error && <p className="error" role="alert">{state.error} Reload page if configuration changed elsewhere.</p>}
  </form>;
}

"use client";

import { useWorkflowSettings, type Catalog, type Connection, type ProviderIdentity, type WorkflowConfiguration, type WorkflowSettingsProps } from "./useWorkflowSettings";

const modeName = (mode: string) => mode === "agentic" ? "Agentic" : mode === "personal" ? "Personal" : mode;

function WorkflowModeStatus({ workflow, error }: { workflow: WorkflowConfiguration | null; error: string }) {
  if (!workflow) return !error && <p className="muted" role="status">Reading workflow configuration…</p>;
  return <>
    <p>Active mode: <strong>{modeName(workflow.active_mode)}</strong></p>
    {workflow.restart_required && <p role="status">Restart required. Configured mode: <strong>{modeName(workflow.configured_mode)}</strong>. Current workflow remains in {modeName(workflow.active_mode)} mode until restart.</p>}
  </>;
}

function IntegrationConnection({ connection, providerID }: { connection: Connection; providerID: string }) {
  const status = !connection.enabled ? "Disabled" : connection.callable ? "Callable" : "Unavailable";
  return <li>
    <p><strong>{connection.name || connection.ref}</strong>{" "}<span className="badge">{status}</span></p>
    <p>Native reference: <code>{connection.ref}</code> · Provider: <code>{connection.provider || providerID}</code>{connection.kind && ` · ${connection.kind}`}</p>
    {connection.error && <p className="error" role="alert">{connection.error}</p>}
  </li>;
}

type ProviderInventoryProps = {
  provider: ProviderIdentity;
  catalog?: Catalog;
  disabled: boolean;
  refreshing: string;
  refreshProvider: (provider: ProviderIdentity) => Promise<void>;
};

function ProviderInventory({ provider, catalog, disabled, refreshing, refreshProvider }: ProviderInventoryProps) {
  return <article className="action-card">
    <div className="row"><h3>{provider.name || provider.id}</h3><button className="button" type="button" disabled={disabled} onClick={() => void refreshProvider(provider)}>{refreshing === provider.id ? "Refreshing integrations…" : "Refresh integrations"}</button></div>
    <p className="muted">Provider ID: <code>{provider.id}</code> · {provider.kind}</p>
    {catalog?.error && <p className="error" role="alert">{catalog.error}</p>}
    {catalog && !catalog.integrations.length && <p className="muted">No connected integrations reported. Refresh to discover connections or check provider setup.</p>}
    {!!catalog?.integrations.length && <ul>{catalog.integrations.map((connection, index) => <IntegrationConnection connection={connection} providerID={provider.id} key={`${connection.ref}:${index}`} />)}</ul>}
  </article>;
}

export function WorkflowSettings(props: WorkflowSettingsProps) {
  const { workflow, workflowError, inventoryError, identities, catalogs, loading, refreshing, refreshProvider, reloadStatus } = useWorkflowSettings(props);
  const disabled = props.busy || loading || Boolean(refreshing);
  const noProviders = !loading && !inventoryError && identities.length === 0;
  return <section className="settings-section" aria-labelledby="workflow-settings-heading">
    <div className="row"><h2 id="workflow-settings-heading">Workflow mode and integrations</h2><button className="button" type="button" disabled={disabled} onClick={reloadStatus}>Reload workflow status</button></div>
    {workflowError && <p className="error" role="alert">{workflowError}</p>}
    <WorkflowModeStatus workflow={workflow} error={workflowError} />
    <p className="muted">Change mode only in the on-disk <code>settings.yaml</code>, then restart Whirlwind. Restarting into a different mode cancels all incomplete tasks; worktrees, diffs, plans, and history remain available.</p>
    <p className="muted">Configure integration provider IDs, native references, scopes, and publication destinations in <code>settings.yaml</code>. Integrations stay owned by their provider. Personal mode uses GitHub through <code>gh</code>; inspecting integrations here does not enable their use by Personal workflows.</p>
    {inventoryError && <p className="error" role="alert">{inventoryError}</p>}
    {loading && <p className="muted" role="status">Reading cached integration inventories…</p>}
    {noProviders && <p className="muted">No providers configured. Add providers in the YAML file to inspect native integrations.</p>}
    <div className="list">{identities.map(provider => <ProviderInventory provider={provider} catalog={catalogs[provider.id]} disabled={disabled} refreshing={refreshing} refreshProvider={refreshProvider} key={provider.id} />)}</div>
    {workflow?.integrations && <details><summary>Configured integration routing</summary><pre>{JSON.stringify(workflow.integrations, null, 2)}</pre></details>}
  </section>;
}

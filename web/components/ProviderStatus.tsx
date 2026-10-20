"use client";

import type { ProviderStatusSnapshot } from "./useProviderStatus";

const labels: Record<string, string> = { working: "Working", failed: "Not working", unavailable: "Unavailable", paused: "Checks paused", unknown: "Not checked" };

export function ProviderStatus({ snapshot, error }: { snapshot: ProviderStatusSnapshot | null; error: string }) {
  return <section className="provider-status" id="providers" aria-labelledby="provider-status-heading">
    <div className="provider-status-heading"><h2 id="provider-status-heading">Providers</h2>
      <span>{snapshot ? `Every ${snapshot.interval_seconds} seconds` : "Checking status…"}</span>
    </div>
    {snapshot && !error && <p className="provider-status-summary" role="status">{snapshot.providers.filter(p => p.status === "working").length} working · {snapshot.providers.filter(p => p.status === "failed").length} failed · {snapshot.providers.filter(p => p.status === "unavailable").length} unavailable{snapshot.stopped ? ` · ${snapshot.providers.filter(p => p.status === "paused").length} checks paused` : ""}</p>}
    <p className="muted">Inbox counts providers marked Working. Live provider checks. Configure <code>workers.provider_status_check_seconds</code> in the YAML file.</p>
    {snapshot?.stopped && <p className="muted">Connection checks paused while runners are stopped.</p>}
    {error && <p className="provider-status-error" role="alert">{error} {snapshot && "Last results may be outdated."}</p>}
    {!snapshot && !error && <p role="status" className="muted">Checking configured providers…</p>}
    {snapshot && snapshot.providers.length === 0 && <p className="muted">No providers configured. Add providers in the YAML file.</p>}
    {snapshot && <ul className="provider-status-list">{snapshot.providers.map(provider => <li key={provider.id}>
      <div className="provider-status-row"><h3>{provider.name}</h3><span className="provider-status-label" data-status={error ? "unknown" : provider.status}>{error ? "Status outdated" : labels[provider.status] ?? "Not checked"}</span></div>
      <p>{provider.detail}</p>
      <div className="provider-status-meta"><span>{provider.kind}</span>{provider.checked_at && <span>Checked <time dateTime={provider.checked_at}>{new Date(provider.checked_at).toLocaleTimeString()}</time></span>}</div>
    </li>)}</ul>}
  </section>;
}

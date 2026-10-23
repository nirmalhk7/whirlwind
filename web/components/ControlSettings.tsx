import { AgentConfiguration } from "./AgentConfiguration";
import { SettingsFileEditor, type SettingsFileView } from "./SettingsFileEditor";
import { ProviderSettings } from "./ProviderSettings";
import { ProviderStatus } from "./ProviderStatus";
import { WorkflowSettings } from "./WorkflowSettings";
import type { ProviderStatusSnapshot } from "./useProviderStatus";
import type { Resource, Provider, View } from "./controlTypes";

type Props = {
  view: View; busy: boolean; providers: Resource<Provider>[];
  startupSettings: SettingsFileView | null;
  providerStatus: { snapshot: ProviderStatusSnapshot | null; error: string };
  perform: (action: () => Promise<unknown>, message: string) => Promise<void>;
};

export function ControlSettings({ view, busy, providers, providerStatus, startupSettings, perform }: Props) {
  if (view === "settings") return <><ProviderStatus {...providerStatus} /><WorkflowSettings providers={providers} busy={busy} perform={perform} />{startupSettings && <SettingsFileEditor key={`${startupSettings.path}:${startupSettings.loaded_at}`} startup={startupSettings} busy={busy} perform={perform} />}</>;
  if (view === "agents") return <section className="settings-section" aria-labelledby="agents-heading"><h2 id="agents-heading">Agents</h2><AgentConfiguration providers={providers} /></section>;
  if (view === "providers") return <ProviderSettings providers={providers} busy={busy} perform={perform} />;
  return null;
}

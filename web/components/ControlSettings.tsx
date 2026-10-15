import { AgentConfiguration, type Config as AgentConfig } from "./AgentConfiguration";
import { SettingsForm, type WorkerSettings } from "./SettingsForm";
import { ProviderSettings } from "./ProviderSettings";
import { api } from "./controlApi";
import type { Resource, Provider, View } from "./controlTypes";

type Props = {
 view: View; busy: boolean; providers: Resource<Provider>[];
 agentConfigs: Resource<AgentConfig>[];
 settings: Resource<WorkerSettings> | null;
 perform: (action: () => Promise<unknown>, message: string) => Promise<void>;
};

export function ControlSettings({view, busy, providers, agentConfigs, settings, perform}: Props) {
 return <>
        {view === "settings" && <>
          <div className="settings-source"><p>Configuration lives in <code>settings.yaml</code> at the Whirlwind repository root. UI saves update it; valid file edits apply without restart.</p>
            <nav aria-label="Settings sections"><a href="#workers">Workers</a><a href="#agents">Agents</a><a href="#providers">Providers</a></nav>
          </div>
          <section id="workers" aria-label="Worker configuration">
        {view === "settings" && settings && <SettingsForm key={settings.version} settings={settings.data} busy={busy}
          onSave={(value) => void perform(() => api("settings", "PATCH", value, settings.version), "Settings updated")} />}
          </section>
        </>}
        {(view === "settings" || view === "agents") && <section id="agents" className="settings-section" aria-labelledby="agents-heading"><h2 id="agents-heading">Agents</h2><AgentConfiguration providers={providers} configurations={view === "settings" ? agentConfigs : undefined} /></section>}
        {(view === "settings" || view === "providers") && (
          <ProviderSettings providers={providers} busy={busy} perform={perform} />
        )}
 </>;
}

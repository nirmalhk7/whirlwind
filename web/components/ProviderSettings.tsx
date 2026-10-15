import type { FormEvent } from "react";
import { api } from "./controlApi";
import { ProviderConnectionTest } from "./ProviderConnectionTest";
import type { Resource, Provider } from "./controlTypes";

type Props = {
 providers: Resource<Provider>[];
 busy: boolean;
 perform: (action: () => Promise<unknown>, message: string) => Promise<void>;
};

function parseCommand(
  value: FormDataEntryValue | null,
  message: string,
): string[] {
  const command: unknown = JSON.parse(String(value));
  if (
    !Array.isArray(command) ||
    command.some((arg) => typeof arg !== "string")
  ) {
    throw new Error(message);
  }
  return command;
}

export function ProviderSettings({providers, busy, perform}: Props) {
  const registerProvider = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const f = new FormData(event.currentTarget);
    void perform(async () => {
      const command = parseCommand(
        f.get("command"),
        "Command must be JSON array of strings.",
      );
      return api("providers", "POST", {
        id: f.get("id"),
        name: f.get("name"),
        kind: "acp",
        command,
        capabilities: ["ideate", "plan", "code", "review", "permissions"],
      });
    }, "ACP provider registered");
  };
 return (
          <section id="providers" className="settings-section" aria-labelledby="providers-heading">
            <h2 id="providers-heading">Providers</h2>
            <section className="list">
              {providers.map((p) => (
                <article className="provider-row" key={p.id}>
                  <div className="row">
                    <h3>{p.data.name}</h3>
                    <span className="badge" data-availability={p.data.available ? "available" : "unavailable"}>
                      {p.data.available ? "Available" : "Unavailable"}
                    </span>
                  </div>
                  <p className="muted">
                    Quota: {p.data.quota} ·{" "}
                    {(p.data.capabilities ?? []).join(", ")}
                  </p>
                  {p.data.limitation && <p>{p.data.limitation}</p>}
                  <code>{p.data.command?.join(" ")}</code>
                  {p.data.kind !== "unavailable" && <ProviderConnectionTest key={JSON.stringify([p.id,p.data.kind,p.data.command,p.data.model_command])} entry={p} disabled={busy} />}
                  {p.data.kind !== "unavailable" && (
                    <details>
                      <summary>Edit provider setup</summary>
                      <form
                        className="whirlwind-form"
                        onSubmit={(event) => {
                          event.preventDefault();
                          const form = new FormData(event.currentTarget);
                          void perform(async () => {
                            const command = parseCommand(
                              form.get("command"),
                              "Command must be JSON string array.",
                            );
                            return api(
                              `providers/${p.id}`,
                              "PATCH",
                              { ...p.data, command },
                              p.version,
                            );
                          }, "Provider setup saved");
                        }}
                      >
                        <label>
                          Executable and arguments
                          <input
                            name="command"
                            key={p.version}
                            defaultValue={JSON.stringify(p.data.command)}
                            required
                          />
                        </label>
                        <p className="muted">
                          Authenticate using provider’s own login. Saving setup
                          rechecks executable availability.
                        </p>
                        <button className="button" disabled={busy}>
                          Save provider setup
                        </button>
                      </form>
                    </details>
                  )}
                </article>
              ))}
            </section>
            <section className="card">
              <h2>Add ACP provider</h2>
              <form className="whirlwind-form" onSubmit={registerProvider}>
                <label>
                  Provider ID
                  <input name="id" required />
                </label>
                <label>
                  Display name
                  <input name="name" required />
                </label>
                <label>
                  Executable and arguments (JSON array)
                  <input
                    name="command"
                    placeholder={'["your-agent", "acp"]'}
                    required
                  />
                </label>
                <button className="button primary" disabled={busy}>
                  Register provider
                </button>
              </form>
            </section>
          </section>
 );
}

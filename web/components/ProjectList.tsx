import { CommandIcon } from "./CommandShell";
import { api } from "./controlApi";
import type { Resource, Project, View } from "./controlTypes";

type Props = {
 view: View; loaded: boolean; error: string; busy: boolean;
 projects: Resource<Project>[];
 perform: (action: () => Promise<unknown>, message: string) => Promise<void>;
};

export function ProjectList({view, loaded, error, busy, projects, perform}: Props) {
 return (
          <section className="list command-projects">
            <h2>
              {view === "overview"
                ? "Project directions"
                : "Registered projects"}
            </h2>
            {loaded && !error && !projects.length && (
              <div className="command-empty">
                <h3>Your next ambition starts here.</h3>
                <p>Register a trusted repository. Choose a direction. Put Whirlwind to work.</p>
                <a className="button primary" href={view === "projects" ? "#project-registration" : "/projects"}>Register a project <CommandIcon name="arrow" /></a>
              </div>
            )}
            {projects.map(({ id, version, data: p }) => (
              <article className="action-card" key={id}>
                <div className="row">
                  <h3>{p.name}</h3>
                  <span className="badge">{p.policy_hash ? "Ready for execution" : "Ideation only · policy setup needed for execution"}</span>
                  <a
                    href={`https://github.com/${p.repo}`}
                    target="_blank"
                    rel="noreferrer"
                  >
                    {p.repo}
                  </a>
                </div>
                <p className="muted">
                  <code>{p.path}</code>
                </p>
                <div className="whirlwind-actions project-actions">
                  <button className="button project-policy-setup" disabled={busy} onClick={() => void perform(
                    () => api(`projects/${encodeURIComponent(id)}/policy`, "POST", {}, version),
                    "Execution policy configured and confirmed",
                  )}>
                    {p.policy_hash ? "Confirm committed policy" : "Finish execution setup"}
                  </button>
                  <button
                    type="button"
                    className="project-delete"
                    aria-label={`Delete project ${p.name}`}
                    title="Unregister project. Repository files and run history are preserved."
                    disabled={busy}
                    onClick={() => void perform(
                      () => api(`projects/${encodeURIComponent(id)}`, "DELETE", undefined, version),
                      "Project removed. Repository files preserved.",
                    )}
                  >
                    <CommandIcon name="trash" />
                  </button>
                </div>
              </article>
            ))}
          </section>
 );
}

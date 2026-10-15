import { useState, type FormEvent } from "react";
import { api } from "./controlApi";
import { RepositoryFolderField } from "./RepositoryFolderField";

type Perform = (action: () => Promise<unknown>, message: string) => Promise<void>;

export function useProjectRegistration(perform: Perform) {
 const [registrationName, setRegistrationName] = useState<string | null>(null);
 const [repositoryName, setRepositoryName] = useState("");
 const [registrationError, setRegistrationError] = useState("");
  const registerProject = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const f = new FormData(event.currentTarget);
    setRegistrationError("");
    void perform(
      async () => {
        try {
          return await api("projects", "POST", {
            name: f.get("name"),
            path: f.get("path"),
            webhook_url: f.get("webhook_url"),
          });
        } catch (reason) {
          setRegistrationError(reason instanceof Error ? reason.message : "Project registration failed.");
          throw reason;
        }
      },
      "Project registered",
    );
  };
 return {registrationName, setRegistrationName, repositoryName, setRepositoryName, registrationError, setRegistrationError, registerProject};
}

type Props = ReturnType<typeof useProjectRegistration> & {busy: boolean};

export function ProjectRegistration({registrationName, setRegistrationName, repositoryName, setRepositoryName, registrationError, setRegistrationError, registerProject, busy}: Props) {
 return (
          <section className="card" id="project-registration" tabIndex={-1} aria-labelledby="project-registration-heading">
            <h2 id="project-registration-heading">Add project</h2>
            <p>
              GitHub CLI must be installed and authenticated before registration. Repository needs a GitHub origin.
              Registration validates Qlty, initializes missing Qlty and CodeGraph setup, and creates and commits
              missing <code>.whirlwind.yml</code> with repository checks and scoped permissions using your Git identity.
              Required Qlty configuration is committed with it. App-generated empty starters can be upgraded.
              Custom policies and unrelated staged changes stay intact.
              Existing <code>.whirlwind.yaml</code> is supported. Agents and checks run with your local account permissions.
            </p>
            <form className="whirlwind-form" onSubmit={registerProject}>
              <label>
                Project name
                <input name="name" required value={registrationName ?? repositoryName} onChange={(event) => setRegistrationName(event.target.value || null)} />
              </label>
              <RepositoryFolderField disabled={busy} onPathChange={(path) => {
                setRepositoryName(path.split("/").filter(Boolean).pop() ?? "");
                setRegistrationError("");
              }} />
              <label>
                Public webhook URL (optional)
                <input
                  name="webhook_url"
                  type="url"
                  placeholder="https://your-ingress/webhooks/github"
                />
              </label>
              <button className="button primary" disabled={busy}>
                Register project
              </button>
              {registrationError && <p className="error" role="alert">{registrationError}</p>}
            </form>
          </section>
 );
}

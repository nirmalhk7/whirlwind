"use client";
import { FormEvent, useCallback, useEffect, useState } from "react";
import { useToast } from "./ToastProvider";

type Resource<T> = { id: string; version: number; data: T };
type Project = { id: string; name: string; repo: string; path: string };
type Provider = {
  id: string;
  name: string;
  kind: string;
  command: string[];
  available: boolean;
  quota: string;
  capabilities: string[];
  limitation?: string;
};
type Direction = {
  id: string;
  project_id: string;
  title: string;
  description: string;
  day: string;
  selected: boolean;
};
type Task = {
  id: string;
  project_id: string;
  issue: { title: string; body: string };
  stage: string;
  score: number;
  review_passes: number;
  plan_version: number;
  error?: string;
  pr?: string;
  plan?: {
    objective: string;
    acceptance_criteria: string[];
    questions: { id: string; text: string; answer: string }[];
    units: { id: string; objective: string; stage: string }[];
  };
  handoff?: { failing_tests: string[] };
};
type Review = {
  score: number;
  summary: string;
  findings: {
    id: string;
    file: string;
    line: number;
    message: string;
    blocking: boolean;
    resolved: boolean;
  }[];
};
type Inbox = {
  id: string;
  task_id?: string;
  kind: string;
  message: string;
  resolved: boolean;
};
type Permission = {
  id: string;
  task: string;
  provider: string;
  decision: string;
  request: unknown;
};
type NativeQuestions = {
  id: string;
  provider: string;
  status: string;
  questions: { id: string; text: string; options?: string[] }[];
};
type View =
  "overview" | "projects" | "providers" | "inbox" | "settings" | "tasks";
type Inventory = {
  projects: Resource<Project>[];
  providers: Resource<Provider>[];
  tasks: Resource<Task>[];
  directions: Resource<Direction>[];
  inbox: Resource<Inbox>[];
};
const empty: Inventory = {
  projects: [],
  providers: [],
  tasks: [],
  directions: [],
  inbox: [],
};
async function api<T>(
  path: string,
  method = "GET",
  data?: unknown,
  version?: number,
): Promise<T> {
  const response = await fetch(`/api/v1/${path}`, {
    method,
    cache: "no-store",
    headers:
      method === "GET"
        ? {}
        : {
            "Content-Type": "application/json",
            "Idempotency-Key": crypto.randomUUID(),
            ...(version !== undefined ? { "If-Match": String(version) } : {}),
          },
    body: method === "GET" ? undefined : JSON.stringify(data ?? {}),
  });
  const result = await response.json();
  if (!response.ok)
    throw new Error(
      result.error?.message ?? "Request failed. Refresh and retry.",
    );
  return result;
}
const stages: Record<string, string> = {
  planning: "Planning",
  plan_ready: "Publishing plan",
  questions: "Awaiting answers",
  coding: "Coding",
  repair: "Repairing",
  review: "Reviewing",
  approval: "Awaiting approval",
  publishing: "Publishing PR",
  pr_created: "PR ready",
  complete: "Complete",
  paused: "Paused",
};
const nav: [View, string, string][] = [
  ["overview", "Workspace", "/"],
  ["projects", "Projects", "/projects"],
  ["tasks", "Tasks", "/agents"],
  ["providers", "Providers", "/providers"],
  ["inbox", "Inbox", "/inbox"],
  ["settings", "Settings", "/settings"],
];

const descriptions: Record<View, string> = {
  overview: "Choose a direction. Track implementation. Review the result.",
  projects: "Trusted local repositories and execution policy.",
  providers: "Local agents, capabilities, and reported quota.",
  inbox: "Questions, permissions, and decisions.",
  settings: "Execution capacity and reconciliation frequency.",
  tasks: "Durable progress from issue to pull request.",
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

export function ControlCenter({ view = "overview" }: { view?: View }) {
  const toast = useToast();
  const [inventory, setInventory] = useState<Inventory>(empty);
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const [reviews, setReviews] = useState<Resource<Review>[]>([]);
  const [permissions, setPermissions] = useState<Resource<Permission>[]>([]);
  const [nativeQuestions, setNativeQuestions] = useState<
    Resource<NativeQuestions>[]
  >([]);
  const [nativeAnswers, setNativeAnswers] = useState<
    Record<string, Record<string, string>>
  >({});
  const [settings, setSettings] = useState<Resource<
    Record<string, number>
  > | null>(null);
  const refresh = useCallback(async () => {
    try {
      const [data, reviewData, permissionData, currentSettings, questionData] =
        await Promise.all([
          api<Inventory>("overview"),
          api<Resource<Review>[]>("reviews"),
          api<Resource<Permission>[]>("permissions"),
          view === "settings"
            ? api<Resource<Record<string, number>>>("settings")
            : Promise.resolve(null),
          view === "inbox"
            ? api<Resource<NativeQuestions>[]>("questions")
            : Promise.resolve([]),
        ]);
      setInventory(data);
      setError("");
      setLoaded(true);
      setReviews(reviewData);
      setPermissions(permissionData);
      if (currentSettings) setSettings(currentSettings);
      setNativeQuestions(questionData);
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Backend unavailable.",
      );
      setLoaded(true);
    }
  }, [view]);
  useEffect(() => {
    void refresh();
    const stream = new EventSource("/api/v1/events");
    let timer: ReturnType<typeof setTimeout> | null = null;
    stream.addEventListener("update", () => {
      if (!timer)
        timer = setTimeout(() => {
          timer = null;
          void refresh();
        }, 500);
    });
    const fallback = setInterval(() => {
      if (stream.readyState !== EventSource.OPEN) void refresh();
    }, 10_000);
    return () => {
      stream.close();
      clearInterval(fallback);
      if (timer) clearTimeout(timer);
    };
  }, [refresh]);
  const perform = async (action: () => Promise<unknown>, message: string) => {
    setBusy(true);
    try {
      await action();
      toast.success(message);
      await refresh();
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : "Request failed");
    } finally {
      setBusy(false);
    }
  };
  const task = inventory.tasks.find((entry) => entry.id === selected);
  const registerProject = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const f = new FormData(event.currentTarget);
    void perform(
      () =>
        api("projects", "POST", {
          name: f.get("name"),
          path: f.get("path"),
          webhook_url: f.get("webhook_url"),
        }),
      "Project registered",
    );
  };
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
    }, "ACP agent registered");
  };
  const projectName = (id: string) =>
    inventory.projects.find((p) => p.id === id)?.data.name ?? id;
  return (
    <div className="shell">
      <aside>
        <div className="brand">Khan</div>
        <nav aria-label="Main navigation">
          {nav.map(([key, title, href]) => (
            <a
              key={key}
              href={href}
              className={view === key ? "active" : ""}
              aria-current={view === key ? "page" : undefined}
            >
              {title}
            </a>
          ))}
        </nav>
      </aside>
      <main>
        <div className="desk-heading">
          <div>
            <h1>{nav.find(([key]) => key === view)?.[1]}</h1>
            <p className="sub">{descriptions[view] ?? descriptions.tasks}</p>
          </div>
          <button className="button" onClick={() => void refresh()}>
            Refresh
          </button>
        </div>
        {error && (
          <div role="alert" className="error">
            <p>{error}</p>
            <button className="button" onClick={() => void refresh()}>
              Retry connection
            </button>
          </div>
        )}
        {!loaded && <p role="status">Loading workspace…</p>}
        {(view === "overview" || view === "projects") && (
          <section className="list">
            <h2>
              {view === "overview"
                ? "Project directions"
                : "Registered projects"}
            </h2>
            {loaded && !error && !inventory.projects.length && (
              <p className="muted">
                No projects yet.{" "}
                <a href="/projects">Register a trusted repository</a> to begin.
              </p>
            )}
            {inventory.projects.map(({ id, version, data: p }) => (
              <article className="action-card" key={id}>
                <div className="row">
                  <h3>{p.name}</h3>
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
                <div className="khan-actions">
                  <button
                    className="button"
                    disabled={busy}
                    onClick={() =>
                      void perform(
                        () => api(`projects/${id}/ideation`, "POST"),
                        "Ideation queued",
                      )
                    }
                  >
                    Generate directions
                  </button>
                  {view === "projects" && (
                    <button
                      className="button"
                      disabled={busy}
                      onClick={() =>
                        void perform(
                          () =>
                            api(`projects/${id}/policy`, "POST", {}, version),
                          "Committed project policy confirmed",
                        )
                      }
                    >
                      Confirm committed policy
                    </button>
                  )}
                </div>
                {inventory.directions
                  .filter((d) => d.data.project_id === id && !d.data.selected)
                  .map((d) => (
                    <div className="khan-direction" key={d.id}>
                      <div>
                        <h4>{d.data.title}</h4>
                        <p>{d.data.description}</p>
                        <p className="muted">{d.data.day}</p>
                      </div>
                      <button
                        className="button"
                        disabled={busy}
                        onClick={() =>
                          void perform(async () => {
                            const result = await api<Resource<Task>>(
                              `directions/${d.id}/select`,
                              "POST",
                              {},
                              d.version,
                            );
                            setSelected(result.id);
                          }, "Direction selected; planning starts")
                        }
                      >
                        Select direction
                      </button>
                    </div>
                  ))}
              </article>
            ))}
          </section>
        )}
        {view === "projects" && (
          <section className="card">
            <h2>Add project</h2>
            <p>
              Repository needs GitHub origin and committed{" "}
              <code>.khan.yaml</code>. Authenticate GitHub CLI first. Agents and
              checks run with your local account permissions.
            </p>
            <form className="khan-form" onSubmit={registerProject}>
              <label>
                Project name
                <input name="name" required />
              </label>
              <label>
                Repository path
                <input
                  name="path"
                  placeholder="/absolute/path/to/repository"
                  required
                />
              </label>
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
            </form>
          </section>
        )}
        {(view === "overview" || view === "tasks") && (
          <section className="list">
            <h2>Tasks</h2>
            {loaded && !error && !inventory.tasks.length && (
              <p className="muted">
                Select a direction or label a GitHub issue{" "}
                <code>khan:plan</code> to start.
              </p>
            )}
            {inventory.tasks.map((t) => (
              <article className="action-card" key={t.id}>
                <div className="row">
                  <h3>{t.data.issue.title}</h3>
                  <span className="badge">
                    {stages[t.data.stage] ?? t.data.stage}
                  </span>
                </div>
                <p className="muted">
                  {projectName(t.data.project_id)} · Plan {t.data.plan_version}
                  {t.data.review_passes > 0
                    ? ` · Review ${t.data.score}/10`
                    : ""}
                </p>
                <button
                  className="button"
                  onClick={() => {
                    setSelected(t.id);
                    setAnswers({});
                  }}
                >
                  Open task
                </button>
              </article>
            ))}
          </section>
        )}
        {task && (
          <section className="card khan-task" aria-label="Task details">
            <div className="row">
              <h2>{task.data.issue.title}</h2>
              <button className="button" onClick={() => setSelected(null)}>
                Close details
              </button>
            </div>
            <p>
              <span className="badge">{stages[task.data.stage]}</span> · Score{" "}
              {task.data.score}/10 · {task.data.review_passes ?? 0} review
              passes
            </p>
            {task.data.error && <p className="error">{task.data.error}</p>}
            {task.data.plan && (
              <>
                <h3>Acceptance criteria</h3>
                <ul>
                  {task.data.plan.acceptance_criteria.map((c, i) => (
                    <li key={i}>{c}</li>
                  ))}
                </ul>
                {task.data.plan.units.length > 1 && (
                  <>
                    <h3>Implementation units</h3>
                    <ul>
                      {task.data.plan.units.map((u) => (
                        <li key={u.id}>
                          {u.objective} — {u.stage || "pending"}
                        </li>
                      ))}
                    </ul>
                  </>
                )}
              </>
            )}
            {task.data.stage === "questions" && (
              <form
                className="khan-form"
                onSubmit={(event) => {
                  event.preventDefault();
                  void perform(
                    () =>
                      api(
                        `tasks/${task.id}/answers`,
                        "POST",
                        { answers },
                        task.version,
                      ),
                    "Answers saved",
                  );
                }}
              >
                {task.data.plan?.questions
                  .filter((q) => !q.answer)
                  .map((q) => (
                    <label key={q.id}>
                      {q.text}
                      <textarea
                        required
                        value={answers[q.id] ?? ""}
                        onChange={(event) =>
                          setAnswers((current) => ({
                            ...current,
                            [q.id]: event.target.value,
                          }))
                        }
                      />
                    </label>
                  ))}
                <button className="button primary" disabled={busy}>
                  Submit answers
                </button>
              </form>
            )}
            <div className="khan-actions">
              {task.data.stage === "approval" && (
                <button
                  className="button primary"
                  disabled={busy}
                  onClick={() =>
                    void perform(
                      () =>
                        api(
                          `tasks/${task.id}/approve`,
                          "POST",
                          {},
                          task.version,
                        ),
                      "PR publication approved",
                    )
                  }
                >
                  Approve PR publication
                </button>
              )}
              {task.data.stage === "paused" && !task.data.pr ? (
                <button
                  className="button"
                  disabled={busy}
                  onClick={() =>
                    void perform(
                      () =>
                        api(
                          `tasks/${task.id}/resume`,
                          "POST",
                          {},
                          task.version,
                        ),
                      "Task resumed",
                    )
                  }
                >
                  Resume task
                </button>
              ) : (
                !["pr_created", "complete"].includes(task.data.stage) && (
                  <button
                    className="button"
                    disabled={busy}
                    onClick={() =>
                      void perform(
                        () =>
                          api(
                            `tasks/${task.id}/pause`,
                            "POST",
                            {},
                            task.version,
                          ),
                        "Task paused",
                      )
                    }
                  >
                    Pause task
                  </button>
                )
              )}
              {task.data.pr &&
                /^https:\/\/github\.com\//.test(task.data.pr) && (
                  <a
                    className="button primary"
                    href={task.data.pr}
                    target="_blank"
                    rel="noreferrer"
                  >
                    Open pull request
                  </a>
                )}
            </div>
            {reviews
              .filter((r) => r.id.startsWith(`${task.id}:`))
              .map((r) => (
                <div key={r.id}>
                  <h3>Review {r.data.score}/10</h3>
                  <p>{r.data.summary}</p>
                  <ul>
                    {r.data.findings.map((f) => (
                      <li key={f.id}>
                        {f.blocking && !f.resolved ? "Blocking: " : ""}
                        {f.file}:{f.line} — {f.message}
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            {task.data.handoff?.failing_tests?.length ? (
              <>
                <h3>Failing checks</h3>
                <pre>{task.data.handoff.failing_tests.join("\n")}</pre>
              </>
            ) : null}
          </section>
        )}
        {view === "providers" && (
          <>
            <section className="list">
              {inventory.providers.map((p) => (
                <article className="provider-row" key={p.id}>
                  <div className="row">
                    <h2>{p.data.name}</h2>
                    <span className="badge">
                      {p.data.available ? "Available" : "Unavailable"}
                    </span>
                  </div>
                  <p className="muted">
                    Quota: {p.data.quota} ·{" "}
                    {(p.data.capabilities ?? []).join(", ")}
                  </p>
                  {p.data.limitation && <p>{p.data.limitation}</p>}
                  <code>{p.data.command?.join(" ")}</code>
                  {p.data.kind !== "unavailable" && (
                    <details>
                      <summary>Edit provider setup</summary>
                      <form
                        className="khan-form"
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
              <h2>Add ACP agent</h2>
              <form className="khan-form" onSubmit={registerProvider}>
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
                  Register agent
                </button>
              </form>
            </section>
          </>
        )}
        {view === "inbox" && (
          <>
            <section className="list">
              <h2>Agent questions</h2>
              {nativeQuestions
                .filter((entry) => entry.data.status === "pending")
                .map((entry) => (
                  <form
                    className="action-card khan-form"
                    key={entry.id}
                    onSubmit={(event) => {
                      event.preventDefault();
                      void perform(
                        () =>
                          api(
                            `questions/${entry.id}/answers`,
                            "POST",
                            { answers: nativeAnswers[entry.id] ?? {} },
                            entry.version,
                          ),
                        "Answers sent to agent",
                      );
                    }}
                  >
                    <h3>{entry.data.provider} needs your input</h3>
                    {entry.data.questions.map((question) => (
                      <label key={question.id}>
                        {question.text}
                        {question.options?.length ? (
                          <span className="muted">
                            Options: {question.options.join(" · ")}. You can
                            also write your own answer.
                          </span>
                        ) : null}
                        <textarea
                          required
                          value={nativeAnswers[entry.id]?.[question.id] ?? ""}
                          onChange={(event) =>
                            setNativeAnswers((current) => ({
                              ...current,
                              [entry.id]: {
                                ...current[entry.id],
                                [question.id]: event.target.value,
                              },
                            }))
                          }
                        />
                      </label>
                    ))}
                    <button className="button primary" disabled={busy}>
                      Send answers to agent
                    </button>
                  </form>
                ))}
            </section>
            <section className="list">
              <h2>Permission requests</h2>
              {permissions
                .filter((p) => p.data.decision === "pending")
                .map((p) => (
                  <article className="action-card" key={p.id}>
                    <h3>{p.data.provider} requests permission</h3>
                    <pre>{JSON.stringify(p.data.request, null, 2)}</pre>
                    <div className="khan-actions">
                      {["allow", "deny"].map((decision) => (
                        <button
                          className="button"
                          disabled={busy}
                          key={decision}
                          onClick={() =>
                            void perform(
                              () =>
                                api(
                                  `permissions/${p.id}/decisions`,
                                  "POST",
                                  { decision },
                                  p.version,
                                ),
                              `Permission ${decision === "allow" ? "allowed" : "denied"}`,
                            )
                          }
                        >
                          {decision === "allow" ? "Allow once" : "Deny"}
                        </button>
                      ))}
                    </div>
                  </article>
                ))}
            </section>
            <section className="list">
              <h2>Notifications</h2>
              {inventory.inbox
                .filter((n) => !n.data.resolved)
                .map((n) => (
                  <article className="action-card" key={n.id}>
                    <h3>{n.data.kind.replaceAll("_", " ")}</h3>
                    <p>{n.data.message}</p>
                    <div className="khan-actions">
                      {n.data.task_id && (
                        <button
                          className="button"
                          onClick={() => setSelected(n.data.task_id ?? null)}
                        >
                          Open task
                        </button>
                      )}
                      <button
                        className="button"
                        disabled={busy}
                        onClick={() =>
                          void perform(
                            () => api(`inbox/${n.id}`, "PATCH", {}, n.version),
                            "Notification resolved",
                          )
                        }
                      >
                        Mark resolved
                      </button>
                    </div>
                  </article>
                ))}
            </section>
          </>
        )}
        {view === "settings" && settings && (
          <form
            className="card khan-form"
            onSubmit={(event) => {
              event.preventDefault();
              const f = new FormData(event.currentTarget);
              void perform(
                () =>
                  api(
                    "settings",
                    "PATCH",
                    {
                      global_slots: Number(f.get("global_slots")),
                      project_slots: Number(f.get("project_slots")),
                      reconciliation_seconds: Number(
                        f.get("reconciliation_seconds"),
                      ),
                    },
                    settings.version,
                  ),
                "Settings updated",
              );
            }}
          >
            <h2>Worker settings</h2>
            <label>
              Global execution slots
              <input
                name="global_slots"
                type="number"
                min="1"
                max="32"
                defaultValue={settings.data.global_slots}
                required
              />
            </label>
            <label>
              Execution slots per project
              <input
                name="project_slots"
                type="number"
                min="1"
                max="32"
                defaultValue={settings.data.project_slots}
                required
              />
            </label>
            <label>
              GitHub reconciliation interval (seconds)
              <input
                name="reconciliation_seconds"
                type="number"
                min="30"
                defaultValue={settings.data.reconciliation_seconds}
                required
              />
            </label>
            <button className="button primary" disabled={busy}>
              Save settings
            </button>
          </form>
        )}
      </main>
    </div>
  );
}

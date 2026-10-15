import type { Dispatch, SetStateAction } from "react";
import { CommandIcon } from "./CommandShell";
import { stages, type Resource, type Task } from "./controlTypes";

type Props = {
 loaded: boolean; error: string; tasks: Resource<Task>[];
 projectName: (id: string) => string;
 setSelected: Dispatch<SetStateAction<string | null>>;
 setAnswers: Dispatch<SetStateAction<Record<string, string>>>;
};

export function TaskList({loaded, error, tasks, projectName, setSelected, setAnswers}: Props) {
 return (
          <section className="list command-tasks">
            <h2>Tasks</h2>
            {loaded && !error && !tasks.length && (
              <div className="command-empty command-empty-tasks">
                <h3>Ready for the work ahead.</h3>
                <p>Select a project direction or label a GitHub issue <code>whirlwind:plan</code>. Follow every step here, from planning to pull request.</p>
                <a className="command-text-link" href="/projects">Choose your project <CommandIcon name="arrow" /></a>
              </div>
            )}
            {tasks.map((t) => (
              <article className="action-card" key={t.id}>
                <div className="row">
                  <h3>{t.data.issue.title}</h3>
                  <span className="badge" data-stage={t.data.stage}>
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
 );
}

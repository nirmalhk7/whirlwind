import type { Dispatch, SetStateAction } from "react";
import { api } from "./controlApi";
import { stages, type Resource, type Task, type Review } from "./controlTypes";
import { PlanReview } from "./PlanReview";
import { TaskBudget } from "./TaskBudget";
import { TaskFeedback } from "./TaskFeedback";

type Props = {
 selectedIdeas: number;
 task: Resource<Task>;
 answers: Record<string, string>;
 setAnswers: Dispatch<SetStateAction<Record<string, string>>>;
 busy: boolean;
 perform: (action: () => Promise<unknown>, message: string) => Promise<void>;
 setSelected: Dispatch<SetStateAction<string | null>>;
 reviews: Resource<Review>[];
};

export function TaskDetails({task, selectedIdeas, answers, setAnswers, busy, perform, setSelected, reviews}: Props) {
 return (
          <section className="card whirlwind-task" aria-label="Task details">
            <div className="row">
              <h2>{task.data.issue.title}</h2>
              <button className="button" onClick={() => setSelected(null)}>
                Close details
              </button>
            </div>
            <p>
              <span className="badge" data-stage={task.data.stage}>{stages[task.data.stage]}</span> · Score{" "}
              {task.data.score}/10 · {task.data.review_passes ?? 0} review
              passes
            </p>
            <p><span className="command-count-value" key={selectedIdeas}>{selectedIdeas}</span> {selectedIdeas === 1 ? "idea" : "ideas"} selected for plan generation</p>
            {task.data.error && <p className="error">{task.data.error}</p>}
            {task.data.selection&&<p className="muted">Selected objective: {task.data.selection.objective}. Agents proceed within this scope and project policy.</p>}
            {(task.data.plan_document || task.data.stage === "plan_approval") && <PlanReview id={task.id} version={task.version} stage={task.data.stage} busy={busy} perform={perform} />}
            <TaskBudget id={task.id} version={task.version} busy={busy} perform={perform}/>
            <TaskFeedback id={task.id} version={task.version} busy={busy} perform={perform}/>
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
                className="whirlwind-form"
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
            <div className="whirlwind-actions">
              {!task.data.selection&&!["complete","pr_created","cancelled"].includes(task.data.stage)&&<button className="button primary" disabled={busy} onClick={()=>void perform(
                ()=>api(`tasks/${task.id}/adopt`,"POST",{},task.version),"Task objective adopted; execution authorized within project policy",
              )}>Adopt task objective</button>}
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
                !["pr_created", "complete", "cancelled"].includes(task.data.stage) && (
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
                /^https:\/\/[^/]+\//.test(task.data.pr) && (
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
 );
}

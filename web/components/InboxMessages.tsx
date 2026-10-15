import type { Dispatch, SetStateAction } from "react";
import { api } from "./controlApi";
import type { InboxMessage } from "./inboxMessageModel";

type Props = {
 messages: InboxMessage[]; loaded: boolean; error: string; busy: boolean;
 perform: (action: () => Promise<unknown>, message: string) => Promise<void>;
 setSelected: Dispatch<SetStateAction<string | null>>;
 nativeAnswers: Record<string, Record<string, string>>;
 setNativeAnswers: Dispatch<SetStateAction<Record<string, Record<string, string>>>>;
};

type Entry<Type extends InboxMessage["type"]> = Extract<InboxMessage, { type: Type }>["entry"];

function QuestionMessage({ entry, busy, perform, nativeAnswers, setNativeAnswers }: Pick<Props, "busy" | "perform" | "nativeAnswers" | "setNativeAnswers"> & { entry: Entry<"question"> }) {
  return <form className="action-card whirlwind-form" key={`question:${entry.id}`}
    onSubmit={(event) => {
      event.preventDefault();
      void perform(
        () => api(`questions/${entry.id}/answers`, "POST", { answers: nativeAnswers[entry.id] ?? {} }, entry.version),
        "Answers sent to agent",
      );
    }}>
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
        <textarea required value={nativeAnswers[entry.id]?.[question.id] ?? ""}
          onChange={(event) => setNativeAnswers((current) => ({
            ...current,
            [entry.id]: { ...current[entry.id], [question.id]: event.target.value },
          }))} />
      </label>
    ))}
    <button className="button primary" disabled={busy}>Send answers to agent</button>
  </form>;
}

function PermissionMessage({ entry: p, busy, perform }: Pick<Props, "busy" | "perform"> & { entry: Entry<"permission"> }) {
  return (
    <article className="action-card" key={`permission:${p.id}`}>
      <h3>{p.data.provider} requests permission</h3>
      <pre>{JSON.stringify(p.data.request, null, 2)}</pre>
      <div className="whirlwind-actions">
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
  );
}

function NotificationMessage({ entry: n, busy, perform, setSelected }: Pick<Props, "busy" | "perform" | "setSelected"> & { entry: Entry<"notification"> }) {
  return (
    <article className="action-card" key={`notification:${n.id}`}>
      <h3>{n.data.kind.replaceAll("_", " ")}</h3>
      <p>{n.data.message}</p>
      <div className="whirlwind-actions">
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
  );
}

function MessageCard({ message, ...props }: Props & { message: InboxMessage }) {
  if (message.type === "question") return <QuestionMessage {...props} entry={message.entry} />;
  if (message.type === "permission") return <PermissionMessage {...props} entry={message.entry} />;
  return <NotificationMessage {...props} entry={message.entry} />;
}

export function InboxMessages(props: Props) {
  const { messages, loaded, error } = props;
  return <section className="list" aria-labelledby="messages-heading">
    <h2 id="messages-heading">Messages</h2>
    {!messages.length && loaded && !error && <p className="muted">No messages waiting.</p>}
    {messages.map((message) => <MessageCard key={`${message.type}:${message.entry.id}`} {...props} message={message} />)}
  </section>;
}

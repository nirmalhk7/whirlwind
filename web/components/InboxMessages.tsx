import type { Dispatch, SetStateAction } from "react";
import { api } from "./controlApi";
import { inboxFeed, type InboxMessage } from "./inboxMessageModel";
import { InboxTimestamp } from "./InboxTimestamp";
import { InboxRemoveButton } from "./InboxRemoveButton";
import { IdeaGroup, type DirectionChoice } from "./DirectionChoices";
import type { Resource, Direction } from "./controlTypes";

type Props = {
 directions: Resource<Direction>[]; onSelect: (direction: DirectionChoice) => Promise<void>;
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
    <InboxTimestamp value={entry.updated_at} />
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
      <InboxTimestamp value={p.updated_at} />
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
      <div className="inbox-message-header">
        <InboxRemoveButton label="Dismiss notification" disabled={busy} onClick={() => void perform(
          () => api(`inbox/${encodeURIComponent(n.id)}`, "PATCH", {}, n.version), "Notification dismissed",
        )} />
        <InboxTimestamp value={n.updated_at} />
      </div>
      <h3>{n.data.kind.replaceAll("_", " ")}</h3>
      <p>{n.data.message}</p>
      {n.data.current && <p className="muted">Current: {n.data.current.replaceAll("_", " ")}</p>}
      {!!n.data.completed?.length && <ul>{n.data.completed.map((item, index) => <li key={`${index}:${item}`}>{item}</li>)}</ul>}
      {n.data.next_action && <p><strong>Next:</strong> {n.data.next_action}</p>}
      {n.data.usage&&<p className="muted">{n.data.usage.attempts} attempts · {n.data.usage.total_tokens} reported tokens · {(n.data.usage.elapsed_ms/1000).toFixed(1)} active seconds{n.data.usage.unknown_token_attempts>0?` · Token usage unknown for ${n.data.usage.unknown_token_attempts} attempts`:""}{n.data.usage.unknown_cost_attempts>0?` · Cost unknown for ${n.data.usage.unknown_cost_attempts} attempts`:""}</p>}
      {(n.data.occurrences ?? 0) > 1 && <p className="muted">Repeated {n.data.occurrences} times; grouped to reduce noise.</p>}
      <div className="whirlwind-actions">
        {n.data.task_id && (
          <button
            className="button"
            onClick={() => setSelected(n.data.task_id ?? null)}
          >
            Open task
          </button>
        )}
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
  const { messages, directions, loaded, error } = props;
  const entries = inboxFeed(messages, directions);
  return <section className="list inbox-feed" aria-labelledby="messages-heading">
    <h2 id="messages-heading">Messages and ideas</h2>
    {!entries.length && loaded && !error && <p className="muted">No messages or ideas waiting.</p>}
    <div className="inbox-feed-entries" role="list">
      {entries.map(item => <div className="inbox-feed-entry" role="listitem" key={item.type === "idea" ? `idea:${item.entry.data.group_id ?? item.entry.id}` : `${item.type}:${item.entry.id}`}>
        {item.type === "idea"
          ? <IdeaGroup directions={item.directions} disabled={props.busy} onSelect={props.onSelect}
              onDelete={() => props.perform(() => api(`directions/${encodeURIComponent(item.directions[0].id)}/group`, "DELETE", {
                versions: Object.fromEntries(item.directions.map(direction => [direction.id, direction.version])),
              }, item.directions[0].version), "Ideas deleted")} />
          : <MessageCard {...props} message={item} />}
      </div>)}
    </div>
  </section>;
}

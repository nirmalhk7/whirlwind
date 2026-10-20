import type { Resource, Inbox, Permission, NativeQuestions, Direction } from "./controlTypes";

export type InboxMessage =
  | { type: "question"; entry: Resource<NativeQuestions> }
  | { type: "permission"; entry: Resource<Permission> }
  | { type: "notification"; entry: Resource<Inbox> };

export function inboxMessages(questions: Resource<NativeQuestions>[], permissions: Resource<Permission>[], notifications: Resource<Inbox>[]): InboxMessage[] {
  const messages: InboxMessage[] = [
    ...questions.filter((entry) => entry.data.status === "pending").map((entry) => ({ type: "question" as const, entry })),
    ...permissions.filter((entry) => entry.data.decision === "pending").map((entry) => ({ type: "permission" as const, entry })),
    ...notifications.filter((entry) => !entry.data.resolved && !represented(entry.data, questions, permissions)).map((entry) => ({ type: "notification" as const, entry })),
  ];
  return messages.sort(compareMessages);
}

function represented(notification: Inbox, questions: Resource<NativeQuestions>[], permissions: Resource<Permission>[]): boolean {
  switch (notification.kind) {
    case "question": return questions.some((entry) => notification.message === `Agent clarification awaits answer: ${entry.id}`);
    case "permission": return permissions.some((entry) => notification.message === `Tool permission awaits decision: ${entry.id}`);
    default: return false;
  }
}

export type InboxFeedEntry = InboxMessage | { type: "idea"; entry: Resource<Direction>; directions: Resource<Direction>[] };

export function inboxFeed(messages: InboxMessage[], directions: Resource<Direction>[]): InboxFeedEntry[] {
  const groups = new Map<string, Resource<Direction>[]>();
  for (const direction of directions) {
    const key = direction.data.group_id ?? direction.id;
    const group = groups.get(key) ?? [];
    group.push(direction);
    groups.set(key, group);
  }
  const categories = ["expand", "optimize", "harden", "simplify"];
  const ideas: InboxFeedEntry[] = [...groups.values()].map(group => {
    const original = group.reduce((first, entry) => creationPosition(entry) < creationPosition(first) ? entry : first);
    group.sort((a, b) => {
      const rank = (category?: string) => { const index = categories.indexOf(category ?? ""); return index < 0 ? categories.length : index; };
      return rank(a.data.category) - rank(b.data.category) || a.id.localeCompare(b.id);
    });
    return { type: "idea", entry: original, directions: group };
  });
  return [...messages, ...ideas].sort(compareMessages);
}

type OrderedEntry = { updated_at?: string; created_order?: number; id: string };

function creationPosition(entry: OrderedEntry) {
  return entry.created_order ?? (Date.parse(entry.updated_at ?? "") || 0);
}

function compareMessages(a: { entry: OrderedEntry }, b: { entry: OrderedEntry }) {
  return creationPosition(b.entry) - creationPosition(a.entry) || a.entry.id.localeCompare(b.entry.id);
}

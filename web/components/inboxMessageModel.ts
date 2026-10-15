import type { Resource, Inbox, Permission, NativeQuestions } from "./controlTypes";

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

function compareMessages(a: InboxMessage, b: InboxMessage) {
  const aTime = Date.parse(a.entry.updated_at ?? "") || 0;
  const bTime = Date.parse(b.entry.updated_at ?? "") || 0;
  return bTime - aTime || a.entry.id.localeCompare(b.entry.id);
}

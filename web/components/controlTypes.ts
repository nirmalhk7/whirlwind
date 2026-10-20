import type { ControlView } from "./CommandShell";

export type Resource<T> = { id: string; version: number; updated_at?: string; created_order?: number; data: T };
export type Project = { id: string; name: string; repo: string; path: string; policy_hash?: string };
export type Provider = {
  id: string;
  name: string;
  kind: string;
  command: string[];
  model_command?: string[];
  available: boolean;
  quota: string;
  capabilities: string[];
  limitation?: string;
};
export type Direction = {
  group_id?: string;
  id: string;
  project_id: string;
  category?: string;
  title: string;
  description: string;
  day: string;
  selected: boolean;
};
export type Task = {
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
export type Review = {
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
export type Inbox = {
  id: string;
  task_id?: string;
  kind: string;
  message: string;
  resolved: boolean;
};
export type Permission = {
  id: string;
  task: string;
  provider: string;
  decision: string;
  request: unknown;
};
export type NativeQuestions = {
  id: string;
  provider: string;
  status: string;
  questions: { id: string; text: string; options?: string[] }[];
};
export type View = ControlView;
export type Inventory = {
  projects: Resource<Project>[];
  providers: Resource<Provider>[];
  tasks: Resource<Task>[];
  directions: Resource<Direction>[];
  inbox: Resource<Inbox>[];
};
export const empty: Inventory = {
  projects: [],
  providers: [],
  tasks: [],
  directions: [],
  inbox: [],
};
export const stages: Record<string, string> = {
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

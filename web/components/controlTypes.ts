import type { ControlView } from "./CommandShell";

export type Resource<T> = { id: string; version: number; updated_at?: string; created_order?: number; data: T };
export type Project = { id: string; name: string; repo: string; path: string; policy_hash?: string; paused?: boolean };
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
	expected_benefit?:string;
	uncertainty?:string[];
	effort_estimate?:string;
	problem_kind?:string;
	rank?:number;
	status?: string;
	task_id?: string;
	scope?: string[];
	evidence?: {kind:string;ref:string;revision?:string;summary?:string}[];
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
 mode?: "personal" | "agentic";
 plan_document?: {path:string;hash:string;approved_hash?:string};
	selection?:{candidate_id:string;objective:string;scope:string[];policy_hash:string;selected_at:string};
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
	usage?:{attempts:number;total_tokens:number;unknown_token_attempts:number;unknown_cost_attempts:number;elapsed_ms:number;cost:number};
	occurrences?: number;
	first_at?: string;
	last_at?: string;
	current?: string;
	next_action?: string;
	completed?: string[];
	evidence_id?: string;
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
	awaiting_selection: "Awaiting selection",
  planning: "Planning",
  plan_ready: "Publishing plan",
  plan_approval: "Review local plan",
  submitting: "Preparing commits and PR",
  cancelled: "Cancelled",
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

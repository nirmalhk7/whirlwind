import type { RunRecord } from "./RunHistory";

type Run = RunRecord["data"];
const roles: Record<string, string> = { ideate: "Ideation Agent", plan: "Planner Agent", code: "Coding Agent", review: "Reviewer Agent", submit: "Submitter Agent" };
const statusOutcomes = new Map([["interrupted", "Interrupted"], ["cancelled", "Cancelled"]]);
const timeValue = (value?: string) => value ? Date.parse(value) || 0 : 0;

export function compareRuns(a: RunRecord, b: RunRecord) {
  return timeValue(b.data.started_at ?? b.updated_at) - timeValue(a.data.started_at ?? a.updated_at) || b.id.localeCompare(a.id);
}

export function runOutcome(run: Run) {
  const statusOutcome = statusOutcomes.get(run.status ?? "");
  if (statusOutcome) return statusOutcome;
  if (run.success === true) return "Succeeded";
  if (run.success === false) return "Failed";
  if (run.finished) return "Finished — outcome unknown";
  return run.started_at ? "Unfinished" : "Outcome unknown";
}

export function runAvailability(run: Run) {
  if (run.success === true) return "available";
  if (run.success === false) return "unavailable";
  return undefined;
}

export function runTitle(run: Run) {
  if (run.purpose === "provider_test") return "Connection test";
  return roles[run.role] ?? run.role;
}

export function runProvider(run: Run) {
  const name = run.provider_name || run.provider || "Unknown";
  if (run.provider_name && run.provider_name !== run.provider) return `${name} · ${run.provider}`;
  return name;
}

export function runModel(run: Run) {
  if (run.resolved_model) return run.resolved_model;
  return run.model ? `${run.model} (configured)` : "Unknown";
}

export function runThinking(run: Run) {
  if (run.effective_thinking?.value != null) return run.effective_thinking.value;
  if (run.thinking) return `${run.thinking.value} (configured)`;
  return run.transport ? "Provider default" : "Unknown";
}

export function runTransport(run: Run) {
  if (run.transport === "acp") return "ACP";
  return run.transport ? `Native ${run.transport}` : "Unknown";
}

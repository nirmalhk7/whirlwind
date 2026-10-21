"use client";

export type RuntimeSnapshot = {
  context?: { used: number; size: number; source: string };
  recovery_reason?: string;
  checkpoint_id?: string;
  recoveries: number;
  rotations: number;
  generation?: number;
  warnings?: string[];
};

export function RunRuntime({ runtime }: { runtime?: RuntimeSnapshot }) {
  const context = runtime?.context;
  const percentage = context && context.size > 0 ? Math.round(context.used / context.size * 100) : undefined;
  const remaining = Math.max(0, 2 - (runtime?.recoveries ?? 0));
  return <section aria-label="Agent runtime">
    <h4>Context and recovery</h4>
    {percentage === undefined ? <p className="muted">Context occupancy unavailable</p>
      : <p className="muted">Context occupancy: {percentage}% · {context?.used.toLocaleString()} of {context?.size.toLocaleString()} tokens reported</p>}
    {runtime && <p className="muted">{remaining} {remaining === 1 ? "recovery" : "recoveries"} remaining · {runtime.rotations} context rotations</p>}
    {runtime?.recovery_reason && <p>{runtime.recovery_reason}</p>}
    {runtime?.checkpoint_id && <p className="muted">Checkpoint: {runtime.checkpoint_id}</p>}
    {runtime?.warnings?.map((warning, index) => <p role="status" key={index}>{warning}</p>)}
  </section>;
}

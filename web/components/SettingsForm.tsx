"use client";

import { useState, type FormEvent } from "react";
import { TimezoneSelect } from "./TimezoneSelect";

export type WorkerSettings = {
  global_slots: number;
  project_slots: number;
  reconciliation_seconds: number;
  ideation: { enabled: boolean; time: string; timezone: string };
};

export function SettingsForm({ settings, busy, onSave }: { settings: WorkerSettings; busy: boolean; onSave: (value: WorkerSettings) => void }) {
  const schedule = settings.ideation ?? { enabled: true, time: "10:00", timezone: "America/Denver" };
  const [timezone, setTimezone] = useState(schedule.timezone);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const fields = new FormData(event.currentTarget);
    onSave({
      global_slots: Number(fields.get("global_slots")),
      project_slots: Number(fields.get("project_slots")),
      reconciliation_seconds: Number(fields.get("reconciliation_seconds")),
      ideation: { enabled: fields.get("ideation_enabled") === "on", time: String(fields.get("ideation_time")), timezone: String(fields.get("ideation_timezone")).trim() },
    });
  };
  return <form className="card whirlwind-form settings-form" onSubmit={submit}>
    <h2>Worker settings</h2>
    <fieldset disabled={busy}>
      <label>Global execution slots<input name="global_slots" type="number" min="1" max="32" defaultValue={settings.global_slots} required /></label>
      <label>Execution slots per project<input name="project_slots" type="number" min="1" max="32" defaultValue={settings.project_slots} required /></label>
      <label>GitHub reconciliation interval (seconds)<input name="reconciliation_seconds" type="number" min="30" max="86400" defaultValue={settings.reconciliation_seconds} required /></label>
      <div className="ideation-schedule">
        <h3>Ideation schedule</h3>
        <p className="muted">Generate four fresh ideas per project, once daily. Whirlwind must be running; after downtime, it catches up the latest due day. Manual Run ideation stays available when automatic runs are off.</p>
        <label className="schedule-toggle"><input type="checkbox" name="ideation_enabled" defaultChecked={schedule.enabled} />Automatic daily ideation</label>
        <div className="schedule-fields">
          <label>Daily ideation time<input type="time" name="ideation_time" defaultValue={schedule.time} required /></label>
          <div className="timezone-field"><label htmlFor="ideation-timezone">Ideation timezone</label><TimezoneSelect value={timezone} disabled={busy} onChange={setTimezone} /></div>
        </div>
        <p id="ideation-timezone-help" className="muted">Search by city, country, or IANA timezone. Changing time does not repeat a project’s completed daily run.</p>
      </div>
      <button className="button primary" disabled={busy}>{busy ? "Saving…" : "Save settings"}</button>
    </fieldset>
  </form>;
}

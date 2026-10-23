"use client";
import { useState } from "react";
import { CommandShell } from "./CommandShell";
import { RunHistory } from "./RunHistory";
import { TaskDetails } from "./TaskDetails";
import { ProjectList } from "./ProjectList";
import { TaskList } from "./TaskList";
import { ControlSettings } from "./ControlSettings";
import { inboxMessages } from "./inboxMessageModel";
import { InboxMessages } from "./InboxMessages";
import { useControlInventory } from "./useControlInventory";
import { useProviderStatus } from "./useProviderStatus";
import { ProjectRegistration, useProjectRegistration } from "./ProjectRegistration";
import { useControlActions } from "./useControlActions";
import { api } from "./controlApi";
import { BrowserNotifications } from "./BrowserNotifications";

import type { View } from "./controlTypes";
export function ControlCenter({ view = "inbox" }: { view?: View }) {
  const { inventory, setInventory, error, loaded, processing, runs, reviews, permissions, nativeQuestions, startupSettings, workersStopped, refresh } = useControlInventory(view);
  const providerStatus = useProviderStatus(view === "inbox" || view === "settings", workersStopped);
  const [selected, setSelected] = useState<string | null>(null);
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const [nativeAnswers, setNativeAnswers] = useState<
    Record<string, Record<string, string>>
  >({});
  const {busy, perform, selectIdea} = useControlActions(refresh, setSelected, setInventory);
  const task = inventory.tasks.find((entry) => entry.id === selected);
  const registration = useProjectRegistration(perform);
  const projectName = (id: string) =>
    inventory.projects.find((p) => p.id === id)?.data.name ?? id;
  const messages = inboxMessages(nativeQuestions, permissions, inventory.inbox);
  const ready = loaded && !error;
  return (
    <CommandShell workersStopped={workersStopped} processing={processing} view={view} loaded={loaded} error={error} busy={busy} onRefresh={() => void refresh()}
      onToggleRunners={() => void perform(
        () => api(workersStopped ? "runners/resume" : "runners/stop", "POST"),
        workersStopped ? "Runners resumed; workers.stopped saved as false" : "Runners stopped; workers.stopped saved as true",
      )}
      activeTasks={inventory.tasks.filter((entry) => !["complete", "pr_created", "paused", "cancelled", "plan_approval"].includes(entry.data.stage)).length}
      availableProviders={providerStatus.snapshot && !providerStatus.error ? providerStatus.snapshot.providers.filter(provider => provider.status === "working").length : null}
      decisions={messages.length}>
        <BrowserNotifications items={inventory.inbox} loaded={loaded&&!error} visible={view==="inbox"||view==="settings"}/>
        {error && (
          <div role="alert" className="error">
            <p>{error}</p>
            <button className="button" onClick={() => void refresh()}>
              Retry connection
            </button>
          </div>
        )}
        {!loaded && <div className="command-loading" role="status"><p>Loading workspace…</p><span /><span /><span /></div>}
        {view === "runs" && ready && <RunHistory runs={runs} projectName={projectName} />}
        {(view === "overview" || view === "projects") && (
          <ProjectList view={view} loaded={loaded} error={error} busy={busy} projects={inventory.projects} perform={perform} />
        )}
        {view === "projects" && (
          <ProjectRegistration {...registration} busy={busy} />
        )}
        {(view === "overview" || view === "tasks") && (
          <TaskList loaded={loaded} error={error} tasks={inventory.tasks} projectName={projectName} setSelected={setSelected} setAnswers={setAnswers} />
        )}
        {task && <TaskDetails selectedIdeas={inventory.directions.filter(entry => entry.data.selected).length} task={task} answers={answers} setAnswers={setAnswers} busy={busy} perform={perform} setSelected={setSelected} reviews={reviews} />}
        <ControlSettings view={view} busy={busy} providers={inventory.providers} providerStatus={providerStatus} startupSettings={startupSettings} perform={perform} />
        {view === "inbox" && (
          <InboxMessages directions={inventory.directions} onSelect={selectIdea} messages={messages} loaded={loaded} error={error} busy={busy} perform={perform} setSelected={setSelected} nativeAnswers={nativeAnswers} setNativeAnswers={setNativeAnswers} />
        )}


    </CommandShell>
  );
}

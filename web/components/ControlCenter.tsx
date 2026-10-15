"use client";
import { useState } from "react";
import { CommandShell } from "./CommandShell";
import { RunHistory } from "./RunHistory";
import { IdeationTrigger } from "./IdeationTrigger";
import { DirectionChoices } from "./DirectionChoices";
import { TaskDetails } from "./TaskDetails";
import { ProjectList } from "./ProjectList";
import { TaskList } from "./TaskList";
import { ControlSettings } from "./ControlSettings";
import { inboxMessages } from "./inboxMessageModel";
import { InboxMessages } from "./InboxMessages";
import { useControlInventory } from "./useControlInventory";
import { ProjectRegistration, useProjectRegistration } from "./ProjectRegistration";
import { useControlActions } from "./useControlActions";

import type { View } from "./controlTypes";
export function ControlCenter({ view = "inbox" }: { view?: View }) {
  const { inventory, setInventory, error, loaded, runs, reviews, permissions, nativeQuestions, agentConfigs, settings, refresh } = useControlInventory(view);
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
    <CommandShell view={view} loaded={loaded} error={error} busy={busy} onRefresh={() => void refresh()}
      activeTasks={inventory.tasks.filter((entry) => !["complete", "pr_created", "paused"].includes(entry.data.stage)).length}
      availableProviders={inventory.providers.filter((entry) => entry.data.available).length}
      decisions={messages.length}>
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
        {task && <TaskDetails task={task} answers={answers} setAnswers={setAnswers} busy={busy} perform={perform} setSelected={setSelected} reviews={reviews} />}
        <ControlSettings view={view} busy={busy} providers={inventory.providers} agentConfigs={agentConfigs} settings={settings} perform={perform} />
        {view === "inbox" && (
          <>
            <section className="list" aria-labelledby="ideation-heading">
              <div className="row"><h2 id="ideation-heading">Project ideation</h2><a href="/projects">Manage projects</a></div>
              {!inventory.projects.length && ready && <p className="muted">Register a project to run ideation.</p>}
              {inventory.projects.map((project) => (
                <article className="action-card" key={project.id}>
                  <div className="row"><h3>{project.data.name}</h3><IdeationTrigger projectID={project.id} projectName={project.data.name} onComplete={refresh} /></div>
                  <DirectionChoices directions={inventory.directions.filter((direction) => direction.data.project_id === project.id)} disabled={busy} onSelect={selectIdea} />
                </article>
              ))}
            </section>
            <InboxMessages messages={messages} loaded={loaded} error={error} busy={busy} perform={perform} setSelected={setSelected} nativeAnswers={nativeAnswers} setNativeAnswers={setNativeAnswers} />
          </>
        )}


    </CommandShell>
  );
}

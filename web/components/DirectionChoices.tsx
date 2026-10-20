"use client";

import { useRef, useState } from "react";
import { InboxTimestamp } from "./InboxTimestamp";
import { InboxRemoveButton } from "./InboxRemoveButton";
import { FontAwesomeIcon } from "@fortawesome/react-fontawesome";
import { faCheck, faEllipsis } from "@fortawesome/free-solid-svg-icons";
import type { Direction, Resource } from "./controlTypes";

export type DirectionChoice = {
  id: string;
  version: number;
  updated_at?: string;
  data: { title: string; description: string; category?: string; selected: boolean; group_id?: string; project_id?: string };
};

export function DirectionChoices({ directions, disabled, onSelect }: {
  directions: DirectionChoice[];
  disabled: boolean;
  onSelect: (direction: DirectionChoice) => Promise<void>;
}) {
  if (!directions.length) return null;
  return <section className="idea-grid" aria-label="Idea choices">
    {directions.map(direction => <IdeaCard key={direction.id} direction={direction} disabled={disabled} onSelect={onSelect} />)}
  </section>;
}

export function IdeaGroup({ directions, disabled, onSelect, onDelete }: {
  directions: Resource<Direction>[]; disabled: boolean;
  onSelect: (direction: DirectionChoice) => Promise<void>; onDelete: () => Promise<void>;
}) {
  return <section className="idea-group" aria-label="Idea batch">
    <div className="idea-group-header">
      <InboxRemoveButton label={`Delete all ${directions.length} ideas`} disabled={disabled} onClick={() => void onDelete()} />
    </div>
    <DirectionChoices directions={directions} disabled={disabled} onSelect={onSelect} />
  </section>;
}

export function IdeaCard({ direction, disabled, onSelect, projectName }: {
  direction: DirectionChoice; projectName?: string; disabled: boolean; onSelect: (direction: DirectionChoice) => Promise<void>;
}) {
  const [expanded, setExpanded] = useState(false);
  const [pending, setPending] = useState<boolean | null>(null);
  const selecting = useRef(false);
  const words = direction.data.description.trim().split(/\s+/);
  const shortened = words.length > 35;
  const selected = pending ?? direction.data.selected;
  const confirmed = direction.data.selected && pending !== false;
  const waiting = pending !== null && !confirmed;
  const select = async () => {
    if (selecting.current || disabled) return;
    selecting.current = true;
    setPending(!direction.data.selected);
    try { await onSelect(direction); } catch { /* Parent presents API errors. */ }
    finally { selecting.current = false; setPending(null); }
  };
  const label = pending !== null ? (pending ? "Liking" : "Unselecting") : direction.data.selected ? "Unselect idea" : "Like idea";
  return <article className="idea-choice" data-selected={selected} data-confirmed-selection={confirmed} data-command-pending={waiting} aria-busy={pending !== null}>
    <div className="inbox-entry-meta">{projectName && <span>{projectName}</span>}<InboxTimestamp value={direction.updated_at} /></div>
    <div className="idea-choice-heading">
      {direction.data.category && <p className="idea-category">{direction.data.category}</p>}
      <span className="idea-command-state" aria-hidden="true">
        <FontAwesomeIcon icon={faCheck} className="idea-selected-mark" />
        <FontAwesomeIcon icon={faEllipsis} className="idea-pending-mark" />
      </span>
    </div>
    <h4>{direction.data.title}</h4>
    <p id={`idea-description-${direction.id}`} className="idea-description">{shortened && !expanded ? `${words.slice(0,35).join(" ")}…` : direction.data.description}</p>
    {shortened && <button type="button" className="idea-expand" aria-label={expanded ? "Show less" : "Show full idea"}
      aria-expanded={expanded} aria-controls={`idea-description-${direction.id}`} onClick={() => setExpanded(value => !value)}>
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
    </button>}
    <button type="button" className="idea-select" disabled={disabled || pending !== null}
      aria-pressed={selected} aria-label={`${label}: ${direction.data.title}`} onClick={() => void select()} />
  </article>;
}

"use client";

import { useRef, useState } from "react";

export type DirectionChoice = {
  id: string;
  version: number;
  data: { title: string; description: string; category?: string; selected: boolean };
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

function IdeaCard({ direction, disabled, onSelect }: {
  direction: DirectionChoice; disabled: boolean; onSelect: (direction: DirectionChoice) => Promise<void>;
}) {
  const [expanded, setExpanded] = useState(false);
  const [pending, setPending] = useState(false);
  const selecting = useRef(false);
  const words = direction.data.description.trim().split(/\s+/);
  const shortened = words.length > 35;
  const selected = direction.data.selected || pending;
  const select = async () => {
    if (selecting.current || disabled || direction.data.selected) return;
    selecting.current = true;
    setPending(true);
    try { await onSelect(direction); } catch { /* Parent presents API errors. */ }
    finally { selecting.current = false; setPending(false); }
  };
  const label = pending ? "Liking" : direction.data.selected ? "Liked" : "Like idea";
  return <article className="idea-choice" data-selected={selected} aria-busy={pending}>
    {direction.data.category && <p className="idea-category">{direction.data.category}</p>}
    <h4>{direction.data.title}</h4>
    <p id={`idea-description-${direction.id}`} className="idea-description">{shortened && !expanded ? `${words.slice(0,35).join(" ")}…` : direction.data.description}</p>
    {shortened && <button type="button" className="idea-expand" aria-label={expanded ? "Show less" : "Show full idea"}
      aria-expanded={expanded} aria-controls={`idea-description-${direction.id}`} onClick={() => setExpanded(value => !value)}>
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
    </button>}
    <button type="button" className="idea-select" disabled={disabled || selected}
      aria-pressed={selected} aria-label={`${label}: ${direction.data.title}`} onClick={() => void select()} />
  </article>;
}

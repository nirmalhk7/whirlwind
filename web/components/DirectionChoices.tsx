"use client";

import { useRef, useState } from "react";
import { InboxTimestamp } from "./InboxTimestamp";
import { InboxRemoveButton } from "./InboxRemoveButton";
import { FontAwesomeIcon } from "@fortawesome/react-fontawesome";
import { faCheck, faEllipsis } from "@fortawesome/free-solid-svg-icons";
import type { Direction, Resource } from "./controlTypes";
import { api } from "./controlApi";

export type DirectionChoice = {
  id: string;
  version: number;
  updated_at?: string;
  data: { title: string; description: string; category?: string; selected: boolean; group_id?: string; project_id?: string;status?:string;scope?:string[];evidence?:Direction["evidence"];rank?:number;expected_benefit?:string;uncertainty?:string[];effort_estimate?:string;problem_kind?:string };
};

export function DirectionChoices({ directions, disabled, onSelect }: {
  directions: DirectionChoice[];
  disabled: boolean;
  onSelect: (direction: DirectionChoice) => Promise<void>;
}) {
  if (!directions.length) return null;
  return <section className="idea-grid" aria-label="Idea choices">
    {[...directions].sort((a,b)=>(a.data.rank??Number.MAX_SAFE_INTEGER)-(b.data.rank??Number.MAX_SAFE_INTEGER)).map(direction => <IdeaCard key={direction.id} direction={direction} disabled={disabled} onSelect={onSelect} />)}
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
  const [lifecycle,setLifecycle]=useState<{version:number;status:string}|null>(null);
  const [lifecycleBusy,setLifecycleBusy]=useState(false);
  const [lifecycleError,setLifecycleError]=useState("");
  const status=lifecycle&&lifecycle.version>direction.version?lifecycle.status:direction.data.status;
  const inactive=status==="dismissed"||status==="deferred";
  async function changeStatus(action:"dismiss"|"defer"|"reopen"){
    setLifecycleBusy(true);setLifecycleError("");
    try{
      const updated=await api<Resource<Direction>>(`directions/${encodeURIComponent(direction.id)}/${action}`,"POST",{},Math.max(direction.version,lifecycle?.version??0));
      setLifecycle({version:updated.version,status:updated.data.status??"proposed"});
    }catch(reason){setLifecycleError(reason instanceof Error?reason.message:"Candidate update failed");}
    finally{setLifecycleBusy(false);}
  }
  const words = direction.data.description.trim().split(/\s+/);
  const shortened = words.length > 35;
  const selected = pending ?? direction.data.selected;
  const confirmed = direction.data.selected && pending !== false;
  const waiting = pending !== null && !confirmed;
  const select = async () => {
    if (selecting.current || disabled || inactive || lifecycleBusy) return;
    selecting.current = true;
    setPending(!direction.data.selected);
    try { await onSelect({...direction,version:Math.max(direction.version,lifecycle?.version??0),data:{...direction.data,status}}); } catch { /* Parent presents API errors. */ }
    finally { selecting.current = false; setPending(null); }
  };
  const label = pending !== null ? (pending ? "Liking" : "Unselecting") : direction.data.selected ? "Unselect idea" : "Like idea";
  return <article className="idea-choice" data-selected={selected} data-confirmed-selection={confirmed} data-command-pending={waiting} aria-busy={pending !== null}>
    <div className="inbox-entry-meta">{projectName && <span>{projectName}</span>}<InboxTimestamp value={direction.updated_at} /></div>
    <div className="idea-choice-heading">
      {direction.data.category && <p className="idea-category">{direction.data.category}</p>}
      {direction.data.rank&&<span className="muted">Priority {direction.data.rank}</span>}
      <span className="idea-command-state" aria-hidden="true">
        <FontAwesomeIcon icon={faCheck} className="idea-selected-mark" />
        <FontAwesomeIcon icon={faEllipsis} className="idea-pending-mark" />
      </span>
    </div>
    <h4>{direction.data.title}</h4>
    <p id={`idea-description-${direction.id}`} className="idea-description">{shortened && !expanded ? `${words.slice(0,35).join(" ")}…` : direction.data.description}</p>
    {direction.data.problem_kind&&<p className="muted">{direction.data.problem_kind==="observed_defect"?"Observed check failure":"Opportunity requiring validation"}</p>}
    {direction.data.expected_benefit&&<p>Expected benefit: {direction.data.expected_benefit}</p>}
    {direction.data.effort_estimate&&<p className="muted">Estimated effort: {direction.data.effort_estimate}</p>}
    {!!direction.data.uncertainty?.length&&<ul>{direction.data.uncertainty.map((item,index)=><li key={`${index}:${item}`}>{item}</li>)}</ul>}
    {direction.data.scope?.length ? <p className="muted">Scope: {direction.data.scope.join("; ")}</p>:null}
    {direction.data.evidence?.length ? <details style={{position:"relative",zIndex:2}}><summary>Evidence</summary><ul>{direction.data.evidence.map((entry,index)=><li key={`${entry.ref}:${index}`}>{entry.kind}: {entry.ref}{entry.revision?` · ${entry.revision.slice(0,12)}`:""}{entry.summary?` · ${entry.summary}`:""}</li>)}</ul></details>:null}
    {status&&<p className="muted">{status.replaceAll("_"," ")}</p>}
    {shortened && <button type="button" className="idea-expand" aria-label={expanded ? "Show less" : "Show full idea"}
      aria-expanded={expanded} aria-controls={`idea-description-${direction.id}`} onClick={() => setExpanded(value => !value)}>
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
    </button>}
    <button type="button" className="idea-select" disabled={disabled || pending !== null || inactive || lifecycleBusy}
      aria-pressed={selected} aria-label={`${label}: ${direction.data.title}`} onClick={() => void select()} />
    <div className="whirlwind-actions" style={{position:"relative",zIndex:2}}>
      {!inactive&&direction.data.evidence?.length?<button type="button" className="button" disabled={disabled||lifecycleBusy||pending!==null} onClick={()=>void select()}>{selected?"Unselect idea":"Select idea and start"}</button>:null}
      {inactive?<button type="button" className="button secondary" disabled={disabled||lifecycleBusy} onClick={()=>void changeStatus("reopen")}>Reopen idea</button>:!selected&&<>
        <button type="button" className="button secondary" disabled={disabled||lifecycleBusy} onClick={()=>void changeStatus("defer")}>Defer idea</button>
        <button type="button" className="button secondary" disabled={disabled||lifecycleBusy} onClick={()=>void changeStatus("dismiss")}>Dismiss idea</button>
      </>}
    </div>
    {lifecycleError&&<p className="error" role="alert">{lifecycleError}</p>}
  </article>;
}

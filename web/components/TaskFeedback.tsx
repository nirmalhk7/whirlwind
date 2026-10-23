"use client";

import { useState } from "react";
import { api } from "./controlApi";

export function TaskFeedback({id,version,busy,perform}:{id:string;version:number;busy:boolean;perform:(action:()=>Promise<unknown>,message:string)=>Promise<void>}){
 const [decision,setDecision]=useState("");
 const [reason,setReason]=useState("");
 return <details><summary>Record task outcome</summary>
  <p className="muted">Record your assessment separately from verification and PR status. This feedback does not merge or change task execution.</p>
  <form className="whirlwind-form" onSubmit={event=>{event.preventDefault();void perform(()=>api(`tasks/${encodeURIComponent(id)}/outcome`,"POST",{decision,reason},version),"Task outcome recorded");}}>
   <label htmlFor={`task-outcome-${id}`}>Outcome</label><select id={`task-outcome-${id}`} className="input" required value={decision} onChange={event=>setDecision(event.target.value)}>
    <option value="" disabled>Choose an outcome</option>
    <option value="accepted">Accepted</option><option value="rejected">Rejected</option><option value="regression">Regression found</option><option value="reverted">Reverted</option>
   </select>
   <label>Outcome notes<textarea className="input" value={reason} maxLength={4000} onChange={event=>setReason(event.target.value)}/></label>
   <button className="button secondary" disabled={busy||!decision}>Record outcome</button>
  </form>
 </details>;
}

"use client";

import { useEffect, useState } from "react";
import { api } from "./controlApi";

type Budget = {max_attempts:number;max_runtime_seconds:number;verification_attempts?:number;verification_runtime_seconds?:number;max_tokens?:number;max_cost?:number;attempt_tokens?:number;attempt_cost?:number};
type Usage = {attempts:number;elapsed_ms:number;total_tokens:number;cost:number;unknown_token_attempts:number;unknown_cost_attempts:number;active_attempts:number;uncertain_attempts:number;uncertain_reservations?:Array<{attempt_id:string;provider:string;model:string;status:string;tokens:number;cost:number;runtime_seconds:number}>};
type Snapshot = {budget:Budget|null;effective_budget?:Budget|null;budget_source?:string;usage:Usage;version:number;enforcement:string};
type Field = keyof Budget;
const fields:Array<{key:Field;label:string;required?:boolean}>=[
 {key:"max_attempts",label:"Maximum agent attempts",required:true},
 {key:"max_runtime_seconds",label:"Maximum active runtime (seconds)",required:true},
 {key:"verification_attempts",label:"Reserved verification attempts",required:true},
 {key:"verification_runtime_seconds",label:"Reserved verification runtime (seconds)",required:true},
 {key:"max_tokens",label:"Token ceiling (optional)"},
 {key:"attempt_tokens",label:"Tokens reserved per attempt"},
 {key:"max_cost",label:"Cost ceiling in dollars (optional)"},
 {key:"attempt_cost",label:"Dollars reserved per attempt"},
];

export function TaskBudget({id,version,busy,perform}:{id:string;version:number;busy:boolean;perform:(action:()=>Promise<unknown>,message:string)=>Promise<void>}){
 const [open,setOpen]=useState(false);
 const [snapshot,setSnapshot]=useState<Snapshot|null>(null);
 const [values,setValues]=useState<Partial<Record<Field,string>>>({});
 const [error,setError]=useState("");
 const [loading,setLoading]=useState(false);
 const [terminated,setTerminated]=useState<Record<string,boolean>>({});
 useEffect(()=>{
  if(!open)return;
  let active=true;setLoading(true);setError("");
  void api<Snapshot>(`tasks/${encodeURIComponent(id)}/budget`).then(result=>{
   if(!active)return;
   if(typeof result.version!=="number"||!result.usage)throw new Error("Task budget unavailable");
   setSnapshot(result);const effective=result.effective_budget??result.budget;setValues(Object.fromEntries(fields.map(field=>[field.key,effective?.[field.key]?.toString()??""])));
  }).catch(reason=>{if(active)setError(reason instanceof Error?reason.message:"Task budget unavailable");}).finally(()=>{if(active)setLoading(false);});
  return()=>{active=false;};
 },[id,version,open]);
 function save(){
  const budget=Object.fromEntries(fields.filter(field=>values[field.key]?.trim()).map(field=>[field.key,Number(values[field.key])])) as Budget;
  void perform(()=>api(`tasks/${encodeURIComponent(id)}/budget`,"PATCH",budget,snapshot?.version??version),"Task budget updated; existing usage retained");
 }
 return <details onToggle={event=>setOpen(event.currentTarget.open)}>
  <summary>Task usage and budget</summary>
  {loading&&<p role="status">Loading task usage…</p>}
  {error&&<p className="error" role="alert">{error}</p>}
  {snapshot&&<>
   <p>{snapshot.usage.attempts} attempts · {(snapshot.usage.elapsed_ms/1000).toFixed(1)} active seconds · {snapshot.usage.active_attempts} active attempts</p>
   <p>Reported tokens: {snapshot.usage.total_tokens}. {snapshot.usage.unknown_token_attempts} attempts with unknown token usage. Reported cost: ${snapshot.usage.cost.toFixed(4)}. {snapshot.usage.unknown_cost_attempts} attempts with unknown cost.</p>
   {snapshot.usage.uncertain_attempts>0&&<p className="muted">{snapshot.usage.uncertain_attempts} interrupted attempts retain reserved allowance until reconciled.</p>}
   {snapshot.usage.uncertain_reservations?.map(reservation=><div key={reservation.attempt_id}>
    <p>{reservation.provider} · {reservation.model} · Attempt {reservation.attempt_id}: {reservation.tokens} tokens, ${reservation.cost} and {reservation.runtime_seconds} seconds remain reserved while usage is unknown.</p>
    {reservation.status==="uncertain"?<>
     <p className="muted">Confirm only after checking that this attempt's process has terminated. Confirmation releases its concurrent slot. Unknown token, cost and runtime reservations remain counted.</p>
     <label><input type="checkbox" checked={terminated[reservation.attempt_id]??false} onChange={event=>setTerminated(current=>({...current,[reservation.attempt_id]:event.target.checked}))}/>I confirmed attempt {reservation.attempt_id} has terminated</label>
     <button type="button" className="button secondary" aria-label={`Confirm terminated: ${reservation.attempt_id}`} disabled={busy||loading||!terminated[reservation.attempt_id]} onClick={()=>void perform(
      ()=>api(`tasks/${encodeURIComponent(id)}/budget-reconcile`,"POST",{attempt_id:reservation.attempt_id,confirmed_terminated:true,usage:null},snapshot.version),"Termination confirmed; unknown usage remains reserved",
     )}>Confirm terminated</button>
    </>:<p className="muted">Concurrent slot released. Unknown usage remains reserved.</p>}
   </div>)}
   {!snapshot.budget&&<p className="muted">No task override. Configured project or global limits apply.</p>}
   {(snapshot.effective_budget??snapshot.budget)&&<p className="muted">Effective budget from {(snapshot.budget_source??"task").replaceAll("_"," ")}: {(snapshot.effective_budget??snapshot.budget)!.max_attempts} attempts · {(snapshot.effective_budget??snapshot.budget)!.max_runtime_seconds} active seconds. Verification reserve: {(snapshot.effective_budget??snapshot.budget)!.verification_attempts??0} attempts · {(snapshot.effective_budget??snapshot.budget)!.verification_runtime_seconds??0} seconds.</p>}
   <p className="muted">{snapshot.enforcement}</p>
   <form className="whirlwind-form" onSubmit={event=>{event.preventDefault();save();}}>
    {fields.map(field=><label key={field.key}>{field.label}<input type="number" className="input" required={field.required} min={field.required?1:0} step={field.key.includes("cost")?"any":"1"} value={values[field.key]??""} onChange={event=>setValues(previous=>({...previous,[field.key]:event.target.value}))}/></label>)}
    <p className="muted">Token and cost ceilings require explicit per-attempt reservations. Raising a limit keeps all previous consumption counted.</p>
    <button className="button" disabled={busy||loading}>Save task budget</button>
   </form>
  </>}
 </details>;
}

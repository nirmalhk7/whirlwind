"use client";
import { useEffect, useState } from "react";

export function RecordList({kind,refreshKey=0}:{kind:"inbox"|"projects";refreshKey?:number}) {
  const [rows,setRows]=useState<any[]>([]); const [error,setError]=useState("");
  useEffect(()=>{ fetch(`/api/v1/${kind}`,{cache:"no-store"}).then(async r=>{if(!r.ok) throw new Error("Whirlwind's local control service is unavailable.");setRows(await r.json());}).catch(e=>setError(e.message)); },[kind,refreshKey]);
  if(error) return <div className="card muted">{error}</div>;
  if(!rows.length) return <div className="card muted">No {kind} records yet.</div>;
  return <div className="list">{rows.map((row,i)=>{const href=row.run_id?`/records/${encodeURIComponent(row.run_id)}`:kind==="projects"?`/projects/detail?name=${encodeURIComponent(row.name)}`:null;const content=<><div><strong>{row.summary ?? row.name ?? row.title ?? row.type}</strong><div className="muted">{row.project ?? row.path ?? row.classification ?? ""}</div></div><span className="badge">{row.status ?? row.default_branch ?? "ready"}</span></>;return href?<a className="card row" href={href} key={row.id ?? row.run_id ?? row.name ?? i}>{content}</a>:<div className="card row" key={row.id ?? row.run_id ?? row.name ?? i}>{content}</div>;})}</div>;
}

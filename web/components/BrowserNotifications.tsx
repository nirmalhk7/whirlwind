"use client";

import { useEffect, useRef, useState } from "react";
import type { Inbox, Resource } from "./controlTypes";

const preferenceKey="whirlwind.desktop.enabled.v1";
const deliveredKey="whirlwind.desktop.delivered.v1";

function deliveredIDs():Set<string>{
 const value=JSON.parse(localStorage.getItem(deliveredKey)??"[]") as unknown;
 return new Set(Array.isArray(value)?value.filter((id):id is string=>typeof id==="string"):[]);
}

// Browser delivery mirrors durable Inbox records while the app is open.
export function BrowserNotifications({items,loaded,visible}:{items:Resource<Inbox>[];loaded:boolean;visible:boolean}){
 const [enabled,setEnabled]=useState(false);
 const [supported,setSupported]=useState(false);
 const [permission,setPermission]=useState<NotificationPermission>("default");
 const [error,setError]=useState("");
 const [requesting,setRequesting]=useState(false);
 const initialized=useRef(false);
 const latest=useRef(items);latest.current=items;
 useEffect(()=>{
  setSupported("Notification" in window);
  if(!("Notification" in window))return;
  setPermission(Notification.permission);
  try{setEnabled(localStorage.getItem(preferenceKey)==="true"&&Notification.permission==="granted");}
  catch{setError("Browser storage unavailable; use Inbox for notifications.");}
 },[]);
 useEffect(()=>{
  if(!loaded)return;
  try{
   const seen=deliveredIDs();
   const baseline=!initialized.current;
   for(const entry of items){
    if(seen.has(entry.id))continue;
    seen.add(entry.id);
    // Persist the claim before constructing a notification so refresh cannot repeat it.
    localStorage.setItem(deliveredKey,JSON.stringify([...seen]));
    if(baseline||!enabled||entry.data.resolved||!("Notification" in window)||Notification.permission!=="granted")continue;
    const notice=new Notification("Whirlwind",{body:[entry.data.message,entry.data.next_action].filter(Boolean).join("\n"),tag:entry.id});
    notice.onclick=()=>{window.focus();window.location.assign("/inbox");notice.close();};
   }
   initialized.current=true;
  }catch{setError("Desktop delivery unavailable. All updates remain in Inbox.");}
 },[items,loaded,enabled]);
 async function enable(){
  if(!supported)return;
  setRequesting(true);setError("");
  try{
   const result=await Notification.requestPermission();setPermission(result);
   if(result!=="granted"){setEnabled(false);return;}
   const seen=deliveredIDs();for(const entry of latest.current)seen.add(entry.id);
   localStorage.setItem(deliveredKey,JSON.stringify([...seen]));
   localStorage.setItem(preferenceKey,"true");setEnabled(true);
  }catch{setError("Could not enable desktop notifications. Updates remain in Inbox.");}
  finally{setRequesting(false);}
 }
 function disable(){
  setEnabled(false);
  try{localStorage.setItem(preferenceKey,"false");}catch{setError("Could not save browser preference.");}
 }
 if(!visible)return null;
 return <div className="desktop-notifications">
  {supported?<>
   <button className="button secondary" type="button" disabled={requesting||!loaded} onClick={()=>enabled?disable():void enable()}>{enabled?"Disable desktop notifications":"Enable desktop notifications"}</button>
   <p className="muted">{enabled?"Desktop notifications enabled for this browser.":permission==="denied"?"Browser notifications blocked. Change browser permission to enable; updates remain in Inbox.":"Desktop notifications are optional and work while this app is open. Existing updates stay in Inbox."}</p>
  </>:<p className="muted">Desktop notifications unavailable in this browser. Updates remain in Inbox.</p>}
  {error&&<p className="error" role="alert">{error}</p>}
 </div>;
}

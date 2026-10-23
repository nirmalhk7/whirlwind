import {expect,test} from "@playwright/test";

test("desktop notifications require opt in and suppress history and reconnect duplicates",async({page})=>{
 await page.addInitScript(()=>{
  const calls:Array<{title:string;body:string}>=[];
  Object.defineProperty(window,"desktopCalls",{value:calls});
  class NotificationFixture{
   static permission:NotificationPermission=localStorage.getItem("fixture-notification-permission")==="granted"?"granted":"default";
   static async requestPermission(){this.permission="granted";localStorage.setItem("fixture-notification-permission","granted");return "granted";}
   onclick:(()=>void)|null=null;
   constructor(title:string,options?:NotificationOptions){calls.push({title,body:options?.body??""});}
   close(){}
  }
  Object.defineProperty(window,"Notification",{value:NotificationFixture,configurable:true});
 });
 let notices=[{id:"old",version:1,created_order:1,data:{id:"old",kind:"pr_created",message:"Historical result",resolved:false}}];
 await page.route("**/api/v1/**",async route=>{
  const path=new URL(route.request().url()).pathname;
  if(path.endsWith("/events"))return route.fulfill({contentType:"text/event-stream",body:": heartbeat\n\n"});
  if(path.endsWith("/overview"))return route.fulfill({json:{projects:[],tasks:[],providers:[],directions:[],inbox:notices}});
  return route.fulfill({json:[]});
 });
 const count=()=>page.evaluate(()=>(window as unknown as {desktopCalls:unknown[]}).desktopCalls.length);
 await page.goto("/inbox");
 await page.getByRole("button",{name:"Enable desktop notifications",exact:true}).click();
 await expect(page.getByText("Desktop notifications enabled for this browser.",{exact:true})).toBeVisible();
 expect(await count()).toBe(0);
 notices=[...notices,{id:"new",version:1,created_order:2,data:{id:"new",kind:"waiting_quota",message:"Waiting for quota reset",resolved:false}}];
 await page.getByRole("button",{name:"Refresh",exact:true}).click();
 await expect.poll(count).toBe(1);
 await page.getByRole("button",{name:"Refresh",exact:true}).click();
 expect(await count()).toBe(1);
 await page.reload();
 await expect(page.getByText("Desktop notifications enabled for this browser.",{exact:true})).toBeVisible();
 expect(await count()).toBe(0);
 await page.getByRole("button",{name:"Disable desktop notifications",exact:true}).click();
 notices=[...notices,{id:"muted",version:1,created_order:3,data:{id:"muted",kind:"pr_created",message:"Muted result",resolved:false}}];
 await page.getByRole("button",{name:"Refresh",exact:true}).click();
 expect(await count()).toBe(0);
});

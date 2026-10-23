import { expect, test } from "@playwright/test";

test("candidate evidence and lifecycle preserve the explicit selection gate", async ({page})=>{
 let version=1,status="proposed",selected=false;
 let selectionCalls=0;
 const direction=()=>({id:"idea",version,data:{id:"idea",project_id:"p",title:"Recover interrupted work",description:"Preserve pending jobs after restart.",category:"harden",expected_benefit:"Avoid lost jobs",effort_estimate:"Small persistence change",uncertainty:["Validate crash recovery"],problem_kind:"opportunity",day:"2026-10-22",selected,status,scope:["Retry persistence"],evidence:[{kind:"code",ref:"internal/retry.go",revision:"abc123",summary:"Pending state lost"}]}});
 await page.route("**/api/v1/**",async route=>{
  const path=new URL(route.request().url()).pathname;
  if(path.endsWith("/events"))return route.fulfill({contentType:"text/event-stream",body:": heartbeat\n\n"});
  if(path.endsWith("/overview"))return route.fulfill({json:{projects:[],providers:[],tasks:[],directions:[direction()],inbox:[]}});
  if(path.includes("/directions/idea/")){
   const action=path.split("/").at(-1);
   expect(route.request().headers()["if-match"]).toBe(String(version));
   version++;
   if(action==="dismiss")status="dismissed";
   if(action==="reopen")status="proposed";
   if(action==="select"){selectionCalls++;selected=true;return route.fulfill({status:202,json:{id:"task",version:1,data:{}}});}
   return route.fulfill({json:direction()});
  }
  return route.fulfill({json:[]});
 });
 await page.goto("/inbox");
 await expect(page.getByText("Scope: Retry persistence")).toBeVisible();
 await expect(page.getByText("Expected benefit: Avoid lost jobs",{exact:true})).toBeVisible();
 await expect(page.getByText("Opportunity requiring validation",{exact:true})).toBeVisible();
 await expect(page.getByText("Validate crash recovery",{exact:true})).toBeVisible();
 await page.getByText("Evidence",{exact:true}).click();
 await expect(page.getByText(/internal\/retry.go/)).toBeVisible();
 expect(selectionCalls).toBe(0);
 await page.getByRole("button",{name:"Dismiss idea",exact:true}).click();
 await expect(page.getByRole("button",{name:"Like idea: Recover interrupted work",exact:true})).toBeDisabled();
 await page.getByRole("button",{name:"Reopen idea",exact:true}).click();
 await page.getByRole("button",{name:"Select idea and start",exact:true}).click();
 await expect.poll(()=>selectionCalls).toBe(1);
});

test("legacy adoption and budget changes require explicit versioned actions",async({page})=>{
 let version=1;
 let adoptionCalls=0;
 let savedBudget:Record<string,number>|undefined;
 let reconciled:unknown;
 let outcome:unknown;
 const task=()=>({id:"legacy",version,data:{id:"legacy",project_id:"p",issue:{title:"Recover work",body:"Fix retries"},stage:adoptionCalls?"planning":"awaiting_selection",score:0,review_passes:0,plan_version:1,...(adoptionCalls?{selection:{candidate_id:"adopt-legacy",objective:"Recover work",scope:["Fix retries"],policy_hash:"policy",selected_at:"2026-10-22T00:00:00Z"}}:{})}});
 await page.route("**/api/v1/**",async route=>{
  const path=new URL(route.request().url()).pathname;
  if(path.endsWith("/events"))return route.fulfill({contentType:"text/event-stream",body:": heartbeat\n\n"});
  if(path.endsWith("/overview"))return route.fulfill({json:{projects:[],providers:[],tasks:[task()],directions:[],inbox:[{id:"notice",version:1,data:{id:"notice",task_id:"legacy",kind:"task_awaiting_selection",message:"Select or adopt legacy work",resolved:false}}]}});
  if(path.endsWith("/tasks/legacy/outcome")){outcome=route.request().postDataJSON();version++;return route.fulfill({json:task()});}
  if(path.endsWith("/tasks/legacy/adopt")){expect(route.request().headers()["if-match"]).toBe(String(version));adoptionCalls++;version++;return route.fulfill({json:task()});}
  if(path.endsWith("/tasks/legacy/budget-reconcile")){reconciled=route.request().postDataJSON();version++;return route.fulfill({json:task()});}
  if(path.endsWith("/tasks/legacy/budget")){
   if(route.request().method()==="PATCH"){expect(route.request().headers()["if-match"]).toBe(String(version));savedBudget=route.request().postDataJSON();version++;return route.fulfill({json:task()});}
   return route.fulfill({json:{budget:savedBudget??null,effective_budget:savedBudget??{max_attempts:20,max_runtime_seconds:900,verification_attempts:3,verification_runtime_seconds:120},budget_source:savedBudget?"task":"project_policy",version,usage:{attempts:2,elapsed_ms:1500,total_tokens:120,cost:0,unknown_token_attempts:1,unknown_cost_attempts:2,active_attempts:0,uncertain_attempts:1,uncertain_reservations:[{attempt_id:"interrupted",provider:"codex",model:"model",status:reconciled?"uncertain_released":"uncertain",tokens:200,cost:0,runtime_seconds:20}]},enforcement:"Active runtime cancellation; unknown measurements stay unknown"}});
  }
  return route.fulfill({json:[]});
 });
 await page.goto("/inbox");
 await page.getByRole("button",{name:"Open task",exact:true}).click();
 expect(adoptionCalls).toBe(0);
 await page.getByRole("button",{name:"Adopt task objective",exact:true}).click();
 await expect(page.getByText(/Selected objective: Recover work/)).toBeVisible();
 await page.getByText("Task usage and budget",{exact:true}).click();
 await expect(page.getByText(/1 attempts with unknown token usage/)).toBeVisible();
 await expect(page.getByText(/Effective budget from project policy: 20 attempts/)).toBeVisible();
 await page.getByLabel("Maximum agent attempts",{exact:true}).fill("12");
 await page.getByLabel("Maximum active runtime (seconds)",{exact:true}).fill("600");
 await page.getByLabel("Reserved verification attempts",{exact:true}).fill("3");
 await page.getByLabel("Reserved verification runtime (seconds)",{exact:true}).fill("120");
 await page.getByRole("button",{name:"Save task budget",exact:true}).click();
 await expect.poll(()=>savedBudget).toEqual({max_attempts:12,max_runtime_seconds:600,verification_attempts:3,verification_runtime_seconds:120});
 await expect(page.getByRole("button",{name:"Confirm terminated: interrupted",exact:true})).toBeDisabled();
 await page.getByLabel("I confirmed attempt interrupted has terminated",{exact:true}).check();
 await page.getByRole("button",{name:"Confirm terminated: interrupted",exact:true}).click();
 await expect.poll(()=>reconciled).toEqual({attempt_id:"interrupted",confirmed_terminated:true,usage:null});
 await page.getByText("Record task outcome",{exact:true}).click();
 await page.getByLabel("Outcome",{exact:true}).selectOption("accepted");
 await page.getByLabel("Outcome notes",{exact:true}).fill("Acceptance checked against real workload");
 await page.getByRole("button",{name:"Record outcome",exact:true}).click();
 await expect.poll(()=>outcome).toEqual({decision:"accepted",reason:"Acceptance checked against real workload"});
});

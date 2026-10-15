import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";

test("backend inventory and unavailable provider render", async ({ page }, testInfo) => {
  await page.goto("/providers");
  await expect(page.getByRole("heading", { name: "Providers", exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Antigravity", exact: true })).toBeVisible();
  await expect(page.getByText(/Consumer-plan third-party/)).toBeVisible();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.screenshot({ path: testInfo.outputPath("providers-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: testInfo.outputPath("providers-mobile.png"), fullPage: true });
});

test("planner answers and critical approval use current versions", async ({ page }) => {
  let answered = false, approved = false;
  const task = { id: "task-one", version: 4, data: { id: "task-one", project_id: "p", issue: { title: "OAuth", body: "Implement OAuth" }, stage: "questions", score: 9, plan_version: 1, plan: { objective: "OAuth", acceptance_criteria: ["Login succeeds"], questions: [{ id: "q", text: "Which callback?", answer: "" }], units: [] } } };
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/events")) { await route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" }); return; }
    if (url.pathname.endsWith("/answers")) {
      expect(route.request().headers()["if-match"]).toBe("4");
      expect(route.request().postDataJSON()).toEqual({ answers: { q: "http://localhost/callback" } });
      answered = true; task.version = 5; task.data.stage = "approval";
      await route.fulfill({ json: task }); return;
    }
    if (url.pathname.endsWith("/approve")) { expect(route.request().headers()["if-match"]).toBe("5"); approved = true; task.version = 6; task.data.stage = "pr_created"; Object.assign(task.data, { pr: "https://github.com/o/r/pull/1" }); await route.fulfill({ json: task }); return; }
    if (url.pathname.endsWith("/tasks")) { await route.fulfill({ json: [task] }); return; }
    if (url.pathname.endsWith("/reviews")) { await route.fulfill({ json: [] }); return; }
    if (url.pathname.endsWith("/overview")) { await route.fulfill({ json: { projects: [], tasks: [task], providers: [], directions: [], inbox: [] } }); return; }
    await route.fulfill({ json: [] });
  });
  await page.goto("/tasks");
  await page.getByRole("button", { name: "Open task" }).click();
  await page.getByLabel("Which callback?").fill("http://localhost/callback");
  await page.getByRole("button", { name: "Submit answers" }).click();
  await expect.poll(() => answered).toBe(true);
  await page.getByRole("button", { name: "Approve PR publication" }).click();
  await expect.poll(() => approved).toBe(true);
  await expect(page.getByRole("link", { name: "Open pull request" })).toHaveAttribute("href", "https://github.com/o/r/pull/1");
});

test("trusted repository runs direction through verified PR with real backend", async ({ page, request }) => {
  test.setTimeout(60_000);
  const fixture: Fixture = JSON.parse(process.env.WHIRLWIND_E2E_FIXTURE!);
  await page.goto("/providers");
  await page.getByLabel("Provider ID").fill("fixture");
  await page.getByLabel("Display name").fill("Fixture agent");
  await page.getByLabel("Executable and arguments (JSON array)").fill(JSON.stringify([fixture.agent]));
  await page.getByRole("button", { name: "Register provider" }).click();
  await expect(page.getByRole("heading", { name: "Fixture agent", exact: true })).toBeVisible();
  await page.goto("/projects");
  await page.getByLabel("Project name").fill("Fixture project");
  await page.getByLabel("Repository path").fill(fixture.repo);
  await page.getByRole("button", { name: "Register project" }).click();
  await expect(page.getByRole("heading", { name: "Fixture project", exact: true })).toBeVisible();
  await page.goto("/inbox");
  await expect(page.getByRole("button", { name: /^Like idea:/ }).first()).toBeVisible({ timeout: 20_000 });
  await page.getByRole("button", { name: /^Like idea:/ }).first().click();
  await expect(page.getByRole("link", { name: "Open pull request" })).toHaveAttribute("href", "https://github.com/whirlwind-fixture/demo/pull/1", { timeout: 40_000 });
  const response = await request.get("/api/v1/tasks");
  const tasks = await response.json();
  expect(tasks).toHaveLength(1);
  expect(tasks[0].data).toMatchObject({ stage: "pr_created", score: 9, checks_passed: true });
  expect(existsSync(join(fixture.repo, "result.txt"))).toBe(false);
  expect(existsSync(join(tasks[0].data.worktree, "result.txt"))).toBe(true);
});

test("native questions appear in Inbox and return answers", async ({ page }) => {
  let answered = false;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/events")) { await route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" }); return; }
    if (path.endsWith("/overview")) { await route.fulfill({ json: { projects: [], providers: [], tasks: [], directions: [], inbox: [] } }); return; }
    if (path.endsWith("/questions")) { await route.fulfill({ json: answered ? [] : [{ id: "native", version: 1, data: { id: "native", provider: "codex", status: "pending", questions: [{ id: "db", text: "Which database?", options: ["SQLite", "PostgreSQL"] }] } }] }); return; }
    if (path.endsWith("/questions/native/answers")) { expect(route.request().headers()["if-match"]).toBe("1"); expect(route.request().postDataJSON()).toEqual({ answers: { db: "SQLite" } }); answered = true; await route.fulfill({ json: {} }); return; }
    await route.fulfill({ json: [] });
  });
  await page.goto("/inbox");
  await page.getByLabel("Which database?").fill("SQLite");
  await page.getByRole("button", { name: "Send answers to agent" }).click();
  await expect.poll(() => answered).toBe(true);
});

test("opening Whirlwind lands in Inbox without Workspace navigation", async ({ page }) => {
  await page.goto("/");
  await expect(page).toHaveURL(/\/inbox$/);
  const navigation = page.getByRole("navigation", { name: "Main navigation" });
  await expect(navigation.getByRole("link", { name: /^Inbox/ })).toHaveAttribute("aria-current", "page");
  await expect(navigation.getByRole("link", { name: "Workspace", exact: true })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Ideas in. Verified software out.", exact: true })).toBeVisible();
});

test("folder selection fills repository path and cancellation preserves it", async ({ page }) => {
  const fixture: Fixture = JSON.parse(process.env.WHIRLWIND_E2E_FIXTURE!);
  await page.goto("/projects");
  await page.getByRole("button", { name: "Choose folder", exact: true }).click();
  const picker = page.getByRole("dialog", { name: "Select repository folder" });
  await expect(picker).toBeVisible();
  await picker.getByLabel("Folder location").fill(dirname(fixture.repo));
  await picker.getByRole("button", { name: "Go", exact: true }).click();
  await picker.getByRole("button", { name: basename(fixture.repo), exact: true }).click();
  await picker.getByRole("button", { name: "Use this folder", exact: true }).click();
  await expect(picker).not.toBeVisible();
  await expect(page.getByLabel("Repository path", { exact: true })).toHaveValue(realpathSync(fixture.repo));
  await expect(page.getByLabel("Project name", { exact: true })).toHaveValue("repo");
  await page.getByLabel("Repository path", { exact: true }).fill(join(fixture.root, "another-project") + "/");
  await expect(page.getByLabel("Project name", { exact: true })).toHaveValue("another-project");
  await page.getByLabel("Project name", { exact: true }).fill("Custom project");
  await page.getByLabel("Repository path", { exact: true }).fill(realpathSync(fixture.repo));
  await expect(page.getByLabel("Project name", { exact: true })).toHaveValue("Custom project");
  await page.getByRole("button", { name: "Choose folder", exact: true }).click();
  await expect(picker).toBeVisible();
  await picker.getByRole("button", { name: "Home", exact: true }).click();
  await picker.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(page.getByLabel("Repository path", { exact: true })).toHaveValue(realpathSync(fixture.repo));
  await expect(page.getByLabel("Project name", { exact: true })).toHaveValue("Custom project");
});

test("new project creates committed policy and onboards for execution", async ({ page, request }) => {
  const fixture: Fixture = JSON.parse(process.env.WHIRLWIND_E2E_FIXTURE!);
  const repo = join(fixture.root, "starter-project");
  mkdirSync(repo);
  execFileSync("git", ["-C", repo, "init", "-b", "main"]);
  execFileSync("git", ["-C", repo, "config", "user.name", "Fixture"]);
  execFileSync("git", ["-C", repo, "config", "user.email", "fixture@example.test"]);
  execFileSync("git", ["-C", repo, "config", "commit.gpgsign", "false"]);
  execFileSync("git", ["-C", repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "Fixture"]);
  execFileSync("git", ["-C", repo, "remote", "add", "origin", "https://github.com/whirlwind-fixture/starter.git"]);
  await page.goto("/projects");
  await page.getByLabel("Repository path", { exact: true }).fill(repo);
  await page.getByRole("button", { name: "Register project", exact: true }).click();
  await expect(page.getByRole("heading", { name: "starter-project", exact: true })).toBeVisible();
  await expect(page.locator("article").filter({has:page.getByRole("heading",{name:"starter-project",exact:true})}).getByText("Ready for execution", { exact: true })).toBeVisible();
  expect(readFileSync(join(repo, ".whirlwind.yml"), "utf8")).toContain("qlty");
  expect(execFileSync("git", ["-C", repo, "show", "HEAD:.whirlwind.yml"], { encoding: "utf8" })).toBe(readFileSync(join(repo, ".whirlwind.yml"), "utf8"));
  expect(execFileSync("git", ["-C", repo, "show", "--format=", "--name-only", "HEAD"], { encoding: "utf8" }).trim()).toBe(".qlty/qlty.toml\n.whirlwind.yml");
  expect(existsSync(join(repo, ".qlty/qlty.toml"))).toBe(true);
  expect(existsSync(join(repo, ".codegraph/codegraph.db"))).toBe(true);
  const projects = await (await request.get("/api/v1/projects")).json();
  expect(projects.some((project: { data: { path: string; policy_hash: string } }) => project.data.path === realpathSync(repo) && project.data.policy_hash !== "")).toBe(true);
});

test("Inbox combines messages newest first and keeps badge current across pages", async ({ page }) => {
  let resolved = false, answered = false, decided = false;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/events")) return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
    if (path.endsWith("/overview")) return route.fulfill({ json: { projects: [], providers: [], tasks: [], directions: [], inbox: [
      { id: "question-alert", version: 1, data: { kind: "question", message: "Agent clarification awaits answer: question", resolved: false } },
      { id: "permission-alert", version: 1, data: { kind: "permission", message: "Tool permission awaits decision: permission", resolved: false } },
      { id: "notice", version: 1, updated_at: "2026-10-14T12:00:00Z", data: { kind: "ideation", message: "Fresh directions ready", resolved } },
    ] } });
    if (path.endsWith("/questions")) return route.fulfill({ json: [{ id: "question", version: 2, updated_at: "2026-10-14T11:00:00Z", data: { provider: "codex", status: answered ? "answered" : "pending", questions: [{ id: "q", text: "Which direction?" }] } }] });
    if (path.endsWith("/permissions")) return route.fulfill({ json: [{ id: "permission", version: 3, updated_at: "2026-10-14T13:00:00Z", data: { provider: "claude", decision: decided ? "deny" : "pending", request: { tool: "Read" } } }] });
    if (path.endsWith("/inbox/notice")) { resolved = true; return route.fulfill({ json: {} }); }
    if (path.endsWith("/questions/question/answers")) { expect(route.request().headers()["if-match"]).toBe("2"); answered = true; return route.fulfill({ json: {} }); }
    if (path.endsWith("/permissions/permission/decisions")) { expect(route.request().headers()["if-match"]).toBe("3"); decided = true; return route.fulfill({ json: {} }); }
    return route.fulfill({ json: [] });
  });
  await page.goto("/agents");
  const badge = page.getByRole("navigation").getByRole("link", { name: /^Inbox/ });
  await expect(badge).toHaveText("Inbox3");
  await badge.click();
  const feed = page.getByRole("region", { name: "Messages" });
  await expect(feed.locator(".action-card")).toHaveCount(3);
  await expect(feed.locator(".action-card").nth(0)).toContainText("claude requests permission");
  await expect(feed.locator(".action-card").nth(1)).toContainText("Fresh directions ready");
  await expect(feed.locator(".action-card").nth(2)).toContainText("Which direction?");
  await page.getByRole("button", { name: "Mark resolved" }).click();
  await expect(badge).toHaveText("Inbox2");
  await page.getByRole("button", { name: "Deny", exact: true }).click();
  await expect(badge).toHaveText("Inbox1");
  await page.getByLabel("Which direction?").fill("Explore reliability");
  await page.getByRole("button", { name: "Send answers to agent" }).click();
  await expect(badge).toHaveText("Inbox");
  await expect(feed).toContainText("No messages waiting");
});

test("Inbox triggers ideation for an onboarded project and shows operation result", async ({ page }) => {
  let queued = false;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/events")) return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
    if (path.endsWith("/overview")) return route.fulfill({ json: { projects: [{ id: "voyager", version: 1, data: { name: "VOYAGER", repo: "o/r", path: "/voyager" } }], providers: [], tasks: [], directions: [], inbox: [] } });
    if (path.endsWith("/projects/voyager/ideation")) { expect(route.request().method()).toBe("POST"); queued = true; return route.fulfill({ status: 202, json: { operation_id: "generation" } }); }
    if (path.endsWith("/operations/generation")) return route.fulfill({ json: { id: "generation", version: 2, data: { stage: "failed", error: "Provider authentication required" } } });
    return route.fulfill({ json: [] });
  });
  await page.goto("/inbox");
  await page.getByRole("button", { name: "Run ideation for VOYAGER" }).click();
  await expect.poll(() => queued).toBe(true);
  await expect(page.getByRole("alert").filter({ hasText: "Ideation failed:" })).toContainText("Provider authentication required");
  await expect(page.getByRole("button", { name: "Run ideation for VOYAGER" })).toBeEnabled();
});

test("Inbox shows four ideas side by side and lets an empty-policy project select any idea", async ({ page }, testInfo) => {
  let selected = false;
  const directions = ["expand", "optimize", "harden", "simplify"].map((category, index) => ({ id: `idea-${index}`, version: 7, data: { project_id: "voyager", category, title: `${category} proposal`, description: `Project-specific ${category} improvement.`, day: "2026-10-14", selected: false } }));
  const task = { id: "direction-idea-2", version: 1, data: { project_id: "voyager", issue: { title: "harden proposal", body: "Improve recovery" }, score: 0, plan_version: 1, review_passes: 0, stage: "paused", error: "Configure and commit project policy before planning." } };
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/events")) return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
    if (path.endsWith("/overview")) return route.fulfill({ json: { projects: [{ id: "voyager", version: 1, data: { name: "VOYAGER", policy_hash: "" } }], providers: [], tasks: selected ? [task] : [], directions, inbox: [] } });
    if (path.endsWith("/directions/idea-2/select")) {
      expect(route.request().method()).toBe("POST");
      expect(route.request().headers()["if-match"]).toBe("7");
      selected = true; directions[2].data.selected = true;
      return route.fulfill({ status: 202, json: task });
    }
    return route.fulfill({ json: [] });
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/inbox");
  const choices = page.getByRole("region", { name: "Idea choices" });
  await expect(choices.getByRole("article")).toHaveCount(4);
  const cards = await choices.getByRole("article").all();
  const positions = await Promise.all(cards.map((card) => card.boundingBox()));
  expect(positions.every((position) => position?.y === positions[0]?.y)).toBe(true);
  await choices.getByRole("button", { name: "Like idea: harden proposal", exact: true }).click();
  await expect.poll(() => selected).toBe(true);
  await expect(choices.getByRole("article")).toHaveCount(4);
  await expect(choices.getByRole("button", { name: "Liked: harden proposal", exact: true })).toBeDisabled();
  await page.screenshot({ path: testInfo.outputPath("ideas-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(choices.getByRole("article")).toHaveCount(4);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("ideas-mobile.png"), fullPage: true });
});

test("idea card previews expand and whole card selects with cyan feedback", async ({ page }, testInfo) => {
  let attempts=0;
  const description=Array.from({length:40},(_,i)=>`word${i+1}`).join(" ");
  const directions=["expand","optimize","harden","simplify"].map((category,i)=>({id:`short-${i}`,version:7,data:{project_id:"p",category,title:`${category} direction`,description,selected:false}}));
  await page.route("**/api/v1/**",async route=>{
    const path=new URL(route.request().url()).pathname;
    if(path.endsWith("/events"))return route.fulfill({contentType:"text/event-stream",body:": heartbeat\n\n"});
    if(path.endsWith("/overview"))return route.fulfill({json:{projects:[{id:"p",version:1,data:{name:"Project",policy_hash:""}}],tasks:[],providers:[],directions,inbox:[]}});
    if(path.includes("/directions/short-") && path.endsWith("/select")){
      attempts++;
      if(attempts===1)return route.fulfill({status:422,json:{error:{message:"Selection unavailable"}}});
      await new Promise(resolve=>setTimeout(resolve,400));
      directions[Number(path.split("/").at(-2)!.split("-")[1])].data.selected=true;
      return route.fulfill({status:202,json:{id:"direction-short-0",version:1,data:{}}});
    }
    return route.fulfill({json:[]});
  });
  await page.goto("/inbox");
  const card=page.locator(".idea-choice").filter({has:page.getByRole("heading",{name:"expand direction",exact:true})});
  await expect(card.locator(".idea-description")).not.toContainText("word36");
  await expect(card.getByRole("button",{name:"Like idea: expand direction",exact:true})).toHaveText("");
  const expand=card.getByRole("button",{name:"Show full idea",exact:true});
  await expect(expand).toHaveText("");
  await expect(expand.locator("svg")).toBeVisible();
  await card.getByRole("button",{name:"Show full idea",exact:true}).click();
  await expect(card.locator(".idea-description")).toContainText("word40");expect(attempts).toBe(0);
  await card.getByRole("button",{name:"Show less",exact:true}).click();
  await card.getByRole("heading",{name:"expand direction",exact:true}).click({force:true});
  await expect(page.getByText("Selection unavailable",{exact:true})).toBeVisible();
  await expect(card).toHaveAttribute("data-selected","false");
  await card.locator(".idea-description").click({force:true});
  await expect(card.getByRole("button",{name:"Liking: expand direction"})).toBeDisabled();
  await expect(card).toHaveCSS("background-color","rgb(56, 189, 248)");
  await expect(card.getByRole("button",{name:"Liked: expand direction"})).toBeDisabled();
  expect(attempts).toBe(2);
  await page.reload();await expect(card).toHaveCSS("background-color","rgb(56, 189, 248)");
  const keyboardChoice=page.getByRole("button",{name:"Like idea: optimize direction",exact:true});
  await keyboardChoice.focus();await page.keyboard.press("Enter");
  await expect(page.getByRole("button",{name:"Liked: optimize direction",exact:true})).toBeDisabled();
  expect(attempts).toBe(3);
  for(const [name,width,height] of [["desktop",1440,1000],["mobile",390,844]] as const){
    await page.setViewportSize({width,height});await page.evaluate(()=>window.scrollTo(0,0));expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
    await page.screenshot({path:testInfo.outputPath(`selected-ideas-${name}.png`),fullPage:true});
  }
});

test("Run history separates attempts and labels historical unknowns",async ({page},testInfo)=>{
  const sessions=[
    {id:"old",version:1,updated_at:"2026-10-14T10:00:00Z",data:{role:"ideate",provider:"deleted",task:"ideation:p",model:"",finished:true}},
    {id:"failed",version:2,updated_at:"2026-10-14T11:00:01Z",data:{role:"code",provider:"codex",provider_name:"Codex",project_id:"p",task:"t",model:"configured",transport:"acp",started_at:"2026-10-14T11:00:00Z",elapsed_ms:1000,finished:true,success:false,error:"Quota exhausted"}},
    {id:"success",version:2,updated_at:"2026-10-14T12:00:01Z",data:{role:"code",provider:"cursor",provider_name:"Cursor",project_id:"p",task:"t",model:"configured",resolved_model:"actual-model",thinking:{config_id:"effort",value:"high"},effective_thinking:{config_id:"effort",value:"high"},transport:"acp",started_at:"2026-10-14T12:00:00Z",elapsed_ms:1234,finished:true,success:true}},
  ];
  await page.route("**/api/v1/**",async route=>{
    const path=new URL(route.request().url()).pathname;
    if(path.endsWith("/events"))return route.fulfill({contentType:"text/event-stream",body:": heartbeat\n\n"});
    if(path.endsWith("/overview"))return route.fulfill({json:{projects:[{id:"p",version:1,data:{name:"Project"}}],providers:[],tasks:[],directions:[],inbox:[]}});
    if(path.endsWith("/sessions"))return route.fulfill({json:sessions});return route.fulfill({json:[]});
  });
  await page.goto("/inbox");await page.getByRole("navigation").getByRole("link",{name:"Run",exact:true}).click();
  await expect(page).toHaveURL(/\/runs$/);
  const rows=page.getByRole("region",{name:"Execution history"}).getByRole("article");
  await expect(rows).toHaveCount(3);await expect(rows.nth(0)).toContainText("actual-model");await expect(rows.nth(0)).toContainText("high");await expect(rows.nth(0)).toContainText("Succeeded");
  await expect(rows.nth(1)).toContainText("configured (configured)");await expect(rows.nth(1)).toContainText("Quota exhausted");
  await expect(rows.nth(2)).toContainText("Finished — outcome unknown");await expect(rows.nth(2)).toContainText("Unknown");
  for(const [name,width,height] of [["desktop",1440,1000],["mobile",390,844]] as const){await page.setViewportSize({width,height});await page.evaluate(()=>window.scrollTo(0,0));expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:testInfo.outputPath(`runs-${name}.png`),fullPage:true});}
  sessions.push({id:"plan",version:1,updated_at:"2026-10-14T13:00:00Z",data:{role:"plan",provider:"cursor",task:"t",model:"m",finished:false}});
  sessions.push({id:"review",version:1,updated_at:"2026-10-14T14:00:00Z",data:{role:"review",provider:"cursor",task:"t",model:"m",finished:true}});
  await page.getByRole("button",{name:"Refresh",exact:true}).click();
  await expect(rows).toHaveCount(5);await expect(rows.nth(0)).toContainText("Reviewer Agent");await expect(rows.nth(1)).toContainText("Planner Agent");
});

test("Agents discover model-dependent thinking and persist saves and tests",async ({page},testInfo)=>{
  let saved:unknown,tested:unknown;
  const config={id:"code",version:2,data:{role:"code",chain:[{provider:"codex",model:"m1"}]}};
  await page.route("**/api/v1/**",async route=>{
    const path=new URL(route.request().url()).pathname;
    if(path.endsWith("/events"))return route.fulfill({contentType:"text/event-stream",body:": heartbeat\n\n"});
    if(path.endsWith("/overview"))return route.fulfill({json:{projects:[],tasks:[],inbox:[],directions:[],providers:[{id:"codex",version:1,data:{id:"codex",name:"Codex",kind:"codex",available:true,quota:"unknown",capabilities:["code"]}}]}});
    if(path.endsWith("/agents"))return route.fulfill({json:[config]});
    if(path.endsWith("/models"))return route.fulfill({status:202,json:{operation_id:route.request().postDataJSON().model==="m2"?"catalog-m2":"catalog-m1"}});
    if(path.includes("/operations/catalog-")){
      const second=path.endsWith("catalog-m2");return route.fulfill({json:{id:"catalog",version:1,data:{stage:"complete",models:[{id:"m1",name:"One"},{id:"m2",name:"Two"}],thinking_options:[{config_id:"effort",name:"Thinking",current:second?"low":"high",options:[{id:second?"low":"high",name:second?"Low":"High"}]}]}}});
    }
    if(path.endsWith("/agents/code")){saved=route.request().postDataJSON();Object.assign(config.data,saved);config.version++;return route.fulfill({json:config});}
    if(path.endsWith("/providers/codex/test") && route.request().method()==="POST"){tested=route.request().postDataJSON();return route.fulfill({status:202,json:{operation_id:"test"}});}
    if(path.endsWith("/operations/test"))return route.fulfill({json:{id:"test",version:2,data:{stage:"complete",text:"Hello World",elapsed_ms:100}}});return route.fulfill({json:[]});
  });
  await page.goto("/agents");const form=page.getByRole("form",{name:"Coding Agent"});const thinking=form.getByLabel("Primary thinking level",{exact:true});
  await expect(thinking).toBeVisible();await expect(thinking.getByRole("option",{name:"High",exact:true})).toHaveCount(1);await thinking.selectOption({label:"High"});
  await form.getByRole("button",{name:"Test model",exact:true}).click();await expect.poll(()=>tested).toEqual({model:"m1",thinking:{config_id:"effort",value:"high"}});
  await form.getByRole("button",{name:"Save agent",exact:true}).click();await expect.poll(()=>saved).toEqual({chain:[{provider:"codex",model:"m1",thinking:{config_id:"effort",value:"high"}}]});
  await page.reload();await expect(thinking).toHaveValue(JSON.stringify({config_id:"effort",value:"high"}));
  await form.getByLabel("Primary model",{exact:true}).fill("m2");await expect(thinking).toHaveValue("");await expect(thinking.getByRole("option",{name:"Low",exact:true})).toHaveCount(1);await expect(form.getByText("Hello World",{exact:true})).toHaveCount(0);
  for(const [name,width,height] of [["desktop",1440,1000],["mobile",390,844]] as const){await page.setViewportSize({width,height});await page.evaluate(()=>window.scrollTo(0,0));expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:testInfo.outputPath(`agents-thinking-${name}.png`),fullPage:true});}
});

test("Settings save ideation time timezone and automatic toggle",async ({page},testInfo)=>{
 let saved:unknown;
 const settings={id:"global",version:3,data:{global_slots:4,project_slots:2,reconciliation_seconds:300,ideation:{enabled:true,time:"10:00",timezone:"America/Denver"}}};
 await page.route("**/api/v1/**",async route=>{
  const path=new URL(route.request().url()).pathname;
  if(path.endsWith("/events"))return route.fulfill({contentType:"text/event-stream",body:": heartbeat\n\n"});
  if(path.endsWith("/overview"))return route.fulfill({json:{projects:[],providers:[],tasks:[],directions:[],inbox:[]}});
  if(path.endsWith("/settings")){
   if(route.request().method()==="PATCH"){expect(route.request().headers()["if-match"]).toBe("3");saved=route.request().postDataJSON();Object.assign(settings.data,saved);settings.version++;}
   return route.fulfill({json:settings});
  }
  return route.fulfill({json:[]});
 });
 await page.goto("/settings");
 await page.getByLabel("Daily ideation time",{exact:true}).fill("09:30");
 const timezone=page.getByRole("combobox",{name:"Ideation timezone",exact:true});
 await timezone.fill("not-a-timezone");
 await expect(page.getByText("No matching timezones",{exact:true})).toBeVisible();
 await timezone.fill("UTC");
 await timezone.press("ArrowDown");await timezone.press("Enter");
 await expect(page.locator('input[name="ideation_timezone"]')).toHaveValue("UTC");
 await timezone.fill("New York");
 await page.getByRole("option",{name:/America\/New_York/}).click();
 await page.getByLabel("Automatic daily ideation",{exact:true}).uncheck();
 await page.getByRole("button",{name:"Save settings",exact:true}).click();
 await expect.poll(()=>saved).toEqual({global_slots:4,project_slots:2,reconciliation_seconds:300,ideation:{enabled:false,time:"09:30",timezone:"America/New_York"}});
 await page.reload();await expect(page.getByLabel("Automatic daily ideation",{exact:true})).not.toBeChecked();
 await expect(page.getByLabel("Daily ideation time",{exact:true})).toHaveValue("09:30");
 await expect(page.locator('input[name="ideation_timezone"]')).toHaveValue("America/New_York");
 for(const [name,width,height] of [["desktop",1440,1000],["mobile",390,844]] as const){await page.setViewportSize({width,height});await page.evaluate(()=>window.scrollTo(0,0));expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await timezone.fill("Kathmandu");await expect(page.getByRole("option",{name:/Asia\/Kathmandu/})).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:testInfo.outputPath(`ideation-settings-${name}.png`),fullPage:true});await timezone.press("Escape");}
});

test("Run inspection exposes persisted inputs timeline and truncation",async ({page})=>{
 await page.route("**/api/v1/**",async route=>{
  const path=new URL(route.request().url()).pathname;
  if(path==="/api/v1/events")return route.fulfill({contentType:"text/event-stream",body:": heartbeat\n\n"});
  if(path.endsWith("/overview"))return route.fulfill({json:{projects:[],providers:[],tasks:[],directions:[],inbox:[]}});
  if(path==="/api/v1/sessions")return route.fulfill({json:[{id:"attempt",version:2,data:{attempt_id:"attempt",task:"t",role:"code",provider:"codex",finished:true,status:"interrupted",prompt_artifact:"prompt",usage:{scope:"session",total_tokens:123}}}]});
  if(path.endsWith("/sessions/attempt/events"))return route.fulfill({json:{items:[{sequence:1,kind:"prompt",replay:false,created_at:"2026-10-14T10:00:00Z",artifact:{id:"prompt",kind:"prompt",truncated:true,bytes:200,stored_bytes:100}}],next_after:1}});
  if(path.endsWith("/artifacts/prompt/content"))return route.fulfill({contentType:"text/plain",body:"exact stored prompt"});
  return route.fulfill({json:[]});
 });
 await page.goto("/runs");await expect(page.getByText("Interrupted",{exact:true})).toBeVisible();
 await page.getByRole("button",{name:"Inspect run",exact:true}).click();
 await expect(page.getByText("Truncated: 100 of 200 bytes",{exact:true})).toBeVisible();
 await page.getByRole("button",{name:"Show prompt",exact:true}).click();await expect(page.getByText("exact stored prompt",{exact:true})).toBeVisible();
 await expect(page.getByText("Session cumulative · 123 tokens · Cost unknown",{exact:true})).toBeVisible();
});

test("project trash unregisters with current version and preserves repository", async ({ page, request }, testInfo) => {
  const originalSettings = await (await request.get("/api/v1/settings")).json();
  const disabled = await request.patch("/api/v1/settings", {
    headers: { "if-match": String(originalSettings.version), "idempotency-key": randomUUID() },
    data: { ideation: { ...originalSettings.data.ideation, enabled: false } },
  });
  expect(disabled.status()).toBe(200);
  // This case deletes an idle project; scheduled ideation owns leases that correctly block deletion.
  try {
  const fixture: Fixture = JSON.parse(process.env.WHIRLWIND_E2E_FIXTURE!);
  const repo = join(fixture.root, "delete-project");
  mkdirSync(repo);
  execFileSync("git", ["-C", repo, "init", "-b", "main"]);
  for (const [key, value] of [["user.name", "Fixture"], ["user.email", "fixture@example.test"], ["commit.gpgsign", "false"], ["core.hooksPath", ".git/hooks"]]) execFileSync("git", ["-C", repo, "config", key, value]);
  execFileSync("git", ["-C", repo, "remote", "add", "origin", "https://github.com/whirlwind-fixture/delete.git"]);
  await page.goto("/projects");
  await page.getByLabel("Repository path", { exact: true }).fill(repo);
  await page.getByRole("button", { name: "Register project", exact: true }).click();
  const card = page.locator("article").filter({ has: page.getByRole("heading", { name: "delete-project", exact: true }) });
  await expect(card).toBeVisible();
  const trash = card.getByRole("button", { name: "Delete project delete-project", exact: true });
  await expect(trash).toBeVisible();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.screenshot({ path: testInfo.outputPath("projects-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: testInfo.outputPath("projects-mobile.png"), fullPage: true });
  const cardBox = await card.boundingBox(), trashBox = await trash.boundingBox();
  expect(cardBox && trashBox && trashBox.x + trashBox.width > cardBox.x + cardBox.width - 8).toBeTruthy();
  const before = readFileSync(join(repo, ".whirlwind.yml"), "utf8");
  const registration = (await (await request.get("/api/v1/projects")).json()).find((p: { data: { path: string } }) => p.data.path === realpathSync(repo));
  const deletion = page.waitForRequest(req => req.method() === "DELETE" && req.url().endsWith(`/projects/${registration.id}`));
  const deleted = page.waitForResponse(response => response.request().method() === "DELETE" && response.url().endsWith(`/projects/${registration.id}`));
  await trash.click();
  expect((await deletion).headers()["if-match"]).toBe(String(registration.version));
  expect((await deleted).status()).toBe(200);
  await expect(card).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole("heading", { name: "delete-project", exact: true })).toHaveCount(0);
  expect(readFileSync(join(repo, ".whirlwind.yml"), "utf8")).toBe(before);
  expect((await request.get(`/api/v1/projects/${registration.id}`)).status()).toBe(404);
  } finally {
    const currentSettings = await (await request.get("/api/v1/settings")).json();
    const restored = await request.patch("/api/v1/settings", {
      headers: { "if-match": String(currentSettings.version), "idempotency-key": randomUUID() },
      data: { ideation: originalSettings.data.ideation },
    });
    expect(restored.status()).toBe(200);
  }
});

test("project deletion failure keeps card and reports error", async ({ page }) => {
  let release!: () => void;
  const responseGate = new Promise<void>(resolve => { release = resolve; });
  let deletes = 0;
  await page.route("**/api/v1/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/events")) return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
    if (path.endsWith("/overview")) return route.fulfill({ json: { projects: [{ id: "busy", version: 2, data: { name: "Busy project", repo: "o/r", path: "/repo" } }], tasks: [], directions: [], providers: [], inbox: [] } });
    if (route.request().method() === "DELETE") {
      deletes++;
      await responseGate;
      return route.fulfill({ status: 409, json: { error: { message: "Stop active work before deleting project." } } });
    }
    return route.fulfill({ json: [] });
  });
  await page.goto("/projects");
  const trash = page.getByRole("button", { name: "Delete project Busy project", exact: true });
  await trash.focus();
  await trash.press("Enter");
  await expect(trash).toBeDisabled();
  await trash.press("Enter");
  release();
  await expect(page.getByText("Stop active work before deleting project.", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Busy project", exact: true })).toBeVisible();
  await expect(trash).toBeEnabled();
  expect(deletes).toBe(1);
});


test("existing starter finishes execution setup with current version", async ({ page }) => {
 let ready = false;
 await page.route("**/api/v1/**", async route => {
  const path = new URL(route.request().url()).pathname;
  if (path.endsWith("/events")) return route.fulfill({contentType:"text/event-stream",body:": heartbeat\n\n"});
  if (path.endsWith("/overview")) return route.fulfill({json:{projects:[{id:"legacy",version:ready?2:1,data:{name:"Legacy",repo:"o/r",path:"/repo",policy_hash:ready?"committed":""}}],providers:[],tasks:[],directions:[],inbox:[]}});
  if (path.endsWith("/projects/legacy/policy")) {expect(route.request().headers()["if-match"]).toBe("1"); ready=true;return route.fulfill({json:{id:"legacy",version:2}})}
  return route.fulfill({json:[]});
 });
 await page.goto("/projects");
 await page.getByRole("button",{name:"Finish execution setup",exact:true}).click();
 await expect(page.getByText("Ready for execution",{exact:true})).toBeVisible();
 await expect(page.getByText("Ideation only · policy setup needed for execution",{exact:true})).toHaveCount(0);
 await expect(page.getByRole("button",{name:"Confirm committed policy",exact:true})).toBeVisible();
});

test("Settings contains workers agents and providers with old route redirects", async ({ page }) => {
 await page.goto("/settings");
 const navigation=page.getByRole("navigation",{name:"Main navigation"});
 await expect(navigation.getByRole("link",{name:"Agents",exact:true})).toHaveCount(0);
 await expect(navigation.getByRole("link",{name:"Providers",exact:true})).toHaveCount(0);
 await expect(page.getByRole("heading",{name:"Worker settings",exact:true})).toBeVisible();
 await expect(page.getByRole("heading",{name:"Agents",exact:true})).toBeVisible();
 await expect(page.getByRole("heading",{name:"Providers",exact:true})).toBeVisible();
 await expect(page.getByText(/settings.yaml/).first()).toBeVisible();
 await page.goto("/agents");await expect(page).toHaveURL(/\/settings#agents$/);
 await page.goto("/providers");await expect(page).toHaveURL(/\/settings#providers$/);
});

test("Settings YAML changes update UI and UI saves update YAML", async ({ page, request }, testInfo) => {
 const fixture: Fixture=JSON.parse(process.env.WHIRLWIND_E2E_FIXTURE!);
 const file=join(fixture.root,"settings.yaml");
 const original=readFileSync(file,"utf8");
 const workerRecord=await (await request.get("/api/v1/settings")).json();
 const nextSlots=workerRecord.data.global_slots===6?7:6;
 const edited=original.replace(/global_slots: \d+/,`global_slots: ${nextSlots}`).replace("name: Antigravity","name: File-controlled provider");
 const {writeFileSync}=await import("node:fs");
 try {
  writeFileSync(file,edited);
  await page.goto("/settings");
  await expect(page.getByLabel("Global execution slots",{exact:true})).toHaveValue(String(nextSlots));
  await expect(page.getByRole("heading",{name:"File-controlled provider",exact:true})).toBeVisible();
  const changedSlots=nextSlots===7?8:7;
  await page.getByLabel("Global execution slots",{exact:true}).fill(String(changedSlots));
  await page.getByRole("button",{name:"Save settings",exact:true}).click();
  await expect.poll(()=>readFileSync(file,"utf8")).toContain(`global_slots: ${changedSlots}`);
  await page.setViewportSize({width:1440,height:1000});
  await page.screenshot({path:testInfo.outputPath("settings-desktop.png"),fullPage:true});
  await page.setViewportSize({width:390,height:844});
  await page.screenshot({path:testInfo.outputPath("settings-mobile.png"),fullPage:true});
 } finally {writeFileSync(file,original)}
 await expect.poll(async()=> (await (await request.get("/api/v1/settings")).json()).data.global_slots).toBe(workerRecord.data.global_slots);
});

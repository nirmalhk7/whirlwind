import {expect,test} from '@playwright/test';
test('settings exposes pending restart and native inventory without a mode editor',async({page})=>{
 let refreshed=false;
 await page.route('**/api/v1/**',async route=>{
  const path=new URL(route.request().url()).pathname;
  if(path.endsWith('/events'))return route.fulfill({contentType:'text/event-stream',body:': heartbeat\n\n'});
  if(path.endsWith('/settings/file'))return route.fulfill({json:{path:'/tmp/settings.yaml',content:'mode: personal\n',revision:'file-v1',loaded_at:'2026-10-22'}});
  if(path.endsWith('/workflow_config/global'))return route.fulfill({json:{id:'global',version:2,data:{active_mode:'personal',configured_mode:'agentic',restart_required:true,integrations:{discovery:'connected'}}}});
  if(path.endsWith('/providers/native/integrations')){
   if(route.request().method()==='POST'){refreshed=true;expect(route.request().headers()['if-match']).toBe('3');}
   return route.fulfill({json:{provider:'native',integrations:refreshed?[{provider:'native',ref:'app://jenkins',name:'Jenkins',kind:'app',enabled:true,callable:true}]:[],...(!refreshed?{error:'Not checked; refresh native connection'}:{})}});
  }
  const providers=[{id:'native',version:3,data:{id:'native',name:'Native provider',kind:'codex',capabilities:['ideate','code'],available:true}}];
  if(path.endsWith('/overview'))return route.fulfill({json:{projects:[],tasks:[],directions:[],inbox:[],providers}});
  if(path.endsWith('/providers'))return route.fulfill({json:providers});
  return route.fulfill({json:[]});
 });
 await page.goto('/settings');
 const panel=page.locator('section').filter({has:page.getByRole('heading',{name:'Workflow mode and integrations',exact:true})});
 await expect(panel).toContainText('Active mode: Personal');
 await expect(panel).toContainText('Configured mode: Agentic');
 await expect(panel).toContainText('Restart required');
 await expect(panel.getByRole('combobox')).toHaveCount(0);
 await expect(panel).toContainText('Not checked; refresh native connection');
 await panel.getByRole('button',{name:'Refresh integrations',exact:true}).click();
 await expect(panel).toContainText('app://jenkins');
 await expect(panel).toContainText('Callable');
 expect(refreshed).toBe(true);
});

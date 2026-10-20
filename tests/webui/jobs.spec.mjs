import { expect, test } from '@playwright/test';
import { mockAPI, navigate } from './fixtures.mjs';

const baseJob = {name:'accounts', mode:'pinned', failure_policy:'strict', selection:'manual', profile:'hk-fast', endpoint:'hk-only', ingress:'endpoint', password:'', target_url:'', size:0, concurrency:8, timeout:'15s', refresh_interval:'10m', session_ttl:'0', session_idle_timeout:'0', max_sessions:1024, max_accounts_per_node:0, probe_batch_size:32, probe_concurrency:4, max_response_bytes:2097152, expected_status:200, body_contains:''};
const autoJob = {...baseJob,name:'catalog',mode:'pooled',selection:'auto',endpoint:'catalog',target_url:'https://example.com/catalog',size:5};
async function setup(page, {locale='zh',width=1280,role='admin'}={}) {
  const state = {jobs:[structuredClone(baseJob),structuredClone(autoJob)], calls:[], saves:[], saveStatus:200, saveWait:null, accessWait:null, accessStatus:200, releaseWait:null, persistAcquire:false, refreshing:false, profileRegex:'', migrations:[], migrationStatus:200, migrationWait:null,
    targets:[{node:'node-1',name:'US node 1',accounts:1,limit:1,available:false},{node:'node-2',name:'US node 2',accounts:1,limit:1,available:false},{node:'node-3',name:'US node 3',accounts:0,limit:1,available:true}],
    sessions:Array.from({length:23},(_,i)=>({job:i===22?'deleted-job':'accounts',session:`account-${String(i+1).padStart(2,'0')}`,node:`node-${i%3+1}`,node_name:`US node ${i%3+1}`,state:i===22?'removed':i===21?'expired':'ready',revision:`binding-${i}`,permanent:i<21,expires_at:'2027-01-01T12:00:00Z'}))};
  await page.setViewportSize({width,height:900});
  await page.addInitScript(({locale,width})=>{localStorage.setItem('uiLanguage',locale);localStorage.setItem('themeMode',width===390?'dark':'light');},{locale,width});
  await mockAPI(page, async response=>{
    const {request,url}=response,path=url.pathname;
    state.calls.push(`${request.method()} ${path}`);
    if(path==='/api/session')response.body={role};
    if(path==='/api/settings') {
      if(request.method()==='PUT') {
        state.saves.push({body:request.postDataJSON(),etag:request.headers()['if-match']});
        if(state.saveWait)await state.saveWait;
        response.status=state.saveStatus;
        response.body=response.status===200?{message:'ok'}:{error:'test save rejected'};
        if(response.status===200)state.jobs=request.postDataJSON().jobs;
      } else {
        response.body.jobs=state.jobs;
        response.body.profiles[0].name_regex=state.profileRegex;
        response.body.endpoints.push({name:'catalog',enabled:true,address:'127.0.0.1',port:23232,username:'catalog',password:'catalog-secret',profile:'hk-fast',status:'running'});
      }
    }
    if(path==='/api/access')Object.assign(response.body.endpoints[1],{job:'accounts',job_mode:'pinned'});
    if(path==='/api/jobs')response.body={jobs:state.jobs.map(j=>({name:j.name,mode:j.mode,selection:j.selection,candidates:6,measured:j.selection==='auto'?4:0,refreshing:j.selection==='auto'&&state.refreshing,selected:j.selection==='auto'?[{node:'node-1',duration_ms:80,success_rate:.95}]:[{node:'node-1'},{node:'node-2'}]}))};
    if(path==='/api/jobs/sessions') {
      const q=url.searchParams;let rows=state.sessions.filter(s=>(!q.get('job')||s.job===q.get('job'))&&(!q.get('state')||s.state===q.get('state'))&&(!q.get('q')||`${s.job} ${s.session} ${s.node} ${s.node_name||''}`.includes(q.get('q'))));
      const pages=Math.ceil(rows.length/20),current=Math.min(Number(q.get('page')||1),pages||1);
      response.body={sessions:rows.slice((current-1)*20,current*20),pagination:{page:current,page_size:20,total_items:rows.length,total_pages:pages}};
    }
    if(path.endsWith('/acquire')) {
      const session=request.postDataJSON().session;
      if(state.accessWait)await state.accessWait;
      if(state.accessStatus!==200){response.status=state.accessStatus;response.body={error:'test acquire rejected'};return response;}
      if(state.persistAcquire&&!state.sessions.some(s=>s.job==='accounts'&&s.session===session))state.sessions.push({job:'accounts',session,node:'node-1',state:'ready',expires_at:'2027-01-01T12:00:00Z'});
      response.body={job:'accounts',mode:'pinned',state:'ready',node:'node-1',node_name:'US node 1',session,proxy_url:`http://hk-session-${session}:hk-secret@127.0.0.1:23231`,concurrency:8,timeout:'15s',expires_at:'2027-01-01T12:00:00Z'};
    }
    if(path.endsWith('/nodes')&&path.startsWith('/api/jobs/'))response.body={nodes:state.targets};
    if(path.endsWith('/migrate')) {
      const requestBody=request.postDataJSON();state.migrations.push({app:path.split('/')[3],...requestBody});
      if(state.migrationWait)await state.migrationWait;
      const binding=state.sessions.find(s=>s.job===path.split('/')[3]&&s.session===requestBody.session);
      response.status=state.migrationStatus;
      if(response.status!==200){response.body={error:'migration rejected; original binding unchanged'};return response;}
      if(!binding||binding.revision!==requestBody.expected_revision){response.status=409;response.body={error:'binding changed; reload before migrating'};return response;}
      Object.assign(binding,{node:requestBody.node,node_name:state.targets.find(n=>n.node===requestBody.node)?.name,permanent:true,revision:'migrated-'+binding.revision,state:'ready',expires_at:'0001-01-01T00:00:00Z'});
      response.body={...binding};
    }
    if(path.endsWith('/release')) {if(state.releaseWait)await state.releaseWait;state.sessions=state.sessions.filter(s=>!(s.job===path.split('/')[3]&&s.session===request.postDataJSON().session));response.body={ok:true};}
    return response;
  });
  await page.goto('/');
  await expect(page.locator('#healthyNodeRate')).toBeVisible();
  return state;
}
const settings=async page=>{await navigate(page, 'settings');await expect(page.locator('#job-1-name')).toHaveValue('catalog');await expect.poll(()=>page.locator('#settingsTab form').evaluate(el=>el.inert)).toBe(false);};
const jobs=async page=>{await navigate(page, 'jobs');await expect(page.locator('.job-status-card')).toHaveCount(2);await expect(page.locator('[data-session-count]')).toContainText('23');};
const save=page=>page.locator('#settingsTab button[type="submit"]').click();

test('application policy, binding rows and dynamic counts retranslate both ways without changing accounts',async({page},testInfo)=>{
  const state=await setup(page,{locale:'en'});
  Object.assign(state.jobs[0],{ingress:'shared',endpoint:'',password:'fixture-application-password',max_accounts_per_node:1});
  state.sessions[0].node_name='全部'; // User-provided names are data, not translation keys.
  await jobs(page);
  await page.locator('[data-access-session]').fill('locale-test-account');
  await page.locator('[data-migrate-job]').first().click();
  await expect(page.locator('[data-binding-target] option')).toHaveCount(3);
  await page.locator('[data-binding-target]').selectOption('node-3');
  const calls=state.calls.filter(c=>c.startsWith('POST '));
  for(const locale of ['zh-CN','en','zh-CN']) {
    await page.evaluate(locale=>window.setLanguage(locale),locale);
    const en=locale==='en',card=page.locator('[data-status-job="accounts"]');
    await expect(card).toContainText(en?'Auto-bind on first connection':'首次连接自动绑定');
    await expect(card).toContainText(en?'Permanent · survives restarts':'永久 · 重启保留');
    await expect(card).toContainText(en?'One account per node, per app':'一账号一节点（按应用）');
    await expect(page.locator('[data-session-count]')).toHaveText(en?'23 sessions · page 1 / 2':'共 23 条 · 1 / 2 页');
    await expect(page.locator('.job-session-table tbody tr').first()).toContainText(en?'Permanent binding':'永久绑定');
    await expect(page.locator('.job-session-table tbody tr').first().locator('.job-node-identity strong')).toHaveText('全部');
    await expect(page.locator('[data-access-session]')).toHaveValue('locale-test-account');
    await expect(page.locator('[data-acquire-job]')).toHaveText(en?'Create / view account binding':'创建 / 查看账号绑定');
    await expect(page.locator('[data-session-job] option').first()).toHaveText(en?'All applications':'全部应用');
    await expect(page.locator('[data-session-state] option[value="paused"]')).toHaveText(en?'Paused':'已暂停');
    await expect(page.locator('[data-binding-target]')).toHaveValue('node-3');
    await expect(page.locator('[data-binding-target] option[value="node-1"]')).toContainText(en?'Current node':'当前节点');
    await expect(card).toContainText(en?'Endpoint:':'代理入口:');
    const timestamp=page.locator('[data-jobs-updated] time');
    const expectedTime=await timestamp.evaluate((el,locale)=>new Date(el.dateTime).toLocaleTimeString(locale),locale);
    await expect(timestamp).toHaveText(expectedTime);
    await expect(page.locator('[data-jobs-updated]')).toContainText(en?'Status updated at':'状态更新于');
  }
  expect(state.calls.filter(c=>c.startsWith('POST '))).toEqual(calls);
  expect(state.migrations).toEqual([]);expect(state.saves).toEqual([]);
  await page.locator('[data-status-job="accounts"]').scrollIntoViewIfNeeded();
  await page.screenshot({path:testInfo.outputPath('localized-application-policy.png')});
});

for(const [locale,width] of [['zh',1280],['en',390]])test(`application policy explains automatic pinning without creating bindings in ${locale}`,async({page},testInfo)=>{
  const state=await setup(page,{locale,width});
  Object.assign(state.jobs[0],{ingress:'shared',endpoint:'',password:'fixture-application-password',max_accounts_per_node:1});
  await jobs(page);
  const card=page.locator('[data-status-job="accounts"]');
  await expect(card).toContainText(locale==='en'?'Auto-bind on first connection':'首次连接自动绑定');
  await expect(card).toContainText(locale==='en'?'Permanent · survives restarts':'永久 · 重启保留');
  await expect(card).toContainText(locale==='en'?'One account per node, per app':'一账号一节点（按应用）');
  await expect(card).toContainText(locale==='en'?'Strict pin · pause on failure':'严格固定 · 故障暂停');
  await expect(card).toContainText(locale==='en'?'Use Profile nodes':'按节点分组选取');
  await expect(card).toContainText(locale==='en'?'Full capacity is rejected':'满额拒绝');
  await expect(card).not.toContainText('manual');
  await expect(page.locator('[data-job-access-help]')).toContainText(locale==='en'?'Optional:':'可选操作');
  await expect(page.locator('[data-job-access-help]')).toContainText(locale==='en'?'reserves an account slot':'占用账号名额');
  await page.screenshot({path:testInfo.outputPath('application-policy.png')});
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.locator('#jobAccessName').selectOption('catalog');
  await expect(page.locator('[data-job-access-help]')).toContainText(locale==='en'?'No account binding is created':'不建立账号绑定');
  await expect(page.locator('[data-acquire-job]')).toHaveText(locale==='en'?'Get application access':'获取应用入口');
  await expect(page.locator('#jobAccessSession')).toBeDisabled();
  expect(state.calls.filter(c=>c.startsWith('POST /api/jobs/'))).toEqual([]);
  expect(state.saves).toHaveLength(0);
});

test('policy summary distinguishes finite lifetimes, failover, direct ingress and missing config',async({page})=>{
  const state=await setup(page,{locale:'en'});
  Object.assign(state.jobs[0],{session_ttl:'48h',max_accounts_per_node:4,failure_policy:'failover'});
  await jobs(page);
  const policy=page.locator('[data-status-job="accounts"] .job-policy-summary');
  await expect(policy).toContainText('48h · survives restarts');
  await expect(policy).toContainText('4 accounts per app');
  await expect(policy).toContainText('Failover · change node automatically');
  await expect(policy).not.toContainText('Permanent');
  await expect(page.locator('[data-status-job="catalog"]')).not.toContainText('New binding lifetime');
  Object.assign(state.jobs[0],{ingress:'direct',endpoint:'',failure_policy:'strict',max_accounts_per_node:0});
  await page.locator('[data-reload-jobs]').click();
  await expect(policy).toContainText('Create via API / panel');
  await expect(policy).toContainText('Unlimited per application');
  await expect(page.locator('[data-job-access-help]')).toContainText('Direct node connections do not create bindings');
  await page.route('**/api/settings',route=>route.fulfill({json:{mode:'pool',jobs:[]}}));
  await page.locator('[data-reload-jobs]').click();
  await expect(page.locator('[data-status-job="accounts"]')).toContainText('Binding policy cannot be confirmed');
  await expect(policy).toHaveCount(0);
  expect(state.calls.filter(c=>c.startsWith('POST /api/jobs/'))).toEqual([]);
});

test('node-source labels clarify automatic binding without changing saved enum values',async({page})=>{
  const state=await setup(page);await settings(page);
  const row=page.locator('[data-job-row]').first();await row.locator('summary').first().click();
  await expect(row.locator('[data-manual-note]')).toContainText('不表示需要手动创建账号绑定');
  await expect(row.locator('[data-binding-note]')).toContainText('新账号首次连接自动绑定');
  await page.locator('#job-0-ingress').selectOption('direct');
  await expect(row.locator('[data-binding-note]')).toContainText('不会自动绑定');
  await page.locator('#job-0-ingress').selectOption('shared');
  await page.locator('#job-0-mode').selectOption('pooled');
  await expect(row.locator('[data-binding-note]')).toContainText('不建立账号绑定');
  await page.locator('#job-0-mode').selectOption('pinned');
  await expect(row.locator('[data-binding-note]')).toContainText('新账号首次连接自动绑定');
  await save(page);await expect.poll(()=>state.saves.length).toBe(1);
  expect(state.saves[0].body.jobs[0]).toMatchObject({mode:'pinned',selection:'manual',ingress:'shared',session_ttl:'0',failure_policy:'strict'});
});

for(const [locale,width] of [['zh',1280],['en',390]])test(`binding node names replace opaque IDs with expandable details in ${locale}`,async({page},testInfo)=>{
  const state=await setup(page,{locale,width});
  const id='node-df9d669626564aed8583ce5c36a7686c055481513fb05fceb739423c26512515';
  const name='[D]🇺🇸 US-CN2-9929-CMIN2 · 美国长名称节点';
  Object.assign(state.sessions[0],{node:id,node_name:name});
  Object.assign(state.targets[0],{node:id,name});
  await jobs(page);
  const row=page.locator('.job-session-table tbody tr').first();
  await expect(row.locator('.job-node-identity > strong')).toHaveText(name);
  await expect(row.locator('.job-node-id code')).toBeHidden();
  await row.locator('summary').focus();await page.keyboard.press('Enter');
  await expect(row.locator('.job-node-id code')).toHaveText(id);
  await page.evaluate(()=>{Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async value=>{window.__copiedNodeID=value;}}});});
  await row.locator('[data-copy-job-node]').click();
  expect(await page.evaluate(()=>window.__copiedNodeID)).toBe(id);
  expect(state.calls.filter(c=>c.startsWith('POST /api/jobs/'))).toEqual([]);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await row.evaluate(el=>el.scrollIntoView({block:'center',inline:'nearest'}));
  await page.screenshot({path:testInfo.outputPath('binding-node-name.png')});
  await row.locator('[data-migrate-job]').click();
  await expect(page.locator('[data-binding-identity] .job-node-identity > strong')).toHaveText(name);
  await page.locator('#bindingTarget').selectOption('node-3');
  await page.locator('[data-binding-save]').click();
  await expect(page.locator('#confirmMessage')).toContainText(name);
  await expect(page.locator('#confirmMessage')).toContainText('US node 3');
  await page.keyboard.press('Escape');await page.locator('[data-binding-cancel]').click();
  expect(state.migrations).toHaveLength(0);
  await page.locator('#jobSessionSearch').fill('US-CN2-9929');
  await expect(page.locator('.job-session-table tbody tr')).toHaveCount(1);
  await expect(page.locator('.job-node-identity > strong').first()).toHaveText(name);
});

test('unknown and untrusted node names remain identifiable without executing markup',async({page})=>{
  const state=await setup(page);
  Object.assign(state.sessions[0],{node:'node-removed',node_name:undefined,state:'paused'});
  state.sessions[1].node_name='<img src=x onerror="window.__nodeNameExecuted=true">';
  await jobs(page);
  const rows=page.locator('.job-session-table tbody tr');
  await expect(rows.nth(0).locator('.job-node-identity > strong')).toHaveText('节点名称不可用');
  await rows.nth(0).locator('summary').click();
  await expect(rows.nth(0).locator('code')).toHaveText('node-removed');
  await expect(rows.nth(1).locator('.job-node-identity > strong')).toHaveText(state.sessions[1].node_name);
  await expect(rows.nth(1).locator('img')).toHaveCount(0);
  expect(await page.evaluate(()=>window.__nodeNameExecuted)).toBeUndefined();
});

test('access helper shows the node name while keeping the full identity available',async({page})=>{
  await setup(page);await jobs(page);
  await page.locator('#jobAccessSession').fill('account-01');await page.locator('[data-acquire-job]').click();
  await expect(page.locator('[data-access-result] .job-node-identity > strong')).toHaveText('US node 1');
  await expect(page.locator('[data-access-result] code')).toBeHidden();
  await page.locator('[data-access-result] summary').click();
  await expect(page.locator('[data-access-result] code')).toHaveText('node-1');
});

for(const body of ['<html>gateway response</html>','[]','null','{}'])test(`invalid successful save response preserves the draft (${body})`,async({page},testInfo)=>{
  const locale=body==='{}'?'en':'zh',width=body.startsWith('<html>')?390:1280;
  const state=await setup(page,{locale,width});await settings(page);
  await page.locator('#job-1-concurrency').fill('23');
  let writes=0;
  await page.route('**/api/settings',async route=>{
    if(route.request().method()!=='PUT')return route.fallback();
    writes++;await route.fulfill({status:200,headers:{ETag:'"config-99"'},contentType:'application/json',body});
  });
  await save(page);
  await expect(page.locator('#settingsSaveStatus')).toContainText(locale==='zh'?'保存结果无法确认，草稿已保留':'Save could not be confirmed. Your draft is preserved.');
  if(body==='{}'||width===390) {
    await page.locator('#settingsSaveStatus').scrollIntoViewIfNeeded();
    expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
    await page.screenshot({path:testInfo.outputPath('jobs-unconfirmed-save.png')});
  }
  await expect(page.locator('#job-1-concurrency')).toHaveValue('23');
  expect(writes).toBe(1);
  await navigate(page, 'jobs');await expect(page.locator('#confirmDialog')).toBeVisible();await page.keyboard.press('Escape');
  await page.unroute('**/api/settings');await save(page);
  await expect(page.locator('#settingsSaveStatus')).toContainText(locale==='zh'?'配置已保存':'Configuration saved');
  expect(state.saves).toHaveLength(1);
  expect(state.saves[0].etag).toBe('"config-1"');
});

for(const body of ['<html>gateway response</html>','[]','{}','{"jobs":[{"name":"broken"}]}'])test(`invalid benchmark status preserves displayed jobs and retries (${body})`,async({page})=>{
  const state=await setup(page);state.refreshing=true;await jobs(page);
  await page.locator('#jobAccessSession').fill('account-01');await page.locator('[data-acquire-job]').click();
  await expect(page.locator('[data-access-output]')).toBeVisible();
  const uri=await page.locator('#jobAccessURL').inputValue();
  await page.route('**/api/jobs',route=>route.fulfill({status:200,contentType:'application/json',body}));
  await expect(page.locator('[data-runtime-error]')).not.toHaveText('');
  await expect(page.locator('.job-status-card')).toHaveCount(2);
  await expect(page.locator('#jobAccessURL')).toHaveValue(uri);
  state.refreshing=false;await page.unroute('**/api/jobs');
  await expect(page.locator('[data-refresh-job="catalog"]')).toBeEnabled({timeout:7000});
  await expect(page.locator('[data-runtime-error]')).toHaveText('');
});

for(const body of ['<html>gateway response</html>','{}'])test(`invalid settings JSON cannot replace an already loaded Job configuration (${body})`,async({page})=>{
  const state=await setup(page);state.jobs[1].concurrency=17;await settings(page);
  await page.route('**/api/settings',route=>route.fulfill({status:200,contentType:'application/json',body}));
  await page.locator('#settingsTab').getByRole('button',{name:'重新载入设置',exact:true}).click();
  await expect(page.locator('#settingsSaveStatus')).toContainText('设置加载失败');
  await expect(page.locator('#job-1-concurrency')).toHaveValue('17');
  await save(page);expect(state.saves).toHaveLength(0);
  await page.unroute('**/api/settings');
  await page.locator('#settingsTab').getByRole('button',{name:'重新载入设置',exact:true}).click();
  await expect(page.locator('#settingsSaveStatus')).toHaveText('');
  await expect(page.locator('#job-1-concurrency')).toHaveValue('17');
});

test('saves every job field with Profile and Endpoint in one revision-checked update',async({page})=>{
  const state=await setup(page);await settings(page);
  await page.locator('[data-job-row]').first().locator('summary').first().click();
  await expect(page.locator('#job-0-target_url')).toBeHidden();
  await page.locator('#job-0-concurrency').fill('12');
  await page.locator('#job-0-timeout').fill('20s');
  await page.locator('#job-0-session_ttl').fill('2h');
  await page.locator('#job-0-max_sessions').fill('200');
  await page.locator('[data-job-row]').first().locator('.job-response-settings summary').click();
  await page.locator('#job-0-body_contains').fill('page\nready');
  await expect(page.locator('#settingsSaveStatus')).toContainText('未保存');
  await save(page);
  await expect(page.locator('#settingsSaveStatus')).toContainText('配置已保存');
  expect(state.saves).toHaveLength(1);
  expect(state.saves[0].etag).toBe('"config-1"');
  expect(state.saves[0].body.jobs[0]).toEqual({...baseJob,concurrency:12,timeout:'20s',session_ttl:'2h',max_sessions:200,body_contains:'page\nready'});
  expect(state.saves[0].body.jobs[1]).toEqual(autoJob);
  expect(state.saves[0].body.endpoints).toHaveLength(3);
  expect(state.saves[0].body.profiles[0].name).toBe('hk-fast');
  await save(page);expect(state.saves).toHaveLength(1);
});

test('supports both connection modes with optional auto selection and validates target and references',async({page})=>{
  const state=await setup(page);await settings(page);
  await page.locator('[data-job-row]').first().locator('summary').first().click();
  await page.locator('#job-0-selection').selectOption('auto');
  await expect(page.locator('#job-0-size')).toHaveValue('5');
  await save(page);
  await expect(page.locator('#job-0-target_url')).toHaveAttribute('aria-invalid','true');
  expect(state.saves).toHaveLength(0);
  await page.locator('#job-0-target_url').fill('https://example.com/login-check');
  await page.locator('#job-0-size').fill('3');
  await page.locator('#job-0-refresh_interval').fill('5m');
  await page.locator('#job-0-probe_batch_size').fill('6');
  await page.locator('#job-0-probe_concurrency').fill('2');
  await page.locator('#job-0-max_response_bytes').fill('10240');
  await page.locator('#job-0-endpoint').selectOption('catalog');
  await save(page);
  await expect(page.locator('#job-1-endpoint')).toHaveAttribute('aria-invalid','true');
  await page.locator('#job-0-endpoint').selectOption('hk-only');
  await page.locator('#job-1-selection').selectOption('manual');
  await expect(page.locator('#job-1-target_url')).toBeHidden();
  await save(page);await expect(page.locator('#settingsSaveStatus')).toContainText('配置已保存');
  expect(state.saves[0].body.jobs[0]).toMatchObject({mode:'pinned',selection:'auto',size:3,probe_batch_size:6,probe_concurrency:2,max_response_bytes:10240,refresh_interval:'5m'});
  expect(state.saves[0].body.jobs[1]).toMatchObject({mode:'pooled',selection:'manual',target_url:autoJob.target_url});
  await page.locator('#job-0-mode').selectOption('pooled');
  await expect(page.locator('#job-0-session_ttl')).toBeHidden();
});

test('switching to manual selection still validates retained benchmark limits',async({page})=>{
  const state=await setup(page);await settings(page);
  await page.locator('#job-1-probe_concurrency').fill('17');
  await page.locator('#job-1-selection').selectOption('manual');
  await expect(page.locator('#job-1-probe_concurrency')).toBeHidden();
  await save(page);
  await expect(page.locator('#job-1-probe_concurrency')).toHaveAttribute('aria-invalid','true');
  await expect(page.locator('#job-1-probe_concurrency')).toBeVisible();
  expect(state.saves).toHaveLength(0);
  await page.locator('#job-1-probe_concurrency').fill('4');await save(page);
  await expect.poll(()=>state.saves.length).toBe(1);
});

test('correcting a retained manual target does not require a positive node limit',async({page})=>{
  const state=await setup(page);await settings(page);
  await page.locator('#job-1-size').fill('0');
  await page.locator('#job-1-target_url').fill('invalid-target');
  await page.locator('#job-1-selection').selectOption('manual');await save(page);
  await expect(page.locator('#job-1-target_url')).toHaveAttribute('aria-invalid','true');
  await page.locator('#job-1-target_url').fill('https://example.com/catalog');await save(page);
  await expect.poll(()=>state.saves.length).toBe(1);
  expect(state.saves[0].body.jobs[1]).toMatchObject({selection:'manual',size:0});
});

for(const status of [500,412])test(`failed save ${status} preserves draft; reload and navigation require explicit discard`,async({page})=>{
  const state=await setup(page);state.saveStatus=status;await settings(page);
  await page.locator('#job-1-concurrency').fill('23');await save(page);
  await expect(page.locator('#settingsSaveStatus')).toContainText(status===412?'草稿已保留':'test save rejected');
  await expect(page.locator('#job-1-concurrency')).toHaveValue('23');
  await navigate(page, 'jobs');
  await expect(page.locator('#confirmDialog')).toBeVisible();
  await expect(page.locator('#confirmCancel')).toBeFocused();await page.keyboard.press('Escape');
  await expect(page.locator('#settingsTab')).toHaveClass(/active/);
  await expect(page.locator('#job-1-concurrency')).toHaveValue('23');
  await page.locator('#settingsTab').getByRole('button',{name:'重新载入设置',exact:true}).click();
  await page.locator('#confirmAccept').click();await expect(page.locator('#job-1-concurrency')).toHaveValue('8');
  expect(state.saves).toHaveLength(1);
});

test('pending save prevents duplicate submit and draft replacement',async({page})=>{
  const state=await setup(page);await settings(page);
  let finish;state.saveWait=new Promise(resolve=>{finish=resolve;});
  await page.locator('#job-1-timeout').fill('25s');await save(page);
  await expect.poll(()=>state.saves.length).toBe(1);
  await expect.poll(()=>page.locator('#settingsTab form').evaluate(el=>el.inert)).toBe(true);
  await navigate(page, 'jobs');await expect(page.locator('#settingsTab')).toHaveClass(/active/);
  finish();await expect(page.locator('#settingsSaveStatus')).toContainText('配置已保存');
  await expect.poll(()=>page.locator('#settingsTab form').evaluate(el=>el.inert)).toBe(false);
});

test('adding and deleting jobs is a draft operation with cancellable confirmation',async({page})=>{
  const state=await setup(page);await settings(page);
  await page.locator('[data-add-job]').click();await expect(page.locator('[data-job-row]')).toHaveCount(3);
  await expect(page.locator('#job-2-name')).toBeFocused();
  await page.locator('[data-remove-job="2"]').click();await page.keyboard.press('Escape');
  await expect(page.locator('[data-job-row]')).toHaveCount(3);
  await page.locator('[data-remove-job="2"]').click();await expect(page.locator('#confirmMessage')).toContainText('应用密码失效');
  await page.locator('#confirmAccept').click();await expect(page.locator('[data-job-row]')).toHaveCount(2);
  expect(state.saves).toHaveLength(0);
});

test('session table is read-only, paginated, filtered and explicitly releases removed jobs',async({page})=>{
  const state=await setup(page);await jobs(page);
  expect(state.calls.some(c=>c.endsWith('/acquire'))).toBe(false);
  await expect(page.locator('[data-release-job]')).toHaveCount(20);
  await page.locator('[data-session-next]').click();await expect(page.locator('[data-release-job]')).toHaveCount(3);
  await page.locator('#jobSessionState').selectOption('removed');await expect(page.locator('[data-release-job]')).toHaveCount(1);
  await expect(page.locator('[data-session-list]')).toContainText('deleted-job');
  await page.locator('[data-release-job]').click();await page.keyboard.press('Escape');
  expect(state.calls.some(c=>c.endsWith('/release'))).toBe(false);
  await page.locator('[data-release-job]').click();await expect(page.locator('#confirmMessage')).toContainText('关闭旧连接');
  await page.locator('#confirmAccept').click();await expect(page.locator('[data-session-list]')).toContainText('没有匹配');
  expect(state.calls.filter(c=>c.endsWith('/release'))).toEqual(['POST /api/jobs/deleted-job/release']);
  await page.locator('#jobSessionState').selectOption('');await page.locator('#jobSessionSearch').fill('account-02');
  await expect(page.locator('[data-release-job]')).toHaveCount(1);
  await expect(page.locator('[data-session-list]')).toContainText('account-02');
});

test('access credentials stay masked, support SOCKS5, and stale acquisitions do not replace a new session',async({page,context})=>{
  const state=await setup(page);await jobs(page);
  await context.grantPermissions(['clipboard-read','clipboard-write']);
  await page.locator('#jobAccessSession').fill('account-a');await page.locator('[data-acquire-job]').click();
  await expect(page.locator('#jobAccessURL')).toHaveValue('http://hk-session-account-a:hk-secret@127.0.0.1:23231');
  await expect(page.locator('#jobAccessURL')).toHaveAttribute('type','password');
  await page.locator('[data-reveal-job-url]').click();await expect(page.locator('#jobAccessURL')).toHaveAttribute('type','text');
  await page.locator('#jobAccessProtocol').selectOption('socks5h');await page.locator('[data-copy-job-url]').click();
  expect(await page.evaluate(()=>navigator.clipboard.readText())).toBe('socks5h://hk-session-account-a:hk-secret@127.0.0.1:23231');
  let finish;state.accessWait=new Promise(resolve=>{finish=resolve;});
  await page.locator('#jobAccessSession').fill('old-id');await page.locator('[data-acquire-job]').click();
  await expect(page.locator('[data-acquire-job]')).toBeDisabled();
  await page.locator('#jobAccessSession').fill('new-id');finish();
  await expect(page.locator('[data-acquire-job]')).toBeEnabled();await expect(page.locator('[data-access-output]')).toBeHidden();
  await navigate(page, 'dashboard');await expect(page.locator('#jobAccessURL')).toHaveValue('');
});

test('only auto jobs expose benchmarking and the old assistant does not emit invalid pinned URLs',async({page})=>{
  const state=await setup(page);await jobs(page);
  await expect(page.locator('[data-refresh-job]')).toHaveCount(1);
  await page.locator('[data-refresh-job="catalog"]').click();await expect.poll(()=>state.calls.filter(c=>c==='POST /api/jobs/catalog/refresh').length).toBe(1);
  await settings(page);await page.locator('[data-access-endpoint]').selectOption('hk-only');
  await expect(page.locator('[data-access-uri]')).toHaveValue('');
  await expect(page.locator('[data-access-help]')).toContainText('pinned');await expect(page.locator('[data-copy="uri"]')).toBeDisabled();
});

test('viewer cannot open admin job controls',async({page})=>{
  const state=await setup(page,{role:'viewer'});
  await expect(page.locator('[data-tab="jobs"]')).toBeHidden();
  await page.evaluate(()=>window.switchTab('jobs'));
  await expect(page.locator('#jobsTab')).not.toHaveClass(/active/);
  expect(state.calls.some(c=>c==='GET /api/jobs')).toBe(false);
});

test('runtime failures can be retried and paused sessions never show a proxy URL',async({page})=>{
  await setup(page);
  await page.route('**/api/jobs',route=>route.fulfill({status:503,json:{error:'runtime offline'}}));
  await navigate(page, 'jobs');
  await expect(page.locator('[data-runtime-error]')).toHaveText('runtime offline');
  await page.unroute('**/api/jobs');await page.locator('[data-reload-jobs]').click();
  await expect(page.locator('.job-status-card')).toHaveCount(2);
  await expect(page.locator('[data-runtime-error]')).toBeEmpty();
  await page.route('**/api/jobs/accounts/acquire',route=>route.fulfill({json:{job:'accounts',mode:'pinned',session:'paused-id',state:'paused',node:'node-1',expires_at:'2027-01-01T12:00:00Z',concurrency:8,timeout:'15s'}}));
  await page.locator('#jobAccessSession').fill('paused-id');await page.locator('[data-acquire-job]').click();
  await expect(page.locator('[data-access-result]')).toContainText('已暂停');
  await expect(page.locator('[data-access-output]')).toBeHidden();
  await expect(page.locator('[data-access-error]')).toContainText('停止客户端');
});

test('empty jobs show configuration action without inventing nodes or acquiring a session',async({page})=>{
  const state=await setup(page);state.jobs=[];state.sessions=[];
  await navigate(page, 'jobs');
  await expect(page.locator('[data-job-status-list]')).toContainText('尚未配置应用');
  await expect(page.locator('[data-acquire-job]')).toBeDisabled();
  await expect(page.locator('[data-session-list]')).toContainText('没有匹配的会话');
  expect(state.calls.some(c=>c.endsWith('/acquire'))).toBe(false);
});

test('deleting an Endpoint updates references and blocks saving its existing job',async({page})=>{
  const state=await setup(page);await settings(page);
  await page.locator('[data-remove-endpoint="2"]').click();await page.locator('#confirmAccept').click();
  await expect(page.locator('#job-1-endpoint option:checked')).toContainText('不存在');
  await expect(page.locator('#settingsSaveStatus')).toContainText('未保存');
  await save(page);await expect(page.locator('#job-1-endpoint')).toHaveAttribute('aria-invalid','true');
  expect(state.saves).toHaveLength(0);
});

test('saving jobs preserves legacy Profile name regex alongside MUST rules',async({page})=>{
  const state=await setup(page);state.profileRegex='^HK';await settings(page);
  await page.locator('#job-1-concurrency').fill('10');await save(page);
  await expect.poll(()=>state.saves.length).toBe(1);
  expect(state.saves[0].body.profiles[0]).toMatchObject({name_regex:'^HK',tag_rules:{must:['Premium']}});
});

test('renaming a Profile preserves Endpoint bindings and blocks saving missing references',async({page})=>{
  const state=await setup(page);state.jobs=[];
  await navigate(page, 'settings');
  await expect(page.locator('#profile-0-name')).toHaveValue('hk-fast');
  await expect.poll(()=>page.locator('#settingsTab form').evaluate(el=>el.inert)).toBe(false);
  await page.locator('#profile-0-name').fill('hk-renamed');
  await expect(page.locator('#endpoint-1-profile')).toHaveValue('hk-fast');
  await expect(page.locator('#endpoint-1-profile option:checked')).toContainText('不存在');
  await save(page);
  await expect(page.locator('.toast').last()).toContainText('不存在的 Profile');
  expect(state.saves).toHaveLength(0);
  await page.locator('#profile-0-name').fill('hk-fast');
  await expect(page.locator('#endpoint-1-profile option:checked')).toHaveText('hk-fast');
  await page.locator('#profile-0-name').fill('hk-renamed');
  await page.locator('#endpoint-1-profile').selectOption('hk-renamed');
  await page.locator('#endpoint-2-profile').selectOption('hk-renamed');
  await save(page);await expect.poll(()=>state.saves.length).toBe(1);
  expect(state.saves[0].body.endpoints[1].profile).toBe('hk-renamed');
  expect(state.saves[0].body.endpoints[2].profile).toBe('hk-renamed');
});

test('subscription-only drafts are protected by reload and navigation guards',async({page})=>{
  await setup(page);await settings(page);
  const url=page.locator('#subscriptionSourcesMount [data-field="url"]');
  await url.fill('https://example.com/changed');
  await navigate(page, 'jobs');
  await expect(page.locator('#confirmDialog')).toBeVisible();await page.keyboard.press('Escape');
  await expect(page.locator('#settingsTab')).toHaveClass(/active/);
  await page.locator('#settingsTab').getByRole('button',{name:'重新载入设置',exact:true}).click();
  await expect(page.locator('#confirmDialog')).toBeVisible();await page.keyboard.press('Escape');
  await expect(url).toHaveValue('https://example.com/changed');
});

test('subscription revision conflicts preserve the draft',async({page})=>{
  await setup(page);await settings(page);
  await page.route('**/api/subscription/preview',route=>route.fulfill({status:412,json:{error:'revision conflict'}}));
  const url=page.locator('#subscriptionSourcesMount [data-field="url"]');
  await url.fill('https://example.com/changed');await save(page);
  await expect(page.locator('#settingsTab button[type="submit"]')).toBeEnabled();
  await expect(url).toHaveValue('https://example.com/changed');
  await expect(page.locator('#settingsSaveStatus')).toContainText('草稿已保留');
});

for(const statusFailure of [false,true])test(`saving subscriptions advances the next Job save revision (status failure: ${statusFailure})`,async({page})=>{
  const state=await setup(page);await settings(page);
  await page.route('**/api/subscription/config',async route=>{
    if(route.request().method()!=='PUT')return route.fallback();
    await route.fulfill({headers:{ETag:'"config-2"'},json:route.request().postDataJSON()});
  });
  if(statusFailure)await page.route('**/api/subscription/status',route=>route.fulfill({status:503,json:{error:'status offline'}}));
  await page.locator('#subscriptionSourcesMount [data-field="url"]').fill('https://example.com/changed');
  await save(page);await page.locator('#confirmAccept').click();
  await expect(page.locator('#settingsTab button[type="submit"]')).toBeEnabled();
  await expect(page.locator('#subscriptionSourcesMount [data-field="url"]')).toHaveValue('https://example.com/changed');
  if(statusFailure)await expect(page.locator('#settingsSaveStatus')).toContainText('订阅已保存');
  await page.locator('#job-1-concurrency').fill('11');await save(page);
  await expect.poll(()=>state.saves.length).toBe(1);
  expect(state.saves[0].etag).toBe('"config-2"');
  expect(state.calls.filter(c=>c==='POST /api/subscription/preview')).toHaveLength(1);
});

test('refreshing after job removal clears the previously generated proxy address',async({page})=>{
  const state=await setup(page);await jobs(page);
  await page.locator('#jobAccessSession').fill('account-a');await page.locator('[data-acquire-job]').click();
  await expect(page.locator('[data-access-output]')).toBeVisible();
  state.jobs=[autoJob];await page.locator('[data-reload-jobs]').click();
  await expect(page.locator('#jobAccessName')).toHaveValue('catalog');
  await expect(page.locator('[data-access-output]')).toBeHidden();
  await expect(page.locator('#jobAccessURL')).toHaveValue('');
});

test('release waits for pending acquisition and cannot leave a recreated session',async({page})=>{
  const state=await setup(page);state.persistAcquire=true;await jobs(page);
  let finish;state.accessWait=new Promise(resolve=>{finish=resolve;});
  await page.locator('#jobAccessSession').fill('account-01');await page.locator('[data-acquire-job]').click();
  await expect.poll(()=>state.calls.includes('POST /api/jobs/accounts/acquire')).toBe(true);
  await page.locator('[data-release-session="account-01"]').click();await page.locator('#confirmAccept').click();
  try {
    await page.waitForTimeout(100);
    expect(state.calls.includes('POST /api/jobs/accounts/release')).toBe(false);
  } finally {finish();}
  await expect(page.locator('[data-release-session="account-01"]')).toHaveCount(0);
  await expect(page.locator('[data-acquire-job]')).toBeEnabled();
  expect(state.sessions.some(s=>s.session==='account-01')).toBe(false);
  await expect(page.locator('[data-access-output]')).toBeHidden();
});

test('pending release blocks acquiring a session until the release completes',async({page})=>{
  const state=await setup(page);await jobs(page);
  let finish;state.releaseWait=new Promise(resolve=>{finish=resolve;});
  await page.locator('#jobAccessSession').fill('account-01');
  await page.locator('[data-release-session="account-01"]').click();await page.locator('#confirmAccept').click();
  await expect.poll(()=>state.calls.includes('POST /api/jobs/accounts/release')).toBe(true);
  try {
    await expect(page.locator('[data-acquire-job]')).toBeDisabled();
    await page.locator('[data-job-access-form]').dispatchEvent('submit');
    await page.waitForTimeout(100);
    expect(state.calls.includes('POST /api/jobs/accounts/acquire')).toBe(false);
  } finally {finish();}
  await expect(page.locator('[data-acquire-job]')).toBeEnabled();
  await page.locator('[data-acquire-job]').click();
  await expect.poll(()=>state.calls.includes('POST /api/jobs/accounts/acquire')).toBe(true);
});

test('failed pending acquisition stops the queued release and allows an explicit retry',async({page})=>{
  const state=await setup(page);await jobs(page);
  let finish;state.accessWait=new Promise(resolve=>{finish=resolve;});state.accessStatus=503;
  await page.locator('#jobAccessSession').fill('account-01');await page.locator('[data-acquire-job]').click();
  await expect.poll(()=>state.calls.includes('POST /api/jobs/accounts/acquire')).toBe(true);
  await page.locator('[data-release-session="account-01"]').click();await page.locator('#confirmAccept').click();
  finish();
  await expect(page.locator('[data-session-error]')).toHaveText('test acquire rejected');
  expect(state.calls.includes('POST /api/jobs/accounts/release')).toBe(false);
  await expect(page.locator('[data-release-session="account-01"]')).toBeEnabled();
  await page.locator('[data-release-session="account-01"]').click();await page.locator('#confirmAccept').click();
  await expect(page.locator('[data-release-session="account-01"]')).toHaveCount(0);
});

test('running benchmark status updates without erasing access output and polling stops on navigation',async({page})=>{
  const state=await setup(page);state.refreshing=true;await jobs(page);
  const refresh=page.locator('[data-refresh-job="catalog"]');
  await expect(refresh).toBeDisabled();
  await page.locator('.job-status-card').last().locator('summary').click();
  await page.locator('#jobAccessSession').fill('account-01');await page.locator('[data-acquire-job]').click();
  await expect(page.locator('[data-access-output]')).toBeVisible();
  const uri=await page.locator('#jobAccessURL').inputValue();
  await page.locator('[data-status-job="catalog"] summary').focus();
  state.refreshing=false;
  await expect(refresh).toBeEnabled();
  await expect(page.locator('#jobAccessURL')).toHaveValue(uri);
  await expect(page.locator('.job-status-card').last().locator('details')).toHaveAttribute('open','');
  await expect(page.locator('[data-status-job="catalog"] summary')).toBeFocused();
  state.refreshing=true;await page.locator('[data-reload-jobs]').click();await expect(refresh).toBeDisabled();
  await navigate(page, 'dashboard');
  const reads=state.calls.filter(c=>c==='GET /api/jobs').length;
  await page.waitForTimeout(2200);
  expect(state.calls.filter(c=>c==='GET /api/jobs')).toHaveLength(reads);
});

test('status polling reconciles removed jobs before exposing another access action',async({page})=>{
  const state=await setup(page);state.refreshing=true;await jobs(page);
  await page.locator('#jobAccessSession').fill('account-01');await page.locator('[data-acquire-job]').click();
  await expect(page.locator('[data-access-output]')).toBeVisible();
  state.jobs=[autoJob];state.refreshing=false;
  await expect(page.locator('#jobAccessName')).toHaveValue('catalog');
  await expect(page.locator('#jobAccessSession')).toBeDisabled();
  await expect(page.locator('[data-access-output]')).toBeHidden();
  await expect(page.locator('#jobAccessURL')).toHaveValue('');
});

for(const failure of ['503','network'])test(`benchmark polling recovers from ${failure} without clearing the acquired endpoint`,async({page},testInfo)=>{
  const locale=failure==='network'?'zh':'en',width=failure==='network'?390:1280;
  const state=await setup(page,{locale,width});state.refreshing=true;await jobs(page);
  await page.locator('#jobAccessSession').fill('account-01');await page.locator('[data-acquire-job]').click();
  await expect(page.locator('[data-access-output]')).toBeVisible();
  const uri=await page.locator('#jobAccessURL').inputValue();
  let failures=0;
  await page.route('**/api/jobs',async route=>{
    if(failures++===0) {
      if(failure==='network')await route.abort();
      else await route.fulfill({status:503,json:{error:'temporary status failure'}});
    }
    else await route.fallback();
  });
  await expect(page.locator('[data-runtime-error]')).not.toHaveText('');
  await expect(page.locator('[data-jobs-updated]')).toContainText(locale==='zh'?'正在重试':'Retrying');
  await page.locator('[data-jobs-updated]').scrollIntoViewIfNeeded();
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.screenshot({path:testInfo.outputPath('jobs-status-retrying.png')});
  state.refreshing=false;
  await expect(page.locator('[data-refresh-job="catalog"]')).toBeEnabled({timeout:7000});
  await expect(page.locator('[data-runtime-error]')).toHaveText('');
  await expect(page.locator('#jobAccessURL')).toHaveValue(uri);
  expect(state.calls.some(c=>c==='POST /api/jobs/catalog/refresh')).toBe(false);
});

test('benchmark status retries back off to 30 seconds and stop after navigation',async({page})=>{
  await page.clock.install({time:new Date('2026-01-01T00:00:00Z')});
  const state=await setup(page);state.refreshing=true;
  await page.clock.pauseAt(new Date('2026-01-01T00:01:00Z'));await jobs(page);
  let reads=0;
  await page.route('**/api/jobs',route=>route.fulfill({status:503,json:{error:`status outage ${++reads}`}}));
  for(const delay of [2000,2000,4000,8000,16000,30000,30000]) {
    const before=reads;
    await page.clock.runFor(delay-1);expect(reads).toBe(before);
    await page.clock.runFor(1);
    await expect(page.locator('[data-runtime-error]')).toHaveText(`status outage ${before+1}`);
    expect(reads).toBe(before+1);
  }
  await navigate(page, 'dashboard');
  await page.clock.runFor(60000);expect(reads).toBe(7);
});

for(const status of [401,403])test(`benchmark polling stops on authorization error ${status}`,async({page})=>{
  await page.clock.install({time:new Date('2026-01-01T00:00:00Z')});
  const state=await setup(page);state.refreshing=true;
  await page.clock.pauseAt(new Date('2026-01-01T00:01:00Z'));await jobs(page);
  let reads=0;
  await page.route('**/api/jobs',route=>{reads++;return route.fulfill({status,json:{error:'job status access denied'}});});
  await page.clock.runFor(2000);
  await expect(page.locator('[data-runtime-error]')).toHaveText('job status access denied');
  if(status===401)await expect(page.locator('#loginOverlay')).toBeVisible();
  await page.clock.runFor(60000);expect(reads).toBe(1);
});

test('auto job named access does not collide with the access helper pending state',async({page})=>{
  const state=await setup(page);state.jobs=[{...autoJob,name:'access'}];
  await navigate(page, 'jobs');await expect(page.locator('.job-status-card')).toHaveCount(1);
  await page.locator('[data-refresh-job="access"]').click();
  await expect(page.locator('[data-acquire-job]')).toBeEnabled();
  await page.locator('[data-acquire-job]').click();
  await expect.poll(()=>state.calls.includes('POST /api/jobs/access/acquire')).toBe(true);
});

for(const [locale,width] of [['zh',390],['en',1280]])test(`jobs render and navigate in ${locale} at ${width}px`,async({page},testInfo)=>{
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await setup(page,{locale,width});await jobs(page);
  await expect(page.locator('#jobsTab h1')).toHaveText(locale==='en'?'Applications':'应用代理');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.locator('.job-status-card summary').first().click();
  await page.screenshot({path:testInfo.outputPath('jobs-runtime.png'),fullPage:true});
  await page.locator('#jobSessionState').selectOption('removed');
  await page.locator('.job-sessions').scrollIntoViewIfNeeded();
  await page.screenshot({path:testInfo.outputPath('jobs-sessions.png')});
  await page.locator('[data-configure-jobs]').click();await expect(page.locator('[data-add-job]')).toBeFocused();
  await expect(page.locator('.jobs-editor .panel-title')).toHaveText(locale==='en'?'Application configuration':'应用配置');
  await page.locator('[data-job-row]').last().locator('summary').first().focus();await page.keyboard.press('Space');await page.keyboard.press('Space');
  await page.locator('#job-1-name').scrollIntoViewIfNeeded();
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.screenshot({path:testInfo.outputPath('jobs-settings.png')});
  await page.locator('#job-1-max_response_bytes').scrollIntoViewIfNeeded();
  await page.screenshot({path:testInfo.outputPath('jobs-benchmark-settings.png')});
  expect(errors).toEqual([]);
});


test('new applications default to permanent bindings and save per-node capacity',async({page})=>{
  const state=await setup(page);await settings(page);
  await page.locator('[data-add-job]').click();
  await expect(page.locator('#job-2-session_ttl')).toHaveValue('0');
  await page.locator('[data-remove-job="2"]').click();await page.locator('#confirmAccept').click();
  await page.locator('[data-job-row]').first().locator('summary').first().click();
  await page.locator('#job-0-max_accounts_per_node').fill('1');
  await save(page);await expect.poll(()=>state.saves.length).toBe(1);
  expect(state.saves[0].body.jobs[0]).toMatchObject({session_ttl:'0',max_accounts_per_node:1});
});

for(const lifetime of ['0','0s','168h'])test(`binding lifetime ${lifetime} is accepted`,async({page})=>{
  const state=await setup(page);await settings(page);
  await page.locator('[data-job-row]').first().locator('summary').first().click();
  await page.locator('#job-0-session_ttl').fill(lifetime);await page.locator('#job-0-max_accounts_per_node').fill('1');await save(page);
  await expect.poll(()=>state.saves.length).toBe(1);
});

for(const [locale,width] of [['zh',390],['en',1280]])test(`manual account migration is scoped, confirmed and responsive in ${locale}`,async({page},testInfo)=>{
  const state=await setup(page,{locale,width});await jobs(page);
  await expect(page.locator('[data-session-list]')).toContainText(locale==='zh'?'永久绑定':'Permanent binding');
  await page.locator('[data-migrate-session="account-01"]').click();
  await expect(page.locator('[data-binding-editor]')).toBeVisible();
  await expect(page.locator('#bindingTarget')).toBeFocused();
  await expect(page.locator('#bindingTarget option[value="node-2"]')).toBeDisabled();
  await expect(page.locator('#bindingTarget option[value="node-1"]')).toBeEnabled();
  await expect(page.locator('[data-acquire-job]')).toBeDisabled();
  await page.locator('#bindingTarget').selectOption('node-3');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.screenshot({path:testInfo.outputPath('account-migration.png')});
  await page.locator('[data-binding-save]').click();await expect(page.locator('#confirmDialog')).toBeVisible();
  await expect(page.locator('#confirmCancel')).toBeFocused();await page.keyboard.press('Escape');expect(state.migrations).toHaveLength(0);
  await page.locator('[data-binding-save]').click();await page.locator('#confirmAccept').click();
  await expect(page.locator('[data-binding-editor]')).toBeHidden();
  expect(state.migrations).toEqual([{app:'accounts',session:'account-01',node:'node-3',expected_revision:'binding-0'}]);
  expect(state.sessions[0]).toMatchObject({node:'node-3',permanent:true});
  expect(state.calls.filter(c=>c.endsWith('/release'))).toEqual([]);
});

test('same-node update converts an expired binding without changing its account ID',async({page})=>{
  const state=await setup(page);state.sessions[0].permanent=false;state.sessions[0].state='expired';await jobs(page);
  await page.locator('[data-migrate-session="account-01"]').click();
  await expect(page.locator('#bindingTarget')).toHaveValue('node-1');
  await page.locator('[data-binding-save]').click();await page.locator('#confirmAccept').click();
  await expect(page.locator('[data-binding-editor]')).toBeHidden();
  expect(state.sessions[0]).toMatchObject({session:'account-01',node:'node-1',permanent:true,state:'ready'});
});

for(const status of [409,503,500])test(`failed migration ${status} preserves target choice and original binding`,async({page})=>{
  const state=await setup(page);state.migrationStatus=status;await jobs(page);
  await page.locator('[data-migrate-session="account-01"]').click();await page.locator('#bindingTarget').selectOption('node-3');
  await page.locator('[data-binding-save]').click();await page.locator('#confirmAccept').click();
  await expect(page.locator('[data-binding-error]')).toContainText('migration rejected');
  await expect(page.locator('#bindingTarget')).toHaveValue('node-3');
  expect(state.sessions[0].node).toBe('node-1');expect(state.migrations).toHaveLength(1);
  await page.locator('[data-binding-cancel]').click();await expect(page.locator('[data-migrate-session="account-01"]')).toBeFocused();
});

test('no available migration targets does not remove the original binding',async({page})=>{
  const state=await setup(page);state.targets=[{node:'node-2',name:'full',accounts:1,limit:1,available:false}];await jobs(page);
  await page.locator('[data-migrate-session="account-01"]').click();await expect(page.locator('[data-binding-save]')).toBeDisabled();
  await expect(page.locator('[data-binding-state]')).toContainText('没有可用名额');expect(state.sessions[0].node).toBe('node-1');
  state.targets=[];await page.locator('[data-binding-reload]').click();await expect(page.locator('#bindingTarget option')).toHaveCount(0);
  await expect(page.locator('[data-binding-save]')).toBeDisabled();expect(state.migrations).toHaveLength(0);
});

test('pending migration prevents duplicate writes and competing binding operations',async({page})=>{
  const state=await setup(page);let finish;state.migrationWait=new Promise(r=>{finish=r;});await jobs(page);
  await page.locator('[data-migrate-session="account-01"]').click();await page.locator('#bindingTarget').selectOption('node-3');
  await page.locator('[data-binding-save]').click();await page.locator('#confirmAccept').click();await expect.poll(()=>state.migrations.length).toBe(1);
  await expect(page.locator('[data-binding-save]')).toBeDisabled();await expect(page.locator('[data-binding-cancel]')).toBeDisabled();
  await expect(page.locator('[data-release-session="account-01"]')).toBeDisabled();await expect(page.locator('[data-acquire-job]')).toBeDisabled();
  finish();await expect(page.locator('[data-binding-editor]')).toBeHidden();expect(state.migrations).toHaveLength(1);
});

test('stale migration revision cannot overwrite another operator binding',async({page})=>{
  const state=await setup(page);await jobs(page);await page.locator('[data-migrate-session="account-01"]').click();
  await page.locator('#bindingTarget').selectOption('node-3');state.sessions[0].revision='another-operator';
  await page.locator('[data-binding-save]').click();await page.locator('#confirmAccept').click();
  await expect(page.locator('[data-binding-error]')).toContainText('binding changed');expect(state.sessions[0].node).toBe('node-1');
});

test('invalid binding list response preserves displayed records and migration identity',async({page})=>{
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  const state=await setup(page);await jobs(page);
  await page.route('**/api/jobs/sessions?*',route=>route.fulfill({status:200,contentType:'application/json',body:'{"sessions":{},"pagination":{"page":1,"total_pages":1,"total_items":1}}'}));
  await page.locator('[data-reload-sessions]').click();
  await expect(page.locator('[data-session-error]')).not.toHaveText('');
  await expect(page.locator('[data-migrate-job]')).toHaveCount(20);
  await page.locator('[data-migrate-session="account-01"]').click();await expect(page.locator('#bindingTarget')).toHaveValue('node-1');
  await page.locator('[data-binding-cancel]').click();
  expect(state.migrations).toHaveLength(0);expect(errors).toEqual([]);
});

for (const [locale,width] of [['zh',1280],['en',390]]) test(`shared-port application defaults, secret controls and advanced access ${locale}`,async({page},testInfo)=>{
  const state=await setup(page,{locale,width});await settings(page);
  await page.locator('[data-add-job]').click();
  await expect(page.locator('#job-2-ingress')).toHaveValue('shared');
  await expect(page.locator('#job-2-endpoint')).toBeHidden();
  await expect(page.locator('#job-2-session_ttl')).toHaveValue('0');
  const row=page.locator('[data-job-row]').nth(2),key=page.locator('#job-2-password');
  const original=await key.inputValue();expect(original).toMatch(/^[0-9a-f]{48}$/);
  await expect(key).toHaveAttribute('type','password');
  await expect(row.locator('[data-shared-endpoint]')).toContainText('23230');
  await row.locator('[data-reveal-app-password]').click();await expect(key).toHaveAttribute('type','text');
  await row.locator('[data-reveal-app-password]').click();await expect(key).toHaveAttribute('type','password');
  await row.locator('[data-generate-app-password]').click();await expect(page.locator('#confirmDialog')).toBeVisible();
  await expect(page.locator('#confirmCancel')).toBeFocused();await page.keyboard.press('Escape');await expect(key).toHaveValue(original);
  await row.locator('[data-generate-app-password]').click();await page.locator('#confirmAccept').click();
  await expect(key).not.toHaveValue(original);await expect(key).toHaveAttribute('type','password');
  const generated=await key.inputValue();expect(state.saves).toHaveLength(0);
  await page.locator('#job-2-ingress').selectOption('endpoint');await expect(key).toBeHidden();
  await expect(page.locator('#job-2-endpoint')).toBeVisible();
  await page.locator('#job-2-endpoint').selectOption('hk-only');
  await page.locator('#job-2-ingress').selectOption('shared');await expect(page.locator('#job-2-endpoint')).toHaveValue('');
  await expect(key).toHaveValue(generated);
  await key.scrollIntoViewIfNeeded();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  if(locale==='en')expect(await row.innerText()).not.toMatch(/[\u3400-\u9fff]/);
  await page.screenshot({path:testInfo.outputPath(`shared-application-${locale}.png`)});
  await save(page);await expect.poll(()=>state.saves.length).toBe(1);
  expect(state.saves[0].body.jobs[2]).toMatchObject({ingress:'shared',endpoint:'',password:generated,session_ttl:'0'});
  expect(state.saves[0].body.endpoints).toHaveLength(3); // No new listening port.
});

test('shared credentials validate uniqueness, preserve bytes and failed rotation draft',async({page})=>{
  const state=await setup(page);await settings(page);
  await page.locator('#job-1-ingress').selectOption('shared');
  const key=page.locator('#job-1-password');
  await key.fill('short');await save(page);await expect(key).toHaveAttribute('aria-invalid','true');
  await page.locator('[data-endpoint-row]').first().locator('[data-field="password"]').fill('ordinary-pool-secret');
  await key.fill('ordinary-pool-secret');await save(page);await expect(key).toHaveAttribute('aria-invalid','true');
  await key.fill(' application-secret-keep-spaces ');await save(page);await expect.poll(()=>state.saves.length).toBe(1);
  expect(state.saves[0].body.jobs[1].password).toBe(' application-secret-keep-spaces ');
  await page.locator('[data-add-job]').click();
  await page.locator('#job-2-password').fill(' application-secret-keep-spaces ');await save(page);
  await expect(page.locator('#job-2-password')).toHaveAttribute('aria-invalid','true');expect(state.saves).toHaveLength(1);
  await page.locator('#job-2-password').fill('different-application-secret');
  state.saveStatus=500;await save(page);await expect(page.locator('#settingsSaveStatus')).toContainText('test save rejected');
  await expect(page.locator('#job-2-password')).toHaveValue('different-application-secret');
  state.saveStatus=200;await save(page);await expect(page.locator('#settingsSaveStatus')).toContainText('配置已保存');
});

test('shared access refuses anonymous or filtered primary and legacy configurations keep access mode',async({page})=>{
  const state=await setup(page);delete state.jobs[0].ingress;delete state.jobs[0].password;await settings(page);
  await expect(page.locator('#job-0-ingress')).toHaveValue('endpoint');
  await page.locator('#job-1-ingress').selectOption('shared');
  const primary=page.locator('[data-endpoint-row]').first();
  await primary.locator('[data-field="username"]').fill('');await primary.locator('[data-field="password"]').fill('');
  await save(page);await expect(page.locator('#job-1-ingress')).toHaveAttribute('aria-invalid','true');expect(state.saves).toHaveLength(0);
  await primary.locator('[data-field="username"]').fill('fleet');await primary.locator('[data-field="password"]').fill('pool-secret');
  await primary.locator('[data-field="profile"]').selectOption('hk-fast');
  await save(page);await expect(page.locator('#job-1-ingress')).toHaveAttribute('aria-invalid','true');expect(state.saves).toHaveLength(0);
  await primary.locator('[data-field="profile"]').selectOption('');await save(page);await expect.poll(()=>state.saves.length).toBe(1);
});

test('shared account helper rejects malformed IDs and displays masked server-issued URL',async({page})=>{
  const state=await setup(page);state.jobs[0]={...baseJob,ingress:'shared',endpoint:'',password:'application-accounts-secret'};
  await page.route('**/api/jobs/accounts/acquire',route=>route.fulfill({json:{job:'accounts',mode:'pinned',state:'ready',session:route.request().postDataJSON().session,node:'node-1',permanent:true,revision:'binding',proxy_url:'http://account-01:application-accounts-secret@127.0.0.1:23230',concurrency:8,timeout:'15s'}}));
  await jobs(page);await expect(page.locator('.job-status-card').first()).toContainText('统一端口');
  await page.locator('#jobAccessSession').fill('account@another-profile');await page.locator('[data-acquire-job]').click();
  await expect(page.locator('[data-access-error]')).toContainText('账号 ID');
  await page.locator('#jobAccessSession').fill('account-01');await page.locator('[data-acquire-job]').click();
  await expect(page.locator('#jobAccessURL')).toHaveValue('http://account-01:application-accounts-secret@127.0.0.1:23230');
  await expect(page.locator('#jobAccessURL')).toHaveAttribute('type','password');
});

for(const width of [390,1280])test(`idle retention saves and exposes connection protection ${width}`,async({page},testInfo)=>{
 const locale=width===390?'zh':'en';const state=await setup(page,{width,locale});await settings(page);
 await page.locator('[data-job-row]').first().locator('summary').first().click();
 const idle=page.locator('#job-0-session_idle_timeout');await expect(idle).toHaveValue('0');
 await idle.fill('30s');await save(page);await expect(page.locator('#job-0-session_idle_timeout-error')).not.toHaveText('');expect(state.saves).toHaveLength(0);
 await idle.fill('24h');await save(page);await expect(page.locator('#settingsSaveStatus')).toContainText(locale==='en'?'Configuration saved':'配置已保存');expect(state.saves[0].body.jobs[0].session_idle_timeout).toBe('24h');
 await idle.evaluate(el=>el.scrollIntoView({block:'center'}));await page.screenshot({path:testInfo.outputPath('idle-settings.png')});
 Object.assign(state.sessions[0],{active_connections:2,last_used_at:'2026-10-21T02:00:00Z'});
 Object.assign(state.sessions[1],{active_connections:0,last_used_at:'2026-10-20T02:00:00Z',reclaim_at:'2026-10-21T02:00:00Z'});
 await jobs(page);await expect(page.locator('[data-session-list]')).toContainText(locale==='en'?'In use; cleanup suspended':'连接使用中，暂停回收');
 await expect(page.locator('[data-session-list]')).toContainText(locale==='en'?'Reclaim after':'预计回收');
 await page.locator('[data-session-list]').scrollIntoViewIfNeeded();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.screenshot({path:testInfo.outputPath('idle-bindings.png')});
});


test('switching an idle-cleanup app to pooled clears the inactive policy',async({page})=>{
 const state=await setup(page);await settings(page);
 await page.locator('[data-job-row]').first().locator('summary').first().click();
 await page.locator('#job-0-session_idle_timeout').fill('24h');
 await page.locator('#job-0-mode').selectOption('pooled');await save(page);
 await expect(page.locator('#settingsSaveStatus')).toContainText('配置已保存');
 expect(state.saves[0].body.jobs[0].session_idle_timeout).toBe('0');
});

for(const [locale,width] of [['zh',390],['en',1280]])test(`application failover setting and audit are scoped and accessible in ${locale}`,async({page},testInfo)=>{
 const state=await setup(page,{locale,width});await settings(page);
 await page.locator('[data-job-row]').first().locator('summary').first().click();
 const policy=page.locator('#job-0-failure_policy');await expect(policy).toHaveValue('strict');
 await policy.focus();await expect(policy).toBeFocused();await policy.selectOption('failover');await expect(policy).toHaveValue('failover');
 await expect(page.locator('[data-job-row]').first().locator('[data-failover-help]')).toContainText('Cloudflare');
 await page.locator('[data-pinned-fields]').first().evaluate(el=>el.scrollIntoView({block:'center'}));
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.screenshot({path:testInfo.outputPath('failover-settings.png')});
 await save(page);await expect.poll(()=>state.saves.length).toBe(1);
 expect(state.saves[0].body.jobs[0].failure_policy).toBe('failover');expect(state.saves[0].body.jobs[1].failure_policy).toBe('strict');
 state.sessions[0].failover_count=2;state.sessions[0].last_failover={at:'2026-10-16T18:00:00Z',from_node:'node-old',to_node:'node-1',from_name:'US old node',to_name:'US node 1',reason:'node_missing'};
 await jobs(page);await expect(page.locator('[data-status-job="accounts"]')).toContainText(locale==='en'?'Failover':'故障转移');
 const history=page.locator('.job-failover-history').first();await history.locator('summary').first().focus();await page.keyboard.press('Enter');
 await expect(history).toContainText('US old node');await expect(history).toContainText('US node 1');
 await history.evaluate(el=>el.scrollIntoView({block:'center'}));await page.screenshot({path:testInfo.outputPath('failover-history.png')});
 expect(state.migrations).toHaveLength(0);expect(state.calls.filter(c=>c.startsWith('POST /api/jobs/'))).toEqual([]);
});

test('failover draft survives failed save and unsupported modes reset to strict',async({page})=>{
 const state=await setup(page);await settings(page);await page.locator('[data-job-row]').first().locator('summary').first().click();
 await page.locator('#job-0-failure_policy').selectOption('failover');state.saveStatus=500;await save(page);
 await expect(page.locator('#job-0-failure_policy')).toHaveValue('failover');await expect(page.locator('#settingsSaveStatus')).not.toHaveText('');
 await page.locator('#job-0-ingress').selectOption('direct');await expect(page.locator('#job-0-failure_policy')).toBeDisabled();await expect(page.locator('#job-0-failure_policy')).toHaveValue('strict');
 await page.locator('#job-0-ingress').selectOption('shared');await expect(page.locator('#job-0-failure_policy')).toBeEnabled();await page.locator('#job-0-failure_policy').selectOption('failover');
 await page.locator('#job-0-mode').selectOption('pooled');await expect(page.locator('#job-0-failure_policy')).toBeHidden();
 state.saveStatus=200;await save(page);await expect.poll(()=>state.saves.length).toBe(2);expect(state.saves[1].body.jobs[0].failure_policy).toBe('strict');
});

test('failover history is escaped and malformed history is rejected',async({page})=>{
 const state=await setup(page);state.sessions[0].failover_count=1;state.sessions[0].last_failover={at:'2026-10-16T18:00:00Z',from_node:'old',to_node:'node-1',from_name:'<img src=x onerror="window.__bad=true">',reason:'dial_failed'};
 await jobs(page);const history=page.locator('.job-failover-history').first();await history.locator('summary').first().click();await expect(history).toContainText('<img');await expect(history.locator('img')).toHaveCount(0);expect(await page.evaluate(()=>window.__bad)).toBeUndefined();
 state.sessions[0].last_failover.at={bad:true};await page.locator('[data-session-search]').fill('account');await expect(page.locator('[data-session-error]')).not.toHaveText('');
});

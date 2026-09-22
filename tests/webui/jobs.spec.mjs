import { expect, test } from '@playwright/test';
import { mockAPI } from './fixtures.mjs';

const baseJob = {name:'accounts', mode:'pinned', selection:'manual', profile:'hk-fast', endpoint:'hk-only', target_url:'', size:0, concurrency:8, timeout:'15s', refresh_interval:'10m', session_ttl:'30m', max_sessions:1024, probe_batch_size:32, probe_concurrency:4, max_response_bytes:2097152, expected_status:200, body_contains:''};
const autoJob = {...baseJob,name:'catalog',mode:'pooled',selection:'auto',endpoint:'catalog',target_url:'https://example.com/catalog',size:5};
async function setup(page, {locale='zh',width=1280,role='admin'}={}) {
  const state = {jobs:[structuredClone(baseJob),structuredClone(autoJob)], calls:[], saves:[], saveStatus:200, saveWait:null, accessWait:null, accessStatus:200, releaseWait:null, persistAcquire:false, refreshing:false, profileRegex:'',
    sessions:Array.from({length:23},(_,i)=>({job:i===22?'deleted-job':'accounts',session:`account-${String(i+1).padStart(2,'0')}`,node:`node-${i%3+1}`,state:i===22?'removed':i===21?'expired':'ready',expires_at:'2027-01-01T12:00:00Z'}))};
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
      const q=url.searchParams;let rows=state.sessions.filter(s=>(!q.get('job')||s.job===q.get('job'))&&(!q.get('state')||s.state===q.get('state'))&&(!q.get('q')||`${s.job} ${s.session} ${s.node}`.includes(q.get('q'))));
      const pages=Math.ceil(rows.length/20),current=Math.min(Number(q.get('page')||1),pages||1);
      response.body={sessions:rows.slice((current-1)*20,current*20),pagination:{page:current,page_size:20,total_items:rows.length,total_pages:pages}};
    }
    if(path.endsWith('/acquire')) {
      const session=request.postDataJSON().session;
      if(state.accessWait)await state.accessWait;
      if(state.accessStatus!==200){response.status=state.accessStatus;response.body={error:'test acquire rejected'};return response;}
      if(state.persistAcquire&&!state.sessions.some(s=>s.job==='accounts'&&s.session===session))state.sessions.push({job:'accounts',session,node:'node-1',state:'ready',expires_at:'2027-01-01T12:00:00Z'});
      response.body={job:'accounts',mode:'pinned',state:'ready',node:'node-1',session,proxy_url:`http://hk-session-${session}:hk-secret@127.0.0.1:23231`,concurrency:8,timeout:'15s',expires_at:'2027-01-01T12:00:00Z'};
    }
    if(path.endsWith('/release')) {if(state.releaseWait)await state.releaseWait;state.sessions=state.sessions.filter(s=>!(s.job===path.split('/')[3]&&s.session===request.postDataJSON().session));response.body={ok:true};}
    return response;
  });
  await page.goto('/');
  await expect(page.locator('#healthyNodeRate')).toBeVisible();
  return state;
}
const settings=async page=>{await page.locator('[data-tab="settings"]').click();await expect(page.locator('#job-1-name')).toHaveValue('catalog');await expect.poll(()=>page.locator('#settingsTab form').evaluate(el=>el.inert)).toBe(false);};
const jobs=async page=>{await page.locator('[data-tab="jobs"]').click();await expect(page.locator('.job-status-card')).toHaveCount(2);await expect(page.locator('[data-session-count]')).toContainText('23');};
const save=page=>page.locator('#settingsTab button[type="submit"]').click();

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
  await page.locator('[data-tab="jobs"]').click();await expect(page.locator('#confirmDialog')).toBeVisible();await page.keyboard.press('Escape');
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
  await page.locator('[data-tab="jobs"]').click();
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
  await page.locator('[data-tab="jobs"]').click();await expect(page.locator('#settingsTab')).toHaveClass(/active/);
  finish();await expect(page.locator('#settingsSaveStatus')).toContainText('配置已保存');
  await expect.poll(()=>page.locator('#settingsTab form').evaluate(el=>el.inert)).toBe(false);
});

test('adding and deleting jobs is a draft operation with cancellable confirmation',async({page})=>{
  const state=await setup(page);await settings(page);
  await page.locator('[data-add-job]').click();await expect(page.locator('[data-job-row]')).toHaveCount(3);
  await expect(page.locator('#job-2-name')).toBeFocused();
  await page.locator('[data-remove-job="2"]').click();await page.keyboard.press('Escape');
  await expect(page.locator('[data-job-row]')).toHaveCount(3);
  await page.locator('[data-remove-job="2"]').click();await expect(page.locator('#confirmMessage')).toContainText('普通 Profile');
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
  await page.locator('[data-tab="dashboard"]').click();await expect(page.locator('#jobAccessURL')).toHaveValue('');
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
  await page.locator('[data-tab="jobs"]').click();
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
  await page.locator('[data-tab="jobs"]').click();
  await expect(page.locator('[data-job-status-list]')).toContainText('尚未配置任务');
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
  await page.locator('[data-tab="settings"]').click();
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
  await page.locator('[data-tab="jobs"]').click();
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
  await page.locator('[data-tab="dashboard"]').click();
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
  await page.locator('[data-tab="dashboard"]').click();
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
  await page.locator('[data-tab="jobs"]').click();await expect(page.locator('.job-status-card')).toHaveCount(1);
  await page.locator('[data-refresh-job="access"]').click();
  await expect(page.locator('[data-acquire-job]')).toBeEnabled();
  await page.locator('[data-acquire-job]').click();
  await expect.poll(()=>state.calls.includes('POST /api/jobs/access/acquire')).toBe(true);
});

for(const [locale,width] of [['zh',390],['en',1280]])test(`jobs render and navigate in ${locale} at ${width}px`,async({page},testInfo)=>{
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await setup(page,{locale,width});await jobs(page);
  await expect(page.locator('#jobsTab h1')).toHaveText(locale==='en'?'Scraping jobs':'抓取任务');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.locator('.job-status-card summary').first().click();
  await page.screenshot({path:testInfo.outputPath('jobs-runtime.png'),fullPage:true});
  await page.locator('#jobSessionState').selectOption('removed');
  await page.locator('.job-sessions').scrollIntoViewIfNeeded();
  await page.screenshot({path:testInfo.outputPath('jobs-sessions.png')});
  await page.locator('[data-configure-jobs]').click();await expect(page.locator('[data-add-job]')).toBeFocused();
  await expect(page.locator('.jobs-editor .panel-title')).toHaveText(locale==='en'?'Job configuration':'抓取任务配置');
  await page.locator('[data-job-row]').last().locator('summary').first().focus();await page.keyboard.press('Space');await page.keyboard.press('Space');
  await page.locator('#job-1-name').scrollIntoViewIfNeeded();
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.screenshot({path:testInfo.outputPath('jobs-settings.png')});
  await page.locator('#job-1-max_response_bytes').scrollIntoViewIfNeeded();
  await page.screenshot({path:testInfo.outputPath('jobs-benchmark-settings.png')});
  expect(errors).toEqual([]);
});

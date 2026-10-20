import { expect, test } from '@playwright/test';
import { mockAPI, navigate } from './fixtures.mjs';

// Labeled fixtures exercise the existing server-side query contract, not live inventory.
const inventory = [
  ...Array.from({length:32},(_,i)=>({tag:`blocked-${i}`,name:`US Blocked fixture ${String(i).padStart(2,'0')}`,region:'us',blacklisted:true,active_connections:2})),
  {tag:'jp-blocked',name:'JP Blocked fixture',region:'jp',blacklisted:true},
  {tag:'resident-blocked',name:'Residential Blocked fixture',region:'resident',blacklisted:true,active_connections:1},
  {tag:'healthy',name:'US Healthy fixture',region:'us',initial_check_done:true,available:true,active_connections:3},
  {tag:'cooling',name:'US Cooling fixture',region:'us',cooling_down:true},
  {tag:'failed',name:'US Failed fixture',region:'us',initial_check_done:true,available:false},
  {tag:'unknown',name:'US Unknown fixture',region:'us',initial_check_done:false},
];
const matches = (node,status) => ({
  all:true,
  healthy:node.initial_check_done&&node.available&&!node.blacklisted&&!node.cooling_down,
  blacklisted:node.blacklisted,
  cooling:node.cooling_down,
  unavailable:node.blacklisted||node.cooling_down||(node.initial_check_done&&!node.available),
  unknown:!node.initial_check_done&&!node.blacklisted&&!node.cooling_down,
})[status];

async function setup(page,{locale='en',width=1280,theme='light'}={}) {
  const state={queries:[],fail:false,blockedWait:null,refreshWait:null,nodes:structuredClone(inventory)};
  await page.setViewportSize({width,height:1000});
  await page.addInitScript(({locale,theme})=>{localStorage.setItem('uiLanguage',locale);localStorage.setItem('themeMode',theme);},{locale,theme});
  await mockAPI(page,async response=>{
    if(response.url.pathname!=='/api/nodes')return response;
    const q=Object.fromEntries(response.url.searchParams);state.queries.push(q);
    if(state.refreshWait)await state.refreshWait;
    if(q.status==='blacklisted'&&state.blockedWait)await state.blockedWait;
    if(state.fail)return {...response,status:503,body:{error:'fixture unavailable'}};
    let rows=state.nodes.filter(n=>(q.region==='all'||n.region===q.region)&&matches(n,q.status)
      &&(q.activity==='active'?n.active_connections>0:q.activity==='idle'?!(n.active_connections>0):true)
      &&(!q.search||`${n.name} ${n.tag}`.toLowerCase().includes(q.search)));
    if(q.sort==='name')rows.sort((a,b)=>a.name.localeCompare(b.name)*(q.order==='desc'?-1:1));
    if(q.sort==='connections')rows.sort((a,b)=>((a.active_connections||0)-(b.active_connections||0))*(q.order==='desc'?-1:1));
    const total=rows.length,size=Number(q.page_size),pages=Math.ceil(total/size),current=Math.min(Number(q.page),pages||1);
    response.body={...response.body,nodes:rows.slice((current-1)*size,current*size),pagination:{page:current,page_size:size,total_items:total,total_pages:pages},summary:{total_nodes:state.nodes.length,healthy_nodes:1,unavailable_nodes:36,unknown_nodes:1,
      active_nodes:state.nodes.filter(n=>n.active_connections>0).length,active_connections:state.nodes.reduce((total,n)=>total+(n.active_connections||0),0)}};
    return response;
  });
  await page.goto('/');
  await expect(page.locator('#totalNodes')).toHaveText(String(inventory.length));
  await page.locator('#autoRefreshBtn').click();
  return state;
}
const statusButton=(page,status)=>page.locator(`#nodeStatusFilter [data-status="${status}"]`);
const regionButton=(page,region)=>page.locator(`#regionFilter [data-region="${region}"]`);
const activityButton=(page,activity)=>page.locator(`#nodeActivityFilter [data-activity="${activity}"]`);
const rows=page=>page.locator('#nodesTableBody tr');

for(const [locale,width,theme] of [['zh',1280,'light'],['en',390,'dark'],['en',1280,'dark'],['zh',390,'light']]) {
  test(`node status controls combine and remain accessible (${locale}, ${width}, ${theme})`,async({page},testInfo)=>{
    const errors=[];page.on('pageerror',error=>errors.push(error.message));
    const state=await setup(page,{locale,width,theme});
    await statusButton(page,'blacklisted').focus();await page.keyboard.press('Enter');
    await expect(rows(page)).toHaveCount(34);
    await regionButton(page,'resident').click();
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page)).toContainText('Residential Blocked fixture');
    await expect(statusButton(page,'blacklisted')).toHaveAttribute('aria-pressed','true');
    await expect(regionButton(page,'resident')).toHaveAttribute('aria-pressed','true');
    expect(state.queries.at(-1)).toMatchObject({region:'resident',status:'blacklisted',page:'1'});
    await page.locator('#nodeStatusFilter').evaluate(el=>el.closest('.panel').scrollIntoView({block:'start'}));
    await page.screenshot({path:testInfo.outputPath('node-status-filters.png')});
    expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
    await page.locator('#nodeSearch').fill('not-found');
    await expect(rows(page)).toHaveCount(0);
    await expect(page.locator('#emptyState')).toHaveText(locale==='en'?'No nodes match these filters.':'没有符合当前筛选的节点。');
    await page.locator('#clearNodeFilters').click();
    await expect(rows(page)).toHaveCount(38);
    await expect(page.locator('#clearNodeFilters')).toBeDisabled();
    await expect(statusButton(page,'all')).toHaveAttribute('aria-pressed','true');
    await expect(regionButton(page,'all')).toHaveAttribute('aria-pressed','true');
    for(const [status,count] of [['healthy',1],['cooling',1],['unavailable',36],['unknown',1]]) {
      await statusButton(page,status).click();await expect(rows(page)).toHaveCount(count);
    }
    await expect(page.locator('#totalNodes')).toHaveText('38'); // Dashboard summary stays global.
    expect(errors).toEqual([]);
  });
}

test('filters reset paging but survive sorting, refresh, navigation and language changes',async({page})=>{
  const state=await setup(page);
  await page.locator('#nodePageSize').selectOption('25');
  await page.locator('#nodePageJump').fill('2');await page.locator('#nodePageJump').press('Enter');
  await expect(page.locator('#nodePageInfo')).toContainText('2/2');
  await statusButton(page,'blacklisted').click();
  await expect(page.locator('#nodePageJump')).toHaveValue('1');
  await regionButton(page,'us').click();
  await page.locator('#nodeSearch').fill('fixture');
  await expect.poll(()=>state.queries.at(-1)?.search).toBe('fixture');
  await page.locator('[data-sort-table="nodes"][data-sort-key="name"]').click();
  await page.locator('#nodePageJump').fill('2');await page.locator('#nodePageJump').press('Enter');
  await expect(rows(page)).toHaveCount(7);
  await page.locator('#dashboardRefresh').click();
  await expect(page.locator('#nodeFilterFeedback')).toBeHidden();
  expect(state.queries.at(-1)).toMatchObject({region:'us',status:'blacklisted',search:'fixture',page:'2',page_size:'25',sort:'name'});
  for(const invalid of ['', '0','3','1.5']) {
    const before=state.queries.length;
    await page.locator('#nodePageJump').fill(invalid);await page.locator('#nodePageJump').press('Enter');
    expect(state.queries.length).toBe(before);
    expect(await page.locator('#nodePageJump').evaluate(el=>el.validity.valid)).toBe(false);
  }
  await navigate(page,'settings');await page.locator('#settingLanguage').selectOption('zh-CN');
  await navigate(page,'dashboard');
  await expect(statusButton(page,'blacklisted')).toHaveAttribute('aria-pressed','true');
  await expect(statusButton(page,'blacklisted')).toHaveText('已拉黑');
  await expect(regionButton(page,'us')).toHaveAttribute('aria-pressed','true');
  await page.locator('#clearNodeFilters').click();
  await expect(page.locator('#nodePageJump')).toHaveValue('1');
  expect(state.queries.at(-1)).toMatchObject({region:'all',status:'all',page:'1',page_size:'25',sort:'name'});
});

for(const [locale,width,theme] of [['zh',1280,'light'],['en',390,'dark']])test(`active usage combines with health and region without treating bindings as usage (${locale})`,async({page},testInfo)=>{
  const state=await setup(page,{locale,width,theme});
  await expect(page.locator('#activeNodeCount')).toHaveText(locale==='en'?'34 active nodes':'34 个使用中节点');
  await expect(page.locator('#activeConnections')).toHaveText('68');
  await activityButton(page,'active').focus();await page.keyboard.press('Enter');
  await expect(rows(page)).toHaveCount(34);
  await page.locator('#nodePageSize').selectOption('25');
  await expect(rows(page)).toHaveCount(25);
  await expect(page.locator('#activeNodeCount')).toContainText('34'); // Global node count is not the current page or connection count.
  await page.locator('#nodePageJump').fill('2');await page.locator('#nodePageJump').press('Enter');
  await expect(rows(page)).toHaveCount(9);
  await regionButton(page,'resident').click();
  await statusButton(page,'blacklisted').click();
  await expect(rows(page)).toHaveCount(1);
  await expect(rows(page)).toContainText('Residential Blocked fixture');
  await expect(page.locator('#nodePageJump')).toHaveValue('1');
  await expect(activityButton(page,'active')).toHaveAttribute('aria-pressed','true');
  expect(state.queries.at(-1)).toMatchObject({region:'resident',status:'blacklisted',activity:'active',page:'1'});
  await page.locator('#nodeActivityFilter').evaluate(el=>el.closest('.panel').scrollIntoView({block:'start'}));
  await page.screenshot({path:testInfo.outputPath('active-node-filter.png')});
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.locator('#nodeSearch').fill('not-found');
  await expect(rows(page)).toHaveCount(0);
  await expect(page.locator('#emptyState')).toHaveText(locale==='en'?'No nodes match these filters.':'没有符合当前筛选的节点。');
  await page.locator('#clearNodeFilters').click();
  await expect(activityButton(page,'all')).toHaveAttribute('aria-pressed','true');
  await expect(page.locator('#clearNodeFilters')).toBeDisabled();
  await expect(rows(page)).toHaveCount(25);
  await statusButton(page,'healthy').click();await activityButton(page,'active').click();
  await expect(rows(page)).toHaveCount(1);
  state.nodes.find(n=>n.tag==='healthy').active_connections=0;
  await page.locator('#dashboardRefresh').click();await expect(rows(page)).toHaveCount(0);
  await expect(page.locator('#activeNodeCount')).toContainText('33');
  await activityButton(page,'idle').click();
  await expect(rows(page)).toHaveCount(1);
  await expect(rows(page)).toContainText('US Healthy fixture'); // Healthy does not imply active.
  expect(state.queries.at(-1)).toMatchObject({status:'healthy',activity:'idle'});
});

test('active usage survives sorting, refresh and navigation and ignores late responses',async({page})=>{
  const state=await setup(page);let release;
  state.refreshWait=new Promise(resolve=>{release=resolve;});
  await activityButton(page,'active').click();
  await expect.poll(()=>state.queries.at(-1)?.activity).toBe('active');
  state.refreshWait=null;
  await activityButton(page,'idle').click();await expect(rows(page)).toHaveCount(4);
  const lateResponse=page.waitForResponse(r=>new URL(r.url()).searchParams.get('activity')==='active');
  release();await lateResponse;
  await expect(activityButton(page,'idle')).toHaveAttribute('aria-pressed','true');
  await expect(rows(page)).toHaveCount(4);
  await page.locator('#clearNodeFilters').click();
  await expect(rows(page)).toHaveCount(38);
  await activityButton(page,'active').click();
  await page.locator('[data-sort-table="nodes"][data-sort-key="connections"]').click();
  await expect(rows(page).first()).toContainText('US Healthy fixture');
  await page.locator('#dashboardRefresh').click();
  await expect(page.locator('#nodeFilterFeedback')).toBeHidden();
  await navigate(page,'settings');await page.locator('#settingLanguage').selectOption('zh-CN');await navigate(page,'dashboard');
  await expect(activityButton(page,'active')).toHaveAttribute('aria-pressed','true');
  await expect(activityButton(page,'active')).toHaveText('使用中');
  await expect(page.locator('#activeNodeCount')).toHaveText('34 个使用中节点');
  expect(state.queries.at(-1)).toMatchObject({activity:'active',sort:'connections',order:'desc'});
});

test('missing active-node summary is unknown rather than inferred from a filtered page',async({page})=>{
  await setup(page);
  await page.route('**/api/nodes?**',route=>route.fulfill({json:{nodes:[],pagination:{page:1,page_size:50,total_items:0,total_pages:0},summary:{total_nodes:200,active_connections:20}}}));
  await activityButton(page,'active').click();
  await expect(page.locator('#activeNodeCount')).toHaveText('Active node count unavailable');
  await expect(page.locator('#activeConnections')).toHaveText('20');
});

test('late responses cannot replace a newer status and failures disclose stale rows',async({page},testInfo)=>{
  const state=await setup(page);let unblock;
  state.blockedWait=new Promise(resolve=>{unblock=resolve;});
  await statusButton(page,'blacklisted').click();
  await expect.poll(()=>state.queries.at(-1)?.status).toBe('blacklisted');
  await expect(page.locator('#nodeFilterFeedback')).toContainText('Updating nodes');
  await statusButton(page,'healthy').click();await expect(rows(page)).toHaveCount(1);
  const lateResponse=page.waitForResponse(response=>new URL(response.url()).searchParams.get('status')==='blacklisted');
  unblock();state.blockedWait=null;await lateResponse;
  await expect(rows(page)).toContainText('US Healthy fixture');
  await expect(page.locator('#nodeFilterFeedback')).toBeHidden();
  state.fail=true;
  await statusButton(page,'cooling').click();
  await expect(page.locator('#nodeFilterFeedback')).toContainText('Previous results below may not match');
  await expect(rows(page)).toContainText('US Healthy fixture');
  await expect(page.locator('#nodeResults')).toHaveAttribute('aria-busy','false');
  await page.locator('#nodeStatusFilter').evaluate(el=>el.closest('.panel').scrollIntoView({block:'start'}));
  await page.screenshot({path:testInfo.outputPath('node-filter-error.png')});
  state.fail=false;await page.locator('#dashboardRefresh').click();
  await expect(rows(page)).toContainText('US Cooling fixture');
  await expect(page.locator('#nodeFilterFeedback')).toBeHidden();
  let finishRefresh;state.refreshWait=new Promise(resolve=>{finishRefresh=resolve;});
  await page.locator('#dashboardRefresh').click();
  await expect(page.locator('#nodeResults')).toHaveAttribute('aria-busy','true');
  await expect(page.locator('#nodeFilterFeedback')).toBeHidden(); // Same query must not shift the table.
  finishRefresh();state.refreshWait=null;
  await expect(page.locator('#nodeResults')).toHaveAttribute('aria-busy','false');
});

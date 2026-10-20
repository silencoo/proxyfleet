import { expect, test } from '@playwright/test';
import { mockAPI, navigate } from './fixtures.mjs';
import { readFileSync } from 'node:fs';

async function boot(page, locale = 'en', width = 1280) {
  await page.setViewportSize({width, height: 960});
  await page.addInitScript(locale => localStorage.setItem('uiLanguage', locale), locale);
  await mockAPI(page, response => {
    const path = response.url.pathname;
    if (path === '/api/jobs') response.body = {jobs: []};
    if (path === '/api/jobs/sessions') response.body = {sessions: [], pagination: {page:1,total_pages:0,total_items:0}};
    if (path === '/api/nodes') {
      response.body.summary = {...response.body.summary,total_nodes:1,healthy_nodes:1,active_nodes:1,active_connections:2};
      response.body.nodes = [{tag:'fixture-node',name:'Fixture node',region:'us',initial_check_done:true,available:true,active_connections:2}];
      response.body.pagination = {page:1,page_size:50,total_items:1,total_pages:1};
      response.body.region_stats = {resident:1}; response.body.region_healthy = {resident:1};
    }
    return response;
  });
  await page.goto('/');
  await expect(page.locator('#activeNodeCount')).toContainText('1');
  await page.locator('#autoRefreshBtn').click();
}

async function untranslatedChinese(page) {
  return page.evaluate(() => {
    const missing=[];
    const walker=document.createTreeWalker(document.body,NodeFilter.SHOW_TEXT);
    for(let node;node=walker.nextNode();) {
      const p=node.parentElement,text=node.textContent.trim();
      if(!p||p.closest('script,style,svg,option,[translate="no"]')||!p.checkVisibility()||!/[\u3400-\u9fff]/u.test(text))continue;
      missing.push(text);
    }
    for(const el of document.querySelectorAll('[aria-label],[title],[placeholder]')) {
      if(!el.checkVisibility()||el.closest('[translate="no"]'))continue;
      for(const key of ['aria-label','title','placeholder']) {
        const value=el.getAttribute(key)||'';
        if(/[\u3400-\u9fff]/u.test(value))missing.push(`${key}: ${value}`);
      }
    }
    return [...new Set(missing)];
  });
}
test('English UI copy and accessible names are localized across every route',async({page})=>{
  await boot(page);
  const missing={};
  for(const route of ['dashboard','manage','jobs','debug','operations','logs','settings']) {
    await navigate(page,route);
    await expect(page.locator(`#${route}Tab`)).toHaveClass(/active/);
    missing[route]=await untranslatedChinese(page);
  }
  expect(missing).toEqual(Object.fromEntries(Object.keys(missing).map(key=>[key,[]])));
});

test('all static UI messages, including hidden/loading states, have an English translation',async({page})=>{
  await boot(page);
  const missing=await page.evaluate(html=>{
    const root=new DOMParser().parseFromString(html,'text/html').body, values=[];
    const walker=document.createTreeWalker(root,NodeFilter.SHOW_TEXT);
    for(let n;n=walker.nextNode();)if(!n.parentElement?.closest('script,style,svg,#settingLanguage'))values.push(n.textContent.trim());
    root.querySelectorAll('[title],[placeholder],[aria-label]').forEach(el=>['title','placeholder','aria-label'].forEach(key=>values.push(el.getAttribute(key)||'')));
    return [...new Set(values.filter(s=>/[\u3400-\u9fff]/u.test(s)&&/[\u3400-\u9fff]/u.test(window.proxyFleetI18n.tr(s))))];
  },readFileSync(new URL('../../webui/index.html',import.meta.url),'utf8'));
  expect(missing).toEqual([]);
});

for(const width of [1280,390])test(`language round trips translate filters and dynamic values without losing settings drafts (${width})`,async({page},testInfo)=>{
  await boot(page,'en',width);
  await page.locator('#nodeActivityFilter [data-activity="active"]').click();
  await navigate(page,'settings');
  await expect(page.locator('[data-profile-preview-status]')).toContainText('42/100');
  await page.locator('#settingExternalIP').fill('198.51.100.23');
  await page.locator('[data-endpoint-row]').first().locator('[data-field="username"]').fill('locale-draft');
  for(const locale of ['zh-CN','en','zh-CN','en']) {
    await page.locator('#settingLanguage').selectOption(locale);
    const en=locale==='en';
    await expect(page.locator('#nodeActivityFilter [data-activity="active"]')).toHaveText(en?'Active':'使用中');
    await expect(page.locator('#nodeActivityFilter [data-activity="idle"]')).toHaveText(en?'Idle':'空闲');
    await expect(page.locator('#nodeStatusFilter [data-status="blacklisted"]')).toHaveText(en?'Blocked':'已拉黑');
    await expect(page.locator('#regionFilter [data-region="resident"]')).toHaveText(en?'Residential':'住宅');
    await expect(page.locator('#activeNodeCount')).toHaveText(en?'1 active node':'1 个使用中节点');
    await expect(page.locator('#healthyNodeRate')).toHaveText(en?'100.0% of all nodes · 0 unknown':'全部节点的 100.0% · 0 个未知');
    await expect(page.locator('#subscriptionStatus')).toHaveText(en?'Subscription nodes: 14':'订阅节点：14');
    await expect(page.locator('#roleBadge')).toHaveText(en?'Admin':'管理员');
    await expect(page.locator('#roleBadge')).toHaveAttribute('title',en?'Current permission role':'当前权限角色');
    await expect(page.locator('#buildInfoCapabilities')).toContainText(en?'QUIC compiled':'QUIC 已编译');
    await expect(page.locator('[data-profile-preview-status]')).toHaveText(en?'42/100 matched':'42/100 命中');
    await expect(page.locator('[data-profile-preview]')).toContainText(en?'Name rules excluded: 41':'名称规则 排除：41');
    await expect(page.locator('#settingExternalIP')).toHaveValue('198.51.100.23');
    await expect(page.locator('[data-endpoint-row]').first().locator('[data-field="username"]')).toHaveValue('locale-draft');
    await expect(page.locator('#nodeActivityFilter [data-activity="active"]')).toHaveAttribute('aria-pressed','true');
  }
  await page.locator('#settingLanguage').selectOption(width===1280?'zh-CN':'en');
  await page.locator('#settingExternalIP').fill('');
  await page.locator('[data-endpoint-row]').first().locator('[data-field="username"]').fill('fleet');
  await navigate(page,'dashboard');
  await expect(page.locator('#regionChart canvas')).toHaveCount(1);
  await expect.poll(()=>page.evaluate(()=>window.echarts.getInstanceByDom(document.getElementById('regionChart'))?.getOption().series?.[0].data?.length)).toBe(1);
  const region=await page.evaluate(()=>{
    const chart=window.echarts.getInstanceByDom(document.getElementById('regionChart')),options=chart.getOption();
    const node=options.series[0].data[0];
    return {name:node.name,tooltip:options.tooltip[0].formatter({name:node.name,data:node})};
  });
  expect(region.name).toBe(width===1280?'住宅':'Residential');
  expect(region.tooltip).toContain(width===1280?'区域可用':'Available');
  await page.locator('#nodeActivityFilter').scrollIntoViewIfNeeded();
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.screenshot({path:testInfo.outputPath('localized-dashboard.png')});
});

test('message updates do not revive old text or interpret interpolated values as templates',async({page})=>{
  await boot(page);
  const result=await page.evaluate(()=>{
    const i18n=window.proxyFleetI18n,root=document.createElement('div');document.body.append(root);
    const text=document.createElement('span');root.append(text);
    i18n.setText(text,'{count} 个使用中节点',{count:'$&{count}'});
    const literal=text.textContent;
    text.textContent='live application data';
    i18n.translateTree(root);
    const unchanged=text.textContent;
    const control=document.createElement('button');root.append(control);control.textContent='显示敏感信息';
    i18n.translateTree(root);control.textContent='隐藏敏感信息';i18n.translateTree(root);
    const updated=control.textContent;
    const data=document.createElement('span');data.translate=false;data.textContent='全部';root.append(data);i18n.translateTree(root);
    const userData=data.textContent;root.remove();
    return {literal,unchanged,updated,userData};
  });
  expect(result).toEqual({literal:'$&{count} active nodes',unchanged:'live application data',updated:'Hide sensitive value',userData:'全部'});
});

import { expect, test } from '@playwright/test';
import { mockAPI, navigate } from './fixtures.mjs';

async function boot(page, {width=1440, locale='en', theme='light', role='admin', hash=''}={}) {
  const errors=[];
  page.on('pageerror', error=>errors.push(error.message));
  await page.setViewportSize({width,height:900});
  await page.emulateMedia({reducedMotion:'reduce'});
  await page.addInitScript(({locale,theme})=>{
    if (!localStorage.getItem('uiLanguage')) localStorage.setItem('uiLanguage',locale);
    if (!localStorage.getItem('themeMode')) localStorage.setItem('themeMode',theme);
  },{locale,theme});
  const calls=[];
  await mockAPI(page, response=>{
    calls.push(response.url.pathname);
    if(response.url.pathname==='/api/session')response.body={role};
    return response;
  });
  await page.goto(`/${hash}`);
  await expect(page.locator('#lastUpdate')).not.toHaveText(/Waiting|等待/);
  return {errors,calls};
}

test('desktop rail persists, reclaims width and preserves scoped controls',async({page},testInfo)=>{
  const {errors}=await boot(page);
  await expect(page.locator('.header')).toHaveCount(0);
  await expect(page.locator('.mobile-bar')).toBeHidden();
  const expanded=await page.locator('#mainContent').boundingBox();
  expect(expanded.y).toBe(0);
  await expect(page.locator('#dashboardTab [onclick="probeAllNodes()"]')).toBeVisible();
  await expect(page.locator('#dashboardTab [onclick="exportNodes()"]')).toBeVisible();
  await page.screenshot({path:testInfo.outputPath('dashboard-expanded.png')});
  await page.getByRole('button',{name:'Collapse navigation',exact:true}).click();
  await expect(page.getByRole('button',{name:'Expand navigation',exact:true})).toBeVisible();
  const collapsed=await page.locator('#mainContent').boundingBox();
  expect(collapsed.x).toBe(72);
  expect(collapsed.width-expanded.width).toBe(152);
  await navigate(page,'manage');
  await expect(page.locator('#refreshSubBtn')).toBeVisible();
  await expect(page.locator('[onclick="probeAllNodes()"]')).toBeHidden();
  await expect(page.locator('[onclick="exportNodes()"]')).toBeHidden();
  await expect(page).toHaveURL(/#manage$/);
  await page.reload();
  await expect(page.locator('#manageTab')).toHaveClass(/active/);
  await expect(page.getByRole('button',{name:'Expand navigation',exact:true})).toBeVisible();
  await navigate(page,'debug');await page.goBack();
  await expect(page.locator('#manageTab')).toHaveClass(/active/);
  await page.setViewportSize({width:1024,height:900});
  await page.screenshot({path:testInfo.outputPath('rail-1024.png')});
  expect(errors).toEqual([]);
});

for(const [width,locale,theme] of [[390,'zh','dark'],[320,'en','light']]) {
  test(`mobile drawer focus, resize and route identity at ${width}px`,async({page},testInfo)=>{
    const {errors}=await boot(page,{width,locale,theme});
    await expect(page.locator('.nav')).toBeHidden();
    await expect(page.locator('#mobileRoute')).toHaveText(locale==='zh'?'监控看板':'Dashboard');
    await page.locator('#menuToggle').focus();await page.keyboard.press('Enter');
    await expect(page.locator('#navigationDrawer')).toBeVisible();
    await expect(page.locator('[data-tab="dashboard"]')).toBeFocused();
    for(let i=0;i<12;i++) {
      await page.keyboard.press('Tab');
      expect(await page.evaluate(()=>document.getElementById('navigationDrawer').contains(document.activeElement))).toBe(true);
    }
    await page.screenshot({path:testInfo.outputPath(`drawer-${width}.png`)});
    await page.keyboard.press('Escape');
    await expect(page.locator('#menuToggle')).toBeFocused();
    await expect(page.locator('#menuToggle')).toHaveAttribute('aria-expanded','false');
    await navigate(page,'settings');
    await expect(page.locator('#mobileRoute')).toHaveText(locale==='zh'?'系统设置':'System settings');
    await expect(page.locator('#navigationDrawer')).toBeHidden();
    await expect(page.locator('#settingTheme')).toBeVisible();
    expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
    await page.screenshot({path:testInfo.outputPath(`settings-${width}.png`)});
    await page.locator('#menuToggle').click();
    await page.setViewportSize({width:1024,height:900});
    await expect(page.locator('#navigationDrawer')).not.toHaveAttribute('open','');
    await expect(page.locator('[data-tab="settings"]')).toBeFocused();
    await page.locator('#sidebarToggle').click();
    await page.setViewportSize({width,height:900});
    await navigate(page,'dashboard');
    await page.setViewportSize({width:1024,height:900});
    await expect(page.locator('#sidebarToggle')).toHaveAttribute('aria-expanded','false');
    expect(errors).toEqual([]);
  });
}

for (const width of [390,1024])test(`stale dashboard remains actionable with navigation closed at ${width}px`,async({page},testInfo)=>{
  const {errors}=await boot(page,{width});
  if(width>900)await page.locator('#sidebarToggle').click();
  await page.route('**/api/nodes?**',route=>route.fulfill({status:503,json:{error:'unavailable'}}));
  await page.locator('#dashboardRefresh').click();
  await expect(page.locator('#dashboardFailure')).toBeVisible();
  await expect(page.locator('#dashboardRefresh')).toBeEnabled();
  await navigate(page,'debug');
  await expect(page.locator('#dashboardFailure')).toBeVisible();
  await page.screenshot({path:testInfo.outputPath(`stale-${width}.png`)});
  await page.unroute('**/api/nodes?**');
  await page.getByRole('button',{name:'Retry dashboard update'}).click();
  await expect(page.locator('#dashboardFailure')).toBeHidden();
  await expect(page.locator('#debugTab')).toHaveClass(/active/);
  expect(errors).toEqual([]);
});

test('initial connection failure can recover role, route and data',async({page})=>{
  await mockAPI(page);
  await page.route('**/api/nodes**',route=>route.fulfill({status:503,json:{error:'offline'}}));
  await page.goto('/#settings');
  await expect(page.locator('#dashboardFailure')).toBeVisible();
  await page.unroute('**/api/nodes**');
  await page.locator('#dashboardFailure button').click();
  await expect(page.locator('#dashboardFailure')).toBeHidden();
  await expect(page.locator('#settingsTab')).toHaveClass(/active/);
  await expect(page.locator('#buildInfoIdentity')).toContainText('ProxyFleet');
});

test('preferences keep theme system support and language without dirtying server configuration',async({page})=>{
  const {errors}=await boot(page,{hash:'#settings'});
  await expect(page.locator('#settingMode')).toHaveValue('pool');
  await page.locator('#settingTheme').selectOption('dark');
  await expect(page.locator('html')).toHaveAttribute('data-theme','dark');
  await page.locator('#settingLanguage').selectOption('zh-CN');
  await expect(page.locator('#settingTheme')).toHaveValue('dark');
  await navigate(page,'debug');
  await expect(page.locator('#confirmOverlay')).not.toHaveClass(/show/);
  await navigate(page,'settings');
  await page.locator('#settingTheme').selectOption('auto');
  await page.emulateMedia({colorScheme:'dark'});
  await expect(page.locator('html')).toHaveAttribute('data-theme','dark');
  await page.emulateMedia({colorScheme:'light'});
  await expect(page.locator('html')).toHaveAttribute('data-theme','light');
  expect(await page.evaluate(()=>({theme:localStorage.getItem('themeMode'),language:localStorage.getItem('uiLanguage')}))).toEqual({theme:'auto',language:'zh-CN'});
  await page.reload();
  await expect(page.locator('#settingsTab')).toHaveClass(/active/);
  await expect(page.locator('#settingLanguage')).toHaveValue('zh-CN');
  await expect(page.locator('#settingTheme')).toHaveValue('auto');
  await page.locator('#settingLanguage').selectOption('en');
  await page.locator('#sidebarToggle').click();
  await expect(page.locator('#sidebarToggle')).toHaveAttribute('aria-label','Expand navigation');
  expect(errors).toEqual([]);
});

test('viewer can use preferences without fetching or revealing admin settings',async({page})=>{
  const {calls,errors}=await boot(page,{role:'viewer',width:390,hash:'#settings'});
  await expect(page.locator('#settingsTab')).toHaveClass(/active/);
  await expect(page.locator('#settingTheme')).toBeVisible();
  await expect(page.locator('#adminSettings')).toBeHidden();
  await expect(page.locator('#roleBadge')).toHaveText('Viewer');
  await page.locator('#settingTheme').selectOption('dark');
  expect(calls).not.toContain('/api/settings');
  expect(calls).not.toContain('/api/jobs');
  expect(errors).toEqual([]);
});

test('mobile dirty navigation keeps draft and returns focus outside the drawer',async({page})=>{
  await boot(page,{width:390,hash:'#settings'});
  await expect(page.locator('#settingMode')).toHaveValue('pool');
  await page.locator('#settingExternalIP').fill('198.51.100.10');
  await navigate(page,'dashboard');
  await expect(page.locator('#navigationDrawer')).toBeHidden();
  await expect(page.locator('#confirmCancel')).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.locator('#settingExternalIP')).toHaveValue('198.51.100.10');
  await expect(page).toHaveURL(/#settings$/);
  await expect(page.locator('#menuToggle')).toBeFocused();
});

test('rail names work on keyboard focus even if preference storage is unavailable',async({page})=>{
  await page.addInitScript(()=>{
    const get=Storage.prototype.getItem, set=Storage.prototype.setItem;
    Storage.prototype.getItem=function(key){if(key==='sidebarCollapsed')throw new Error('storage unavailable');return get.call(this,key);};
    Storage.prototype.setItem=function(key,value){if(key==='sidebarCollapsed')throw new Error('storage unavailable');return set.call(this,key,value);};
  });
  const {errors}=await boot(page);
  await page.locator('#sidebarToggle').click();
  await page.locator('[data-tab="settings"]').focus();
  await expect(page.locator('#navigationTooltip')).toHaveText('System settings');
  await expect(page.locator('#navigationTooltip')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.locator('#navigationTooltip')).toBeHidden();
  await page.keyboard.press('Enter');
  await expect(page.locator('#settingsTab')).toHaveClass(/active/);
  expect(errors).toEqual([]);
});

test('settings and controls reflow under 200% CSS zoom',async({page},testInfo)=>{
  const {errors}=await boot(page,{width:1280,hash:'#settings'});
  await page.evaluate(()=>document.documentElement.style.zoom='2');
  await expect(page.locator('#settingTheme')).toBeVisible();
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.locator('#settingTheme').selectOption('dark');
  await expect(page.locator('html')).toHaveAttribute('data-theme','dark');
  await page.screenshot({path:testInfo.outputPath('settings-css-zoom-200.png')});
  expect(errors).toEqual([]);
});

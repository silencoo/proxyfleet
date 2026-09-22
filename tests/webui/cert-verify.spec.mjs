import { expect, test } from '@playwright/test';
import { mockAPI } from './fixtures.mjs';

for (const [locale, width] of [['zh',390], ['en',1280]]) {
  test(`certificate policy saves, retains conflicts and reloads (${locale}, ${width})`, async ({page}, testInfo) => {
    let mode, skip = false, status = 200, revision = 1;
    const saves = [];
    await page.setViewportSize({width,height:900});
    await page.addInitScript(({locale,width}) => {
      localStorage.setItem('uiLanguage',locale);
      localStorage.setItem('themeMode',width===390?'dark':'light');
    }, {locale,width});
    await mockAPI(page, response => {
      if (response.url.pathname === '/api/settings') {
        if (response.request.method() === 'PUT') {
          response.status = status;
          if (status === 200) {
            const data = response.request.postDataJSON();
            saves.push(data); mode=data.skip_cert_verify_mode; skip=data.skip_cert_verify; revision++;
          } else response.body={error:'revision conflict'};
        } else {
          response.body.skip_cert_verify_mode=mode;
          response.body.skip_cert_verify=skip;
        }
        response.headers={ETag:`"config-${revision}"`};
      }
      return response;
    });
    await page.goto('/');
    await page.locator('[data-tab="settings"]').click();
    const policy=page.locator('#settingSkipCertVerifyMode'), checkbox=page.locator('#settingSkipCertVerify');
    await expect(policy).toHaveValue('default');
    await expect.poll(()=>page.locator('#settingsTab form').evaluate(el=>el.inert)).toBe(false);
    await policy.selectOption('override');
    await expect(page.locator('#settingsSaveStatus')).toContainText(locale==='zh'?'未保存':'Unsaved');
    await expect(page.locator('#certVerifyModeHelp')).toContainText(locale==='zh'?'所有节点使用全局开关':'all nodes use the global checkbox');
    await policy.scrollIntoViewIfNeeded();
    expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
    await page.screenshot({path:testInfo.outputPath('certificate-policy.png')});
    await page.locator('[data-tab="dashboard"]').click();
    await expect(page.locator('#confirmDialog')).toBeVisible();
    await page.keyboard.press('Escape');
    status=412;
    await page.locator('#settingsTab button[type="submit"]').click();
    await expect(page.locator('#settingsSaveStatus')).toContainText(locale==='zh'?'草稿已保留':'draft is preserved');
    await expect(policy).toHaveValue('override');
    expect(saves).toHaveLength(0);
    status=200;
    await page.locator('#settingsTab button[type="submit"]').click();
    await expect(page.locator('#settingsSaveStatus')).toContainText(locale==='zh'?'配置已保存':'Configuration saved');
    expect(saves[0]).toMatchObject({skip_cert_verify_mode:'override',skip_cert_verify:false});
    await page.locator('#settingsTab').getByRole('button',{name:locale==='zh'?'重新载入设置':'Reload settings',exact:true}).click();
    await expect(page.locator('#settingsSaveStatus')).toHaveText('');
    await expect(policy).toHaveValue('override');
    await expect(checkbox).not.toBeChecked();
    await policy.selectOption('default');
    await checkbox.check();
    await page.locator('#settingsTab button[type="submit"]').click();
    await expect(page.locator('#settingsSaveStatus')).toContainText(locale==='zh'?'配置已保存':'Configuration saved');
    expect(saves[1]).toMatchObject({skip_cert_verify_mode:'default',skip_cert_verify:true});
  });
}

import { expect, test } from '@playwright/test';

const buildInfo = {
  product: 'ProxyFleet',
  version: 'v3.2.0-test',
  commit: '0123456789abcdef',
  built_at: '2026-07-28T12:00:00Z',
  go_version: 'go1.26.0',
  goos: 'windows',
  goarch: 'amd64',
  capabilities: { clash_api: true, grpc: true, gvisor: true, quic: true, utls: true, wireguard: true },
  protocols: ['http', 'hysteria2', 'socks5', 'tuic', 'vless', 'vmess', 'wireguard'],
  official_release_ready: true
};

async function mockAPI(page) {
  await page.route('**/api/**', async route => {
    const requestURL = new URL(route.request().url());
    const path = requestURL.pathname;
    if (path === '/api/traffic') {
      await route.abort();
      return;
    }
    let body;
    let headers = {};
    if (path === '/api/nodes') {
      body = {
        nodes: [],
        pagination: { page: 1, page_size: 50, total_items: 0, total_pages: 0 },
        summary: { total_nodes: 0, healthy_nodes: 0, active_connections: 0, unavailable_nodes: 0 },
        region_stats: {},
        region_healthy: {},
        top_latency_nodes: [],
        top_quality_nodes: [],
        probe_sweep: { active: false, done: 0, total: 0, available: 0, failed: 0 }
      };
    } else if (path === '/api/session') {
      body = { role: 'admin' };
    } else if (path === '/api/subscription/status') {
      body = { enabled: true, node_count: 14, is_refreshing: false, last_error: '', skipped_nodes: 0, node_failures: [], sources: [
        { name: 'provider-a', enabled: true, is_refreshing: false, using_fallback: false, node_count: 14, duration_ms: 245, last_success: '2026-07-28T12:00:00Z', next_refresh: '2026-07-28T13:00:00Z' }
      ] };
    } else if (path === '/api/build-info') {
      body = buildInfo;
    } else if (path === '/api/settings') {
      headers = { ETag: '"config-1"' };
      body = {
        mode: 'pool',
        external_ip: '',
        probe_target: 'www.apple.com:80',
        probe_concurrency: 32,
        probe_mode: 'adaptive',
        probe_interval: '5m0s',
        probe_timeout: '10s',
        probe_batch_size: 100,
        listener: { address: '127.0.0.1', port: 23230, username: 'fleet', password: 'secret-pass' },
        endpoints: [
          { name: 'public', enabled: true, address: '127.0.0.1', port: 23230, username: 'fleet', password: 'secret-pass', profile: '', status: 'running', message: '监听正常' },
          { name: 'hk-only', enabled: true, address: '127.0.0.1', port: 23231, username: 'hk', password: 'hk-secret', profile: 'hk-fast', status: 'running' }
        ],
        multi_port: {}, pool: {}, geoip: { enabled: false, listen: '', port: 0, auto_update_enabled: false, auto_update_interval: '0s' }, log: {},
        traffic_log: { enabled: true, file: 'traffic-log.db', retention: '24h0m0s', max_entries: 100000, redact_destination: true },
        profiles: [{ name: 'hk-fast', regions: ['hk'], name_regex: '', tag_rules: { any: ['HK|Hong Kong'], must: ['Premium'], must_not: ['Expired'] }, protocols: ['vless'], sources: [], min_quality: 80 }],
        management: {
          probe_healthy_interval: '30m0s',
          probe_failure_retry_interval: '1m0s',
          probe_failure_max_interval: '1h0m0s',
          probe_passive_grace: '10m0s',
          probe_max_per_hour: 600,
          probe_max_per_day: 5000
        }
      };
    } else if (path === '/api/access') {
      body = {
        mode: 'pool', host: '127.0.0.1', port: 23230,
        username: 'fleet', password: 'secret-pass',
        http_uri: 'http://fleet:secret-pass@127.0.0.1:23230',
        socks5_uri: 'socks5://fleet:secret-pass@127.0.0.1:23230',
        profiles: [{ name: 'hk-fast', regions: ['hk'], name_regex: '', protocols: ['vless'], sources: [], min_quality: 80 }],
        endpoints: [
          { name: 'public', enabled: true, host: '127.0.0.1', port: 23230, username: 'fleet', password: 'secret-pass', profile: '', status: 'running' },
          { name: 'hk-only', enabled: true, host: '127.0.0.1', port: 23231, username: 'hk', password: 'hk-secret', profile: 'hk-fast', status: 'running' }
        ]
      };
    } else if (path === '/api/subscription/config') {
      if (route.request().method() === 'PUT') await new Promise(resolve => setTimeout(resolve, 200));
      headers = { ETag: '"config-1"' };
      body = {
        subscriptions: ['https://example.com/sub?token=secret'], sources: [{ name: 'provider-a', url: 'https://example.com/sub?token=secret', enabled: true, refresh_interval: '30m0s', has_headers: false }], enabled: true, interval: '1h0m0s', fetch_concurrency: 16,
        allow_private_networks: false, max_removed_ratio: 0.5, min_available_ratio: 0,
        quarantine_new_nodes: true, node_failure_policy: 'skip'
      };
    } else if (path === '/api/subscription/preview') {
      await new Promise(resolve => setTimeout(resolve, 200));
      body = { token: 'preview-1', candidate_total: 15, added: 2, removed: 1, unchanged: 13, removed_ratio: 0.0625, risky: false };
    } else if (path === '/api/profiles/preview') {
      body = { total: 100, matched: 42, excluded: { tag_rule: 41, region: 17 }, matched_samples: [{ name: 'HK Premium 01', region: 'hk', protocol: 'vless', source: 'subscription', quality: 91, matched: true }], excluded_samples: [{ name: 'HK Expired', region: 'hk', protocol: 'vless', source: 'subscription', quality: 80, matched: false, reason: 'tag_rule', rule_group: 'must_not', rule_index: 0, rule: 'Expired' }] };
    } else if (path === '/api/subscription/sources/refresh') {
      body = { message: 'ok', node_count: 14, sources: [{ name: 'provider-a', enabled: true, is_refreshing: false, using_fallback: false, node_count: 14, duration_ms: 123, last_success: '2026-07-28T12:05:00Z', next_refresh: '2026-07-28T12:35:00Z' }] };
    } else if (path === '/api/traffic/logs') {
      body = { enabled: true, dropped: 0, events: [{ timestamp: '2026-07-28T12:05:00Z', node_id: 'node-a', profile: 'hk-fast', destination: '[sha256:0123456789abcdef]:443', network: 'tcp', connect_ms: 81, ttfb_ms: 123, duration_ms: 940, upload_bytes: 1024, download_bytes: 4096, attempt: 2, retried: true, success: true }] };
    } else if (path === '/api/traffic/logs/clear') {
      body = { message: 'cleared' };
    } else {
      body = {};
    }
    await route.fulfill({ status: 200, contentType: 'application/json', headers, body: JSON.stringify(body) });
  });
}

test.beforeEach(async ({ page }) => {
  await mockAPI(page);
});

test('keeps all three dashboard charts visible with an empty pool and no traffic', async ({ page }) => {
  const pageErrors = [];
  const consoleErrors = [];
  page.on('pageerror', error => pageErrors.push(error.message));
  page.on('console', message => {
    if (message.type() === 'error' && !message.text().includes('Failed to load resource')) consoleErrors.push(message.text());
  });
  await page.goto('/');

  const brandLogo = page.locator('.brand-logo');
  await expect(page).toHaveTitle(/^ProxyFleet - (监控中心|Control Center)$/);
  await expect(page.locator('meta[name="application-name"]')).toHaveAttribute('content', 'ProxyFleet');
  await expect(page.locator('link[rel="icon"]')).toHaveAttribute('href', '/assets/proxyfleet-logo.png?v=66dc1afe');
  await expect(brandLogo).toBeVisible();
  await expect(brandLogo).toHaveAttribute('src', '/assets/proxyfleet-logo.png?v=66dc1afe');
  await expect(page.locator('.brand-name')).toHaveText('ProxyFleet');
  await expect.poll(() => brandLogo.evaluate(image => image.complete && image.naturalWidth > 0)).toBe(true);
  await expect(page.locator('#dashboardTab')).toHaveClass(/active/);
  await expect.poll(() => page.evaluate(() => typeof window.echarts)).toBe('object');
  expect(consoleErrors).toEqual([]);
  for (const chartID of ['regionChart', 'latencyChart', 'trafficChart']) {
    const chart = page.locator(`#${chartID}`);
    await expect(chart).toBeVisible();
    await expect(chart.locator('canvas')).toHaveCount(1);
  }
  await expect(page.locator('#trafficPanel')).toBeVisible();
  expect(pageErrors).toEqual([]);
});

test('shows release capabilities and the new cost/safety controls', async ({ page }) => {
  await page.goto('/');
  await page.locator('.nav-item[data-tab="settings"]').click();

  await expect(page.locator('#buildInfoIdentity')).toHaveText('ProxyFleet v3.2.0-test');
  await expect(page.locator('#buildInfoStatus')).toHaveText('完整发行构建');
  await expect(page.locator('#buildInfoCapabilities')).toContainText('QUIC ON');
  await expect(page.locator('#settingProbeMaxPerDay')).toHaveValue('5000');
  await expect(page.locator('#settingSubNodeFailurePolicy')).toHaveValue('skip');
});


test('edits named profiles and generates profile-aware proxy commands', async ({ page }) => {
  await page.goto('/');
  await page.locator('.nav-item[data-tab="settings"]').click();

  const mount = page.locator('#profileSettingsMount');
  await expect(mount.getByText('Named Profiles')).toBeVisible();
  await expect(mount.locator('[data-profile-row]')).toHaveCount(1);
  await expect(mount.locator('[data-field="name"]')).toHaveValue('hk-fast');
  await expect(mount.locator('[data-field="tag_any"]')).toHaveValue('HK|Hong Kong');
  await expect(mount.locator('[data-field="tag_must"]')).toHaveValue('Premium');
  await expect(mount.locator('[data-field="tag_must_not"]')).toHaveValue('Expired');
  await expect(mount.locator('[data-profile-preview]')).toContainText('42');
  await expect(mount.locator('[data-access-status]')).toHaveText('就绪');

  await mount.locator('[data-access-profile]').selectOption('hk-fast');
  await expect(mount.locator('[data-access-uri]')).toHaveValue('http://fleet%40hk-fast:secret-pass@127.0.0.1:23230');
  await expect(mount.locator('[data-access-curl]')).toHaveValue(/curl --proxy/);

  await mount.locator('[data-add-profile]').click();
  await expect(mount.locator('[data-profile-row]')).toHaveCount(2);
});

test('manages named subscription sources with masked URLs, status, refresh, and delete confirmation', async ({ page }) => {
  await page.goto('/');
  await page.locator('.nav-item[data-tab="settings"]').click();

  const mount = page.locator('#subscriptionSourcesMount');
  await expect(mount.getByText('Subscription Sources')).toBeVisible();
  await expect(mount.locator('[data-subscription-source]')).toHaveCount(1);
  const source = mount.locator('[data-subscription-source]').first();
  await expect(source.locator('[data-field="url"]')).toHaveAttribute('type', 'password');
  await expect(source.locator('[data-field="refresh_interval"]')).toHaveValue('30m0s');
  await expect(source).toContainText('14');
  await source.locator('[data-toggle-source-url]').click();
  await expect(source.locator('[data-field="url"]')).toHaveAttribute('type', 'text');
  await source.locator('[data-refresh-source]').click();
  await expect(source).toContainText('123 ms');

  await source.locator('[data-remove-source]').click();
  await expect(page.locator('#confirmOverlay')).toHaveClass(/show/);
  await expect(page.locator('#confirmMessage')).toContainText('provider-a');
  await page.locator('#confirmAccept').click();
  await expect(mount.locator('[data-subscription-source]')).toHaveCount(0);
});

test('shows and clears structured traffic history and exposes safe settings', async ({ page }) => {
  await page.goto('/');
  await page.locator('.nav-item[data-tab="logs"]').click();
  await expect(page.locator('#trafficLogTableBody')).toContainText('node-a');
  await expect(page.locator('#trafficLogTableBody')).toContainText('hk-fast');
  await expect(page.locator('#trafficLogTableBody')).toContainText('retry');
  await expect(page.locator('#trafficLogSummary')).toContainText('丢弃 0 条');

  await page.locator('.nav-item[data-tab="settings"]').click();
  await expect(page.locator('#settingTrafficLogEnabled')).toBeChecked();
  await expect(page.locator('#settingTrafficLogRedact')).toBeChecked();
  await expect(page.locator('#settingTrafficLogMaxEntries')).toHaveValue('100000');
});

test('manages shared-pool endpoints with status, profile binding, and delete confirmation', async ({ page }) => {
  await page.goto('/');
  await page.locator('.nav-item[data-tab="settings"]').click();

  const mount = page.locator('#endpointSettingsMount');
  await expect(mount.getByText('Endpoint Manager')).toBeVisible();
  await expect(mount.locator('[data-endpoint-row]')).toHaveCount(2);
  await expect(mount.locator('[data-endpoint-row]').first().locator('[data-endpoint-status]')).toHaveText('运行中');
  await expect(mount.locator('[data-endpoint-row]').nth(1).locator('[data-field="profile"]')).toHaveValue('hk-fast');

  await mount.locator('[data-add-endpoint]').click();
  await expect(mount.locator('[data-endpoint-row]')).toHaveCount(3);
  const added = mount.locator('[data-endpoint-row]').last();
  await expect(added.locator('[data-endpoint-status]')).toHaveText('待保存');
  await added.locator('[data-remove-endpoint]').click();
  await expect(page.locator('#confirmOverlay')).toHaveClass(/show/);
  await expect(page.locator('#confirmMessage')).toContainText('endpoint-3');
  await page.locator('#confirmAccept').click();
  await expect(mount.locator('[data-endpoint-row]')).toHaveCount(2);

  const access = page.locator('#profileSettingsMount');
  await access.locator('[data-access-endpoint]').selectOption('hk-only');
  await expect(access.locator('[data-access-profile]')).toBeDisabled();
  await expect(access.locator('[data-access-profile]')).toHaveValue('hk-fast');
  await expect(access.locator('[data-access-uri]')).toHaveValue('http://hk:hk-secret@127.0.0.1:23231');

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(mount.locator('[data-add-endpoint]')).toBeVisible();
  await expect.poll(() => mount.locator('.endpoint-fields').first().evaluate(element => getComputedStyle(element).gridTemplateColumns.split(' ').length)).toBe(1);
});

test('localizes dynamic settings content completely in English', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('uiLanguage', 'en'));
  await page.goto('/');
  await page.locator('.nav-item[data-tab="settings"]').click();

  await expect(page).toHaveTitle('ProxyFleet - Control Center');
  await expect(page.locator('#endpointSettingsMount')).toContainText('Endpoints start only in pool / hybrid mode.');
  await expect(page.locator('#endpointSettingsMount')).toContainText('Listener is running normally');
  await expect(page.locator('#profileSettingsMount')).toContainText('Access Assistant');
  await expect(page.locator('#subscriptionSourcesMount')).toContainText('Source refresh interval');

  const chineseByTab = {};
  for (const tab of ['dashboard', 'manage', 'debug', 'operations', 'logs', 'settings']) {
    await page.locator(`.nav-item[data-tab="${tab}"]`).click();
    await page.waitForTimeout(100);
    const visibleChinese = await page.locator('body').evaluate(element => element.innerText
      .split(/\n+/)
      .map(value => value.trim())
      .filter(value => /[\u3400-\u9fff]/.test(value)));
    if (visibleChinese.length) chineseByTab[tab] = [...new Set(visibleChinese)];
  }
  expect(chineseByTab).toEqual({});
});

test('localizes the complete subscription save workflow in English', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('uiLanguage', 'en'));
  await page.goto('/');
  await page.locator('.nav-item[data-tab="settings"]').click();

  await page.locator('#subscriptionSourcesMount [data-field="url"]').fill('https://example.com/sub?token=updated');
  await page.locator('form').first().locator('button[type="submit"]').click();

  await expect(page.locator('#fullscreenLoading')).toBeVisible();
  await expect(page.locator('#fullscreenLoadingText')).toHaveText('Updating subscription...');
  await expect(page.locator('#fullscreenLoadingSubtext')).toHaveText('Fetching candidate subscriptions and calculating changes.');

  await expect(page.locator('#confirmOverlay')).toHaveClass(/show/);
  await expect(page.locator('#confirmMessage')).toContainText('The candidate pool has 15 nodes');
  await expect(page.locator('#confirmMessage')).not.toContainText(/[\u3400-\u9fff]/);
  await page.locator('#confirmAccept').click();

  await expect(page.locator('#fullscreenLoading')).toBeVisible();
  await expect(page.locator('#fullscreenLoadingSubtext')).toHaveText('Validating the candidate pool before switching atomically.');
  await expect(page.locator('#fullscreenLoading')).toBeHidden();
});

test('keeps the Endpoint mode note separated from the panel header', async ({ page }) => {
  await page.goto('/');
  await page.locator('.nav-item[data-tab="settings"]').click();

  const spacing = await page.locator('.endpoint-mode-note').evaluate(element => {
    const note = element.getBoundingClientRect();
    const header = element.previousElementSibling.getBoundingClientRect();
    return { gap: note.top - header.bottom, marginTop: getComputedStyle(element).marginTop };
  });
  expect(spacing.marginTop).toBe('16px');
  expect(spacing.gap).toBeGreaterThanOrEqual(16);
});

test('saves other settings when disabled GeoIP auto-update reports a zero interval', async ({ page }) => {
  await page.goto('/');
  await page.locator('.nav-item[data-tab="settings"]').click();

  await expect(page.locator('#settingGeoIPAutoUpdate')).not.toBeChecked();
  await expect(page.locator('#settingGeoIPUpdateInterval')).toHaveValue('24h');
  await page.locator('#settingExternalIP').fill('198.51.100.8');
  const requestPromise = page.waitForRequest(request => request.url().endsWith('/api/settings') && request.method() === 'PUT');
  await page.locator('#settingsSaveLabel').click();
  const request = await requestPromise;
  const payload = request.postDataJSON();
  expect(payload.geoip.auto_update_enabled).toBe(false);
  expect(payload.geoip.auto_update_interval).toBe('');
  await expect(page.locator('.toast').last()).toContainText(/保存|Saved/);
});

test('places probe progress details below the header actions', async ({ page }) => {
  await page.goto('/');
  await page.locator('#probeProgress').evaluate(element => element.classList.add('show'));

  const layout = await page.evaluate(() => {
    const progress = document.querySelector('.progress-float').getBoundingClientRect();
    const actions = document.querySelector('.header-actions').getBoundingClientRect();
    const header = document.querySelector('.header').getBoundingClientRect();
    const overlapsActions = !(progress.right <= actions.left || progress.left >= actions.right || progress.bottom <= actions.top || progress.top >= actions.bottom);
    return { progressTop: progress.top, headerBottom: header.bottom, overlapsActions };
  });
  expect(layout.progressTop).toBeGreaterThanOrEqual(layout.headerBottom + 8);
  expect(layout.overlapsActions).toBe(false);
});

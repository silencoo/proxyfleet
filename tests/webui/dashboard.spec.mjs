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
      body = { enabled: true, node_count: 0, is_refreshing: false, last_error: '', skipped_nodes: 0, node_failures: [] };
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
        listener: { host: '127.0.0.1', port: 23230, username: 'fleet', password: 'secret-pass' },
        multi_port: {}, pool: {}, geoip: {}, log: {},
        profiles: [{ name: 'hk-fast', regions: ['hk'], name_regex: '', protocols: ['vless'], sources: [], min_quality: 80 }],
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
        profiles: [{ name: 'hk-fast', regions: ['hk'], name_regex: '', protocols: ['vless'], sources: [], min_quality: 80 }]
      };
    } else if (path === '/api/subscription/config') {
      headers = { ETag: '"config-1"' };
      body = {
        subscriptions: [], enabled: false, interval: '1h0m0s', fetch_concurrency: 16,
        allow_private_networks: false, max_removed_ratio: 0.5, min_available_ratio: 0,
        quarantine_new_nodes: true, node_failure_policy: 'skip'
      };
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
  await expect(brandLogo).toBeVisible();
  await expect(page.locator('.brand')).toContainText('ProxyFleet');
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
  await expect(mount.locator('[data-access-status]')).toHaveText('就绪');

  await mount.locator('[data-access-profile]').selectOption('hk-fast');
  await expect(mount.locator('[data-access-uri]')).toHaveValue('http://fleet%40hk-fast:secret-pass@127.0.0.1:23230');
  await expect(mount.locator('[data-access-curl]')).toHaveValue(/curl --proxy/);

  await mount.locator('[data-add-profile]').click();
  await expect(mount.locator('[data-profile-row]')).toHaveCount(2);
});

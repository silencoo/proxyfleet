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

export async function mockAPI(page, transform = response => response) {
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
    } else if (path === '/api/settings' && route.request().method() === 'PUT') {
      headers = { ETag: '"config-2"' };
      body = { message: '设置已保存并生效', need_reload: false, need_restart: false, auth_changed: false };
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
    const result = await transform({request:route.request(), url:requestURL, body, headers, status:200});
    await route.fulfill({ status: result.status, contentType: 'application/json', headers:result.headers, body: JSON.stringify(result.body) });
  });
}

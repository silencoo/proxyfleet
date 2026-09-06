<p align="center">
  <img src="webui/public/assets/proxyfleet-logo.png" alt="ProxyFleet dithering snowman logo" width="180" />
</p>

<h1 align="center">ProxyFleet</h1>

[简体中文](README_ZH.md) | English

> A production-oriented sing-box proxy pool for crawlers and automation: one
> rotating endpoint, one stable port per node, or both at the same time.

This repository is an actively developed fork of
[the original upstream repository](https://github.com/jasonwong1991/easy_proxies).
It keeps the upstream protocol foundation, but its runtime lifecycle, state
persistence, WebUI, and first-run experience have diverged substantially.
Upstream changes are reviewed and selectively ported instead of blindly merged.

## Why This Fork

| Area | Enhancement in this fork |
|------|--------------------------|
| First run | A native binary can start without `config.yaml`; it creates a safe loopback-only default, prints a cryptographically random administrator password once, and opens a zero-node WebUI |
| Proxy access | `pool`, `multi-port`, and `hybrid` modes support rotating traffic and deterministic direct access to individual nodes |
| Live updates | Node-level diffs keep unchanged listeners and connections alive; removed outbounds drain instead of being cut off immediately |
| Stable identity | Subscription reorder/rename does not change dedicated ports; port mappings and credentials remain strong YAML configuration while weak health/latency state survives restarts in SQLite |
| Subscription safety | Bounded concurrent fetching, private-network protection, per-source fallback, deduplication, candidate health checks, atomic persistence, and rollback |
| WebUI | Vite/TypeScript modules compiled into the single executable, with a bilingual monochrome UI, Profiles, access-command generation, diagnostics, logs, and masked secrets |
| Operations | Serialized health sweeps, probe deadlines, transient cooldown, retry/session-affinity controls, log rotation, and transactional config writes |
| Region insight | Exit-IP GeoIP routing plus name-based JP/KR/US/HK/TW/SG/residential fallback grouping for dashboard visibility |

## Runtime Modes

| Mode | Best for | Entry points |
|------|----------|--------------|
| `pool` | Crawlers that want automatic node rotation | One mixed HTTP/SOCKS5 port |
| `multi-port` | Jobs that must pin traffic to a specific node | One stable mixed port per node |
| `hybrid` | Using rotation and node pinning together | Pool port plus all dedicated node ports |

## Quick Start

### Native Zero-Config Start (Recommended)

Build the full-protocol binary:

```bash
go build -trimpath -tags "with_utls with_quic with_grpc with_wireguard with_gvisor with_clash_api" -o proxyfleet ./cmd/proxyfleet
./proxyfleet
```

Windows:

```powershell
go build -trimpath -tags "with_utls with_quic with_grpc with_wireguard with_gvisor with_clash_api" -o proxyfleet.exe ./cmd/proxyfleet
.\proxyfleet.exe
```

On the first launch, ProxyFleet:

1. Creates `config.yaml` in the current working directory if it is missing.
2. Generates a 32-character cryptographically random administrator password, stores it in the new file, and prints it **once** with a change-password warning.
3. Prints the absolute path of the config it is using.
4. Starts the embedded WebUI at `http://127.0.0.1:9091` in management-only mode.
5. Starts the proxy runtime automatically after a subscription refresh or node edit produces usable nodes.

In the WebUI, open **System Settings**, add a subscription, save and refresh it.
The generated pool listener is available at `127.0.0.1:2323` after nodes pass
validation:

```bash
curl -x http://127.0.0.1:2323 https://api.ipify.org
```

The full tag set is important for real-world Clash subscriptions. Hysteria,
Hysteria2, and TUIC require `with_quic`; a plain untagged build can parse those
nodes but cannot start them.

### Start with an Existing Config

```bash
./proxyfleet -config /path/to/config.yaml
```

The `-config` flag is optional. When omitted, the program uses
`config.yaml` in the current working directory.

### Docker from This Fork

The Compose setup builds the image from the current checkout so it cannot
silently run the original upstream image:

```bash
./start.sh
```

Or prepare the bind-mounted files and build manually:

```bash
cp config.example.yaml config.yaml
touch nodes.txt
docker compose up -d --build
```

Open `http://127.0.0.1:9091` after startup.

## Configuration

### Runtime Modes

| Mode | Description |
|------|-------------|
| `pool` | Single port proxy pool. All nodes share one port with load balancing |
| `multi-port` | One local port per node for direct access |
| `hybrid` | Both pool + multi-port simultaneously |

Dedicated ports keep their assignments in `port-map.yaml`. Per-node listener credentials edited through the WebUI are stored separately in `node-auth.yaml` (mode `0600`) and are automatically reapplied to matching `nodes_file` or subscription nodes by stable node identity.

Pool failure streaks, blacklist/cooldown deadlines, monitor counters, and bounded per-domain passive latency EWMAs are coalesced into `runtime-state.db` next to `config.yaml`. SQLite uses WAL and transactional dirty-node batches: normal traffic updates do not rewrite untouched nodes or their domain caches, and failed writes retry the newest in-memory values. An existing `health-state.yaml` is imported once when the database is empty. Active connection counts remain process-local. Set `pool.runtime_state_file` to move the database.

Retired nodes release their live callbacks and shared runtime references once their last pool owner and client connection have drained. Weak history for absent nodes is retained for up to seven days, capped at 4,096 unprotected retired nodes; current nodes and unexpired bans/cooldowns are excluded from eviction. Cleanup runs after committed configuration changes and does not close live connections.

Node-level refreshes preserve observed health for unchanged live outbounds. A successful candidate validation transfers its probe results after commit, including routing cooldown/recovery state, without counting probes as client traffic or spending another adaptive-probe batch to rediscover those results. Rejected candidates never publish their health results. Replaced transports and changed probe targets still require fresh evidence; manual blacklists remain authoritative.

The dashboard's healthy percentage is healthy nodes divided by **all** nodes, with unknown nodes shown separately. Healthy, unavailable (including blocked/cooling), and unknown counts partition the inventory. Diagnostics shows a separate **recorded success rate** from retained counters, not current node availability; legacy counters may include probes from older builds. An empty diagnostic history has no success rate, rather than a 0% failure claim. Subscription candidate validation is a separate safety check from the scheduled adaptive-probe budget.

### Pool Scheduling

| Algorithm | Description |
|-----------|-------------|
| `sequential` | Round-robin through healthy nodes |
| `random` | Random node selection |
| `balance` | O(1) power-of-two-choices balancing using active connection counts |
| `latency` | Samples a bounded set, then balances connections among nodes inside the configured latency tolerance |

Transient network failures use a short cooldown instead of immediately increasing the long-term blacklist streak. Real traffic confirms transport health only after receiving data (or a valid UDP datagram), not merely after opening a socket or sending a request. Early I/O failures are recorded once; unused connections, local cancellation/close, and normal EOF after data do not create false failures. Traffic logs leave unused connections `unconfirmed`. This is transport evidence, not a claim that an opaque HTTPS application request returned HTTP 200. New target-domain latency samples measure time from dial start to the first response, without extra requests; latency scheduling prefers this bounded EWMA and falls back to the global probe result. The unified pool can retry a different node after a failed dial and can optionally keep bounded, expiring session affinity; it never replays application data after an I/O failure. Dedicated per-node ports never retry through another node.

### Named Pool Profiles

Profiles precompute filtered views by region, protocol, source, composable node-name rules, and minimum quality. `tag_rules.any` requires at least one match, `must` requires every match, and `must_not` excludes any match. The legacy `name_regex` remains an additional `must` rule. A managed Endpoint can bind a Profile directly; authenticated shared endpoints can also use username `base@profile`. The WebUI shows live match counts, exclusion reasons, and safe samples before saving.

```yaml
profiles:
  - name: hk-fast
    regions: [hk]
    protocols: [vless, hysteria2]
    sources: [subscription]
    tag_rules:
      any: ["(?i)HK|Hong Kong", "(?i)香港"]
      must: ["(?i)premium|dedicated"]
      must_not: ["(?i)expired|traffic left"]
    min_quality: 80
```

### Structured Traffic History

Connection history is optional and disabled by default. When enabled, TCP/UDP outcomes, selected node/Profile, retries, connect time, TTFB, duration, and byte counts are written asynchronously to a separate WAL-mode `traffic-log.db`. A bounded queue ensures logging cannot block proxy traffic. Destination addresses are hashed by default, and retention plus row count are both bounded.

```yaml
traffic_log:
  enabled: false
  file: traffic-log.db
  retention: 24h
  max_entries: 100000
  redact_destination: true
```

### Minimal Config Example

```yaml
mode: pool

endpoints:
  - name: default
    enabled: true
    address: 127.0.0.1
    port: 2323
    username: user
    password: pass

pool:
  mode: sequential    # sequential / random / balance / latency
  failure_threshold: 3
  blacklist_duration: 24h

management:
  enabled: true
  listen: 127.0.0.1:9091
  probe_target: http://cp.cloudflare.com/generate_204
  probe_concurrency: 32  # process-wide batch probe workers (1-1024)
  password: ""
  # Required together with a strong password for any non-loopback listen address:
  # tls_cert_file: ./certs/management.crt
  # tls_key_file: ./certs/management.key

dns:
  server: 223.5.5.5
  port: 53
  strategy: prefer_ipv4

nodes_file: nodes.txt
```

### Full Config Reference

See [config.example.yaml](config.example.yaml) for the full documented configuration with all available options.

## GeoIP Region Routing

### Overview

When GeoIP is enabled, ProxyFleet automatically classifies your proxy nodes by geographic region and provides a separate HTTP proxy endpoint that lets you route traffic through nodes in a specific country/region.

### Supported Regions

| Code | Region |
|------|--------|
| `jp` | Japan 🇯🇵 |
| `kr` | South Korea 🇰🇷 |
| `us` | United States 🇺🇸 |
| `hk` | Hong Kong 🇭🇰 |
| `tw` | Taiwan 🇹🇼 |
| `sg` | Singapore 🇸🇬 |
| `other` | All other regions |

### Configuration

```yaml
geoip:
  enabled: true
  database_path: "./GeoLite2-Country.mmdb"
  listen: "0.0.0.0"          # defaults to listener.address if omitted
  port: 1221                  # defaults to listener.port if omitted
  exit_ip_url: "https://api.ipify.org" # requested through every node
  exit_ip_timeout: 10s
  exit_ip_concurrency: 16
  auto_update_enabled: true   # auto-update the GeoIP database
  auto_update_interval: 24h   # check interval
```

The GeoIP router reuses the `listener.username` and `listener.password` for proxy authentication.

Key behaviors:
- The GeoIP database (MaxMind GeoLite2-Country) is **auto-downloaded** on first startup
- When auto-update is enabled, the MMDB is checked every 24h by default and hot-reloaded without restarting listeners
- Node region classification uses the public exit IP observed by requesting `exit_ip_url` through that exact outbound; it does not use the subscription server address
- Classification runs at startup and after node reloads. A transient probe failure keeps the node's last observed exit IP when available; otherwise it is placed in `other`
- After an MMDB update, saved exit IPs are immediately reclassified and the region pools/router are replaced in place; this does not repeat the external exit-IP probes or interrupt existing proxy connections

### How to Use

The GeoIP router is an HTTP proxy that listens on its own port. Region selection uses standard proxy credentials, so it works for both normal HTTP requests and HTTPS `CONNECT` tunnels without changing the destination URL.

When proxy authentication is configured, append `@<region>` to the proxy username (URL-encode the `@` as `%40`). Without authentication, use the region code as the proxy username. The normal configured username selects the global pool.

```bash
# Route through Japanese nodes
curl -x http://user%40jp:pass@localhost:1221 http://example.com

# Route through US nodes
curl -x http://user%40us:pass@localhost:1221 http://example.com

# Route through Hong Kong nodes
curl -x http://user%40hk:pass@localhost:1221 http://example.com

# Route through Singapore nodes
curl -x http://user%40sg:pass@localhost:1221 http://example.com

# Normal configured credentials = global pool (all nodes)
curl -x http://user:pass@localhost:1221 http://example.com
```

#### HTTPS Requests (CONNECT Tunnel)

```bash
# Route HTTPS through Japanese nodes
https_proxy=http://user%40jp:pass@localhost:1221 curl https://www.google.com

# Route HTTPS through US nodes
https_proxy=http://user%40us:pass@localhost:1221 curl https://www.google.com

# Normal configured credentials = global pool
https_proxy=http://user:pass@localhost:1221 curl https://www.google.com
```

#### Using with Applications

**Environment variables:**

```bash
# Use Japanese nodes for all traffic
export http_proxy=http://user%40jp:pass@your-server:1221
export https_proxy=http://user%40jp:pass@your-server:1221

# Use global pool (all nodes)
export http_proxy=http://user:pass@your-server:1221
export https_proxy=http://user:pass@your-server:1221
```

**Browser proxy extensions (SwitchyOmega, FoxyProxy, etc.):**

- Protocol: HTTP
- Server: your-server-ip
- Port: 1221
- Username/Password: use `<configured username>@<region>` with the configured password; use the unmodified username for the global pool

**Python requests:**

```python
import requests

proxies = {
    "http": "http://user%40jp:pass@your-server:1221",
    "https": "http://user%40jp:pass@your-server:1221",
}
r = requests.get("http://example.com", proxies=proxies)
```

**Go net/http:**

```go
proxyURL, _ := url.Parse("http://user%40jp:pass@your-server:1221")
client := &http.Client{
    Transport: &http.Transport{
        Proxy: http.ProxyURL(proxyURL),
    },
}
resp, err := client.Get("http://example.com")
```

### How It Works

1. After outbounds start, each node requests the configured IP-echo endpoint through that exact proxy
2. The observed public exit IP is looked up in MaxMind and nodes are grouped into per-region pools (`pool-jp`, `pool-kr`, `pool-us`, etc.)
3. The GeoIP router listens on its own port and reads the optional region suffix from the proxy username
4. Matching requests are routed through the corresponding region pool; unmatched requests use the global pool
5. Each region pool uses the same scheduling algorithm configured in the `pool` section
6. The last successfully observed exit IP is retained across a transient probe failure within the running process

## Supported Protocols

| Protocol | URI Schemes | Transport |
|----------|-------------|-----------|
| VLESS | `vless://` | TCP, WS, HTTP/2, gRPC, HTTPUpgrade; TLS/Reality/uTLS |
| VMess | `vmess://` | WS, HTTP/2, gRPC, HTTPUpgrade; TLS/uTLS |
| Trojan | `trojan://` | WS, HTTP/2, gRPC, HTTPUpgrade; TLS/Reality/uTLS |
| Shadowsocks | `ss://`, `shadowsocks://` | SIP002, legacy whole-payload Base64, plaintext-compatible forms; external plugins are rejected |
| ShadowsocksR | `ssr://`, `shadowsocksr://` | Protocol/obfuscation parameters and Unicode metadata |
| Hysteria | `hysteria://` | QUIC; auth, bandwidth, obfuscation, TLS/SNI, ALPN, windows and MTU |
| Hysteria2 | `hysteria2://`, `hy2://` | QUIC-based |
| TUIC | `tuic://` | QUIC-based |
| AnyTLS | `anytls://` | TLS |
| SOCKS5 | `socks5://`, `socks5h://`, `socks://` | Direct; `socks5h` is accepted for subscription compatibility |
| HTTP | `http://`, `https://` | Direct |

## Node Sources

### Inline Nodes

```yaml
nodes:
  - uri: "vless://uuid@server:443?security=tls&type=ws&path=/path#Name"
```

### Nodes File

```yaml
nodes_file: nodes.txt
```

One proxy URI per line. Lines starting with `#` are comments.

### Subscriptions

```yaml
subscriptions:
  - name: provider-a
    url: "https://provider.example/api?token=xxx"
    enabled: true
    refresh_interval: 30m # omit to inherit the global interval
  - name: provider-b
    url: "https://backup.example/api?token=yyy"
    enabled: false

subscription_refresh:
  enabled: true
  interval: 1h
  fetch_concurrency: 16 # default 16, capped at 32
  max_removed_ratio: 0.5 # require explicit confirmation above 50% removal
  min_available_ratio: 0 # optional candidate availability ratio; combined with min_available_nodes
  quarantine_new_nodes: true # preflight candidates before the atomic cutover
  node_failure_policy: skip # isolate individual unsupported/broken nodes; use strict to reject the batch
  allow_private_networks: false # opt in only for trusted private subscription services
```

Supports Base64, plain text, and Clash YAML formats. Each named provider can be enabled, scheduled, inspected, and refreshed independently; the historical string-array format is still accepted and is migrated on the next WebUI save. Optional per-source request headers are supported, but authority-bearing `Host`/`Authorization` headers are rejected. Subscription URLs are fetched with bounded concurrency, responses are strictly limited to 10 MB, and URL credentials/query data are redacted from errors and logs. Loopback, private, link-local, and metadata destinations (including redirects) are blocked by default; set `allow_private_networks: true` only when a trusted subscription service is intentionally hosted on such a network. Duplicate URLs and nodes are removed by stable identity. Runtime refreshes cache each source independently so one failed provider can reuse only its own last known-good nodes; after a restart, `nodes_file` is the conservative aggregate fallback until every provider has refreshed successfully. Inline and WebUI-added nodes remain explicit configuration and are never overwritten by a subscription refresh. With the default `node_failure_policy: skip`, nodes requiring unavailable build capabilities or failing candidate construction are isolated by stable hash while the remaining pool commits; `strict` preserves all-or-nothing behavior.

When subscriptions are configured, fetched nodes are written to `nodes_file`. A refresh is committed as one transaction across configuration, cache files, and runtime state. The WebUI first fetches a candidate and displays stable-identity added/removed/unchanged counts; risky removal ratios require a second explicit confirmation and a short-lived one-time preview token. Candidate nodes are built and health-checked before cutover; a failed fetch, strict-mode unsupported node, availability ratio violation, persistence error, or stale configuration revision rolls back without replacing the active pool. Unchanged nodes and listeners retain their connections, removed outbounds drain for the configured timeout, and dedicated ports are restored from `port-map.yaml`.

Clash Shadowsocks conversion preserves plugin requirements so unsupported external plugins are rejected during candidate construction. Plugin-required nodes are never silently converted into plain Shadowsocks; the configured skip/strict failure policy still applies.

For large pools, `management.probe_mode: adaptive` supports both `probe_max_per_hour` (default 600) and `probe_max_per_day` (default 5000). One batch can run immediately; further capacity refills gradually at the stricter hourly/daily rate. Idle credit is capped at one batch, and hourly/day boundaries do not grant another burst. Both fixed-window limits remain hard caps. `/api/probe/status` distinguishes capacity usable now (`available_now`) from the remaining hourly/daily totals. Due new nodes receive two scheduling shares, recovery checks one, and healthy rechecks one; unused shares go to the other groups. Large inventories can therefore take longer to validate fully instead of exhausting the budget early. Recent successful real traffic suppresses redundant checks only while the node remains healthy and no newer failure or runtime replacement invalidates that evidence. Explicit manual probes remain outside the automatic budget.

HTTP probes use the configured URL path and query (or `/` for a bare `host:port`), validate bounded response headers, and require a 2xx/3xx response. They neither follow redirects nor download bodies. Target HTTP errors trigger temporary cooldown rather than a permanent proxy-protocol blacklist. Live health checks and subscription preflight use the same validation. New active probes do not increment real-traffic success/failure counters or update passive-traffic timestamps; timeline entries identify their `source` as `probe` or `traffic`, and probe latency is applied once. Existing persisted counters are retained, not retroactively reclassified.

Manual node/bulk probes honor `management.probe_timeout`, and HTTP validation honors its caller's deadline, including a longer subscription preflight timeout. Routing penalties/recovery and monitor results share one generation/freshness check: obsolete callbacks cannot blacklist nodes or release newer failures. A short-lived caller timing out does not publish a shared probe failure; the probe's own configured deadline does, once, even if a transport ignores cancellation.

Timeouts, connection refusal, EOF, broken pipes, and closed connections use transient cooldown rather than long protocol-failure bans. This includes remote `i/o timeout` and `context deadline exceeded` errors that a proxy forwards as plain text. A transient error does not restart an existing automatic blacklist's deadline or add a permanent-failure strike, and administrator bans retain their original deadline. A separate newer cooldown can outlast an automatic ban; expiry restores retry eligibility, not confirmed health. Failure logs report effective expiry times. These rules apply to new outcomes; existing saved bans and historical errors are not erased or retroactively rewritten.

HTTP failure diagnostics distinguish a health-check target's unsuccessful response from a WebSocket handshake rejected by the proxy server or CDN. Known status codes include their reason, for example `HTTP 503 (Service Unavailable)`. WebSocket 429/503 failures retain transient cooldown; this does not turn authentication or transport configuration errors into successful health checks.

The dashboard console retains the latest 64 KiB in a fixed-memory circular buffer. At normal log levels, startup lists at most 20 nodes per section and reports how many were omitted; `log_level: debug` or `trace` restores the full inventory. For logs that survive restarts, use `log.output: file` with the existing size/age/backup rotation settings. File output also keeps stdout and the dashboard console; changing the logging sink requires a restart.

AnyTLS links do not need `security=tls` to preserve their `sni`, `alpn`, or `fp` handshake settings. For implicit TLS (missing `security` or `security=none`), the existing global verification policy is retained; previously ignored per-link bypass flags are not newly enabled by this compatibility fix. Certificate diagnostics distinguish hostname/SAN problems, expired certificates, and untrusted issuers. Correcting SNI cannot repair an invalid provider certificate.

When all management role passwords are empty, `management.listen` must use a loopback address. Optional `operator_password` and `viewer_password` provide scoped operational and read-only access; the primary `password` remains the administrator credential. A non-loopback management listener requires a strong administrator password and native TLS certificate/key files; the service refuses an insecure remote-management configuration. Unsafe management requests are recorded in a bounded JSONL audit trail without credentials or query strings.

## WebUI Dashboard

Access at `http://your-server:9091` (configurable via the `management` section).

Features:

- **Dashboard**: Real-time node status, traffic charts, region/resident availability, composite 0–100 quality scores, and latency monitoring
- **Large node sets**: Server-side search, filters, sorting, and configurable 25/50/100/200-row pagination, so the browser never downloads the entire pool
- **Adaptive probes**: DIY healthy/retry/backoff/passive-grace intervals, batch concurrency, and a visible hourly traffic/performance budget
- **Operations**: Bounded metric history, availability alerts, probe budget status, and administrator-only mutation audit
- **Quality routing**: `pool.mode: quality` selects the best composite health/latency/stability score from a bounded sample
- **Node Config**: Add/edit/delete inline nodes and copy full URIs without exposing credentials in list responses
- **Subscription Sources**: Named per-provider enable, interval, status, fallback visibility, masked URL, and independent refresh controls
- **Subscription safety**: Candidate diff preview, risky-removal confirmation, availability-ratio preflight, and atomic cutover
- **Diagnostics / Console**: Searchable diagnostics and clearable in-memory logs; disk logs support scheduled rotation and compression
- **Traffic history**: Optional separate SQLite history with connect/TTFB/duration/bytes/retry fields, filtering, bounded retention, redaction, and administrator clear
- **Settings**: Chinese/English switcher, system theme, masked secrets, Named Profiles, copyable proxy access commands, viewer/operator/admin passwords, and persistent configuration editing

When all management role passwords are empty, loopback requests run as administrator without a login.

## Management API

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/auth` | POST | Login with password |
| `/api/settings` | GET, PUT | Read/update settings and Named Profiles |
| `/api/access` | GET | Administrator-only listener credentials, Profiles, and copyable HTTP/SOCKS5 URIs |
| `/api/nodes` | GET | Paginated/filterable node status and pool summary |
| `/api/nodes/{tag}/probe` | POST | Test node connectivity |
| `/api/nodes/{tag}/blacklist` | POST | Manually blacklist a node |
| `/api/nodes/{tag}/release` | POST | Release node from blacklist |
| `/api/nodes/probe-all` | POST | Probe all nodes (SSE stream) |
| `/api/export` | GET | Export node configuration |
| `/api/subscription/preview` | POST | Fetch candidate and return a safe added/removed diff token |
| `/api/subscription/config` | GET, PUT | Read or apply previewed subscription settings |
| `/api/probe/status` | GET | Adaptive probe budget and sweep progress |
| `/api/metrics/history` | GET | Bounded aggregate metric history |
| `/api/alerts` | GET | Availability alerts |
| `/api/audit` | GET | Administrator-only mutation audit |
| `/api/subscription/status` | GET | Check subscription status |
| `/api/subscription/refresh` | POST | Trigger manual refresh |
| `/api/subscription/sources/refresh` | POST | Refresh one named source and compose it with other providers' caches |
| `/api/traffic/logs` | GET | Query bounded structured connection history |
| `/api/traffic/logs/clear` | DELETE | Administrator-only traffic history clear |
| `/api/nodes/config` | GET, POST, PUT, DELETE | CRUD for node config |
| `/api/reload` | POST | Reload sing-box instance |

## Docker Deployment

### docker-compose.yml

The default setup uses host networking (recommended for automatic port management). Volumes mount `config.yaml` and `nodes.txt`:

```yaml
services:
  proxyfleet:
    build:
      context: .
    image: proxyfleet:local
    container_name: proxyfleet
    restart: unless-stopped
    network_mode: host
    volumes:
      - ./config.yaml:/etc/proxyfleet/config.yaml
      - ./nodes.txt:/etc/proxyfleet/nodes.txt
      - ./logs:/app/logs
```

### Important Notes

- **Builds this fork**: the default Compose file builds the current checkout instead of pulling the original upstream image.
- **Create config files first**: `config.yaml` and `nodes.txt` must exist as files before running `docker compose up`. Use `./start.sh` which handles this automatically.
- **Permissions**: Files must be writable by the container user for WebUI settings to persist. Prefer correct ownership with `0600`/`0640` permissions; avoid world-writable configuration files.
- **Multi-platform**: Supports amd64 and arm64 architectures.
- **Reload**: node/subscription changes use a node-level diff. Unchanged listeners and active connections stay up; new candidates are health-checked before cutover when `min_available_nodes` is configured, and removed outbounds drain for `drain_timeout`. Immutable global listener/log changes still require a short validated full-instance handoff.

### Ports

| Port | Usage |
|------|-------|
| 2323 | Pool proxy entry (pool/hybrid mode) |
| 9091 | WebUI and Management API |
| 1221 | GeoIP region router (when enabled, configurable) |
| 24000+ | Multi-port mode (one per node) |

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for version history.

## Development

```bash
npm ci
npm run check:webui
npm run build:webui
npm run test:e2e
go test ./...
go vet ./...

# webui/dist is embedded into the executable; CI verifies it matches the TypeScript/CSS source.
# Verify the production/full-protocol build
go build -trimpath -tags "with_utls with_quic with_grpc with_wireguard with_gvisor with_clash_api" -o proxyfleet ./cmd/proxyfleet
```

## License

MIT License. Dependency attributions are in [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md) and are also compiled into the executable: `proxyfleet -third-party-notices`.

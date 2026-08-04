# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [3.3.0] - 2026-08-04

### Changed
- Made adaptive probing the default for omitted `management.probe_mode` values, with bounded hourly and daily active-probe budgets

## [3.2.1] - 2026-08-03

### Fixed
- Made the traffic-log relative-path test platform-independent so the official Linux release runner validates the Windows binary build correctly

## [3.2.0] - 2026-08-03

### Added
- **Endpoint Manager**: Multiple named HTTP/SOCKS5 pool listeners with per-endpoint enablement, Profile binding, live status, and validated reloads
- **Composable Profile rules**: `ANY`, `MUST`, and `MUST_NOT` tag filters with match previews and rule-level diagnostics
- **Named subscription sources**: Independent enablement, refresh intervals, status, fallback reporting, masked URLs, and per-source refresh controls
- **Structured traffic history**: Optional asynchronous SQLite traffic log with connection latency, TTFB, transfer totals, retries, retention, and destination redaction
- **Secure first run**: Fresh starts generate a cryptographically random administrator password, persist it with the new loopback-only config, and print it once with a change warning
- **Named pool Profiles**: Precomputed region/protocol/source/name views with dynamic quality thresholds, selected through `base@profile` authentication
- **Access Assistant**: Administrator-only HTTP/SOCKS5 URI and curl generation with one-click copy controls
- **SQLite runtime state**: WAL-backed transactional health, blacklist/cooldown, monitor, and bounded target-latency persistence with one-time legacy YAML migration
- **Target-aware passive latency**: Successful real traffic feeds per-domain EWMA scheduling without additional probe traffic
- **Modular WebUI build**: Vite/TypeScript source modules and reproducible production assets embedded into the single executable
- **Log Rotation**: Configurable log file rotation with size limits, backup count, and compression
  - New `log` section in config with `output`, `file`, `max_size`, `max_backups`, `max_age`, `compress` options
  - Uses lumberjack for automatic log rotation
  - Defaults: 50MB max size, 3 backups, 7 days retention
- **WebUI Console**: Real-time log streaming in the dashboard
  - In-memory ring buffer captures last 1000 log lines
  - WebSocket-based live log streaming to browser
  - Console tab in WebUI for instant log viewing
- **AnyTLS Protocol**: Support for AnyTLS outbound protocol
  - Parse `anytls://` URIs from subscriptions
  - Full TLS configuration support
- **TUIC Protocol**: Support for TUIC outbound protocol
  - Parse `tuic://` URIs with UUID and password authentication
  - Congestion control and UDP relay mode configuration
  - Full TLS/ALPN support
- **Clash API Integration**: Embedded Clash API controller
  - Internal controller at `127.0.0.1:9092`
  - Enables Clash-compatible tooling integration

### Changed
- **Complete ProxyFleet identity migration**: Go module, command path, Docker service/image/paths, executable, Release asset, documentation, and local defaults now consistently use ProxyFleet
- **WebUI branding**: Snowman favicon and sidebar mark share one cache-versioned asset, with ProxyFleet application metadata in the embedded UI
- **Subscription Parsing**: Improved Clash YAML format detection
  - User-Agent changed to `clash-verge/v2.2.3` for better compatibility
  - YAML detection sample size increased from 200 to 16384 characters
  - Better support for modern proxy types (AnyTLS, TUIC) in Clash format
- **Docker Entrypoint**: Fixed bind-mount permission issues
  - Uses gosu for privilege dropping
  - Ensures proper file ownership for nodes.txt and logs

### Fixed
- Stale local executables serving the former Easy Proxies title and icon
- Mixed shell-script line endings that prevented the Docker entrypoint from parsing on Linux
- Docker nodes.txt permission denied on bind-mount
- VMess node name extraction from base64 payload
- Cross-platform file locking for Windows support

## [2.0.0] - 2025-01-XX

### Added
- SOCKS5 inbound protocol support via Mixed type
- Cross-platform file locking for Windows support
- GeoIP database auto-download and hot-reload
- Hysteria2 (hy2://) protocol support
- Comprehensive security and performance improvements

### Changed
- Major protocol, performance and UI overhaul
- Improved subscription parsing with better error handling
- Enhanced dashboard with real-time statistics

## [1.1.0] - 2024-12-XX

### Added
- GeoIP region routing and dashboard statistics
- Global skip_cert_verify option
- Node port assignment persistence across reloads
- ARM64 support for Docker image

### Fixed
- Hybrid mode export credentials
- Settings save permission issues
- Health check timing after node registration

## [1.0.0] - 2024-11-XX

### Added
- Initial release
- Pool, multi-port, and hybrid runtime modes
- Support for vmess, vless, trojan, ss, hysteria2, socks5, http protocols
- Subscription support (Base64/plain text/Clash YAML)
- Web dashboard with node management
- Automatic health checks and blacklist recovery
- Configurable DNS resolver

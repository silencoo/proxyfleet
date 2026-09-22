<p align="center">
  <img src="webui/public/assets/proxyfleet-logo.png" alt="ProxyFleet 黑白抖动雪人 Logo" width="180" />
</p>

<h1 align="center">ProxyFleet</h1>

[English](README.md) | 简体中文

> 面向爬虫与自动化任务的生产级 sing-box 代理池：既能提供统一轮换
> 入口，也能为每个节点保留稳定的独立端口。

本仓库基于
[原始上游仓库](https://github.com/jasonwong1991/easy_proxies)
持续开发。它保留了上游的协议基础，但运行时生命周期、状态持久化、
WebUI 和首次启动体验已经明显分化。上游更新会经过评估后选择性移植，
不会直接覆盖本 fork 的实现。

## 为什么选择这个 Fork

| 方向 | 本 Fork 的增强 |
|------|----------------|
| 首次启动 | 原生二进制无需提前准备 `config.yaml`；自动生成仅监听本机的安全默认配置，并仅一次输出密码学随机管理员密码和修改提醒 |
| 代理入口 | `pool`、`multi-port`、`hybrid` 三种模式，可同时满足自动轮换和指定节点出口 |
| 在线更新 | 节点级 diff 保持未变化监听器和现有连接，删除的出站会先排空，不会在每次订阅刷新时整体中断 |
| 稳定身份 | 订阅改名、重排和重启不会改变节点独立端口；强配置保留在 YAML，健康、黑名单和目标延迟等弱状态持久化到 SQLite |
| 订阅安全 | 有界并发、私网目标保护、按来源缓存回退、稳定身份去重、候选测活、原子持久化和失败回滚 |
| WebUI | Vite/TypeScript 模块编译进单个 EXE，提供中英文黑白界面、Named Profiles、访问命令生成、诊断、日志和敏感字段遮罩 |
| 运行保障 | 健康检查批次串行化、严格探测超时、瞬时故障冷却、代理重试/会话保持、日志轮转和事务化配置写入 |
| 地域观察 | 按真实出口 IP 进行 GeoIP 路由，并通过节点名补充 JP/KR/US/HK/TW/SG/住宅节点分组展示 |

## 三种使用方式

| 模式 | 适用场景 | 对外入口 |
|------|----------|----------|
| `pool` | 爬虫希望自动轮换健康节点 | 一个 HTTP/SOCKS5 混合端口 |
| `multi-port` | 外部任务需要固定使用某个节点 | 每个节点一个稳定的混合端口 |
| `hybrid` | 同时需要轮换池和指定节点 | 统一池端口 + 全部节点独立端口 |

## 快速开始

### 原生零配置启动（推荐）

构建完整协议版本：

```bash
go build -trimpath -tags "with_utls with_quic with_grpc with_wireguard with_gvisor with_clash_api" -o proxyfleet ./cmd/proxyfleet
./proxyfleet
```

Windows：

```powershell
go build -trimpath -tags "with_utls with_quic with_grpc with_wireguard with_gvisor with_clash_api" -o proxyfleet.exe ./cmd/proxyfleet
.\proxyfleet.exe
```

首次启动时程序会：

1. 当前工作目录缺少 `config.yaml` 时自动创建默认配置。
2. 生成 32 位密码学随机管理员密码，写入新配置，并在终端**仅显示一次**且提醒尽快修改。
3. 在终端明确显示实际使用的配置文件绝对路径。
4. 以仅管理模式启动内置 WebUI：`http://127.0.0.1:9091`。
5. 在订阅刷新或节点编辑产生可用节点后，自动启动代理运行时，无需重启进程。

进入 WebUI 的**系统设置**，填写订阅地址并保存、刷新。节点通过校验后，
默认统一入口为 `127.0.0.1:2323`：

```bash
curl -x http://127.0.0.1:2323 https://api.ipify.org
```

完整标签用于启用真实 Clash 订阅常见的可选协议实现。其中 Hysteria、
Hysteria2 和 TUIC 必须使用 `with_quic`；无标签构建虽然能解析这些节点，
但无法启动对应出站。

### 使用已有配置

```bash
./proxyfleet -config /path/to/config.yaml
```

`-config` 可以省略；省略时使用当前工作目录的 `config.yaml`。

### 使用当前 Fork 的 Docker 版本

默认 Compose 会从当前检出的源码构建镜像，避免误拉取原版镜像：

```bash
./start.sh
```

首次启动由程序生成仅监听本机的安全配置和随机管理密码，密码见容器日志。
访问 `http://127.0.0.1:9091`。配置、节点缓存和运行状态统一保存在 `./data`，
脚本先构建镜像，成功后才替换容器，并使用私有文件权限。

旧版本的单文件挂载需要先迁移数据，**不要先删除旧容器**。
完整步骤见 [Docker 数据目录与升级](docs/docker-deployment.md)。

## 最小配置示例（Pool）

```yaml
mode: pool

listener:
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
  probe_concurrency: 32  # 全局批量探测并发数（1-1024）
  password: ""
  # 非回环监听地址必须同时配置强密码和原生 TLS：
  # tls_cert_file: ./certs/management.crt
  # tls_key_file: ./certs/management.key

dns:
  server: 223.5.5.5
  port: 53
  strategy: prefer_ipv4

nodes_file: nodes.txt
```

## 节点证书校验策略

```yaml
skip_cert_verify: false
skip_cert_verify_mode: default # default 或 override
```

WebUI「系统设置」中的「跳过 SSL 证书验证」和「节点证书校验策略」可配置这两个字段。

| 策略 | 节点显式 true / 1 | 节点显式 false / 0 | 节点未设置 |
|---|---|---|---|
| `default`：全局默认（默认值） | 跳过校验 | 校验证书 | 使用全局开关 |
| `override`：全局强制覆盖 | 使用全局开关 | 使用全局开关 | 使用全局开关 |

AnyTLS 链接即使没有 `security=tls`，也会采用节点的 `allowInsecure` 或 `insecure`。Clash 的 `skip-cert-verify` 保留显式 true 和 false，VMess JSON 也支持 `allowInsecure` / `insecure`。旧配置省略策略时使用 `default`。如需所有上游节点都必须校验证书，设置 `skip_cert_verify_mode: override` 和 `skip_cert_verify: false`。

策略作用于连接代理节点的 TLS；HTTPS 探测和 Job 目标站的校验继续使用既有全局开关，节点覆盖值不会改变目标站校验。保存策略后会重建运行时配置；已有 pinned Job 绑定会保留并显示策略变化，不会自动更换节点。

## DNS 配置说明

`dns` 会同时影响 sing-box DNS 客户端和 VMess 域名拨号解析：

```yaml
dns:
  server: 223.5.5.5
  fallback_servers:    # 备用 DNS 服务器（主 DNS 解析失败时使用）
    - 8.8.8.8
    - 1.1.1.1
  port: 53
  strategy: prefer_ipv4
```

`strategy` 可选值：

- `as_is`
- `prefer_ipv4`
- `prefer_ipv6`
- `ipv4_only`
- `ipv6_only`

如果日志中出现 `lookup <domain>: empty result`，请优先检查该 DNS 配置是否可达且策略合理。

## 运行模式

- `pool`：所有节点共享一个本地 HTTP/SOCKS5 入口。
- `multi-port`：每个节点一个独立本地 HTTP/SOCKS5 端口。
- `hybrid`：同时启用 pool + multi-port。

多端口模式会把节点规范化 URI 的哈希与端口持久化到配置目录下的 `port-map.yaml`。订阅改名、重排或进程重启不会改变已有节点端口；节点删除后，端口默认保留 24 小时再允许其他节点复用。可通过 `multi_port.port_map_file` 和 `multi_port.port_reuse_delay` 调整。

Pool 支持 `sequential`、`random`、`balance`、`latency` 和 `quality` 调度。真实流量成功时会被动记录目标域名连接延迟 EWMA，不产生额外请求；延迟调度优先使用该目标数据，再回退到全局探测结果。短暂网络故障会进入独立冷却，不会立即累加长期拉黑计数；统一入口可以在拨号失败时切换其他节点重试，并可选启用有容量和 TTL 的会话保持。每节点独立端口始终只使用对应节点。

健康、黑名单/冷却、监控计数以及每节点有界的目标延迟写入配置目录的 `runtime-state.db`。SQLite 使用 WAL 与事务快照；数据库为空时会将旧 `health-state.yaml` 导入一次。可用 `pool.runtime_state_file` 调整路径。

Named Profiles 可按地域、协议、来源、节点名称正则和最低质量预计算子池。连接时使用同一密码，用户名写成 `base@profile`；不带后缀仍使用全局池。该功能需要统一入口认证，适用于 `pool`/`hybrid` 模式，WebUI 的访问助手可一键生成和复制 HTTP/SOCKS5 URI 与 curl 命令。

## 节点来源行为

- 配置了 `subscriptions` 时：
  - 会以有界并发抓取订阅节点（`subscription_refresh.fetch_concurrency`，默认 16，最大 32）
  - 默认拒绝回环、私网、链路本地和元数据地址，并对重定向目标重新校验；只有可信内网订阅才应开启 `subscription_refresh.allow_private_networks`
  - 单个响应严格限制为 10 MB，日志和错误不会暴露订阅凭据、路径或查询参数
  - URL 和节点会按稳定身份去重；运行期按 URL 独立回退到上次成功节点
  - 默认 `subscription_refresh.node_failure_policy: skip` 会按稳定哈希隔离缺少编译能力或候选构建失败的单个节点；设为 `strict` 可恢复整批拒绝
  - `nodes_file` 作为订阅节点写入路径
  - 重启后若首次抓取不完整，会保守使用 `nodes_file` 中上次可用的全局缓存
- `nodes`（内联节点）以及 WebUI 手动新增的节点会保留在 `config.yaml`，订阅刷新不会覆盖。

订阅刷新会把配置、各来源缓存、聚合 `nodes_file` 和运行时切换作为一个事务处理。候选节点会在切换前完成构建和健康检查；抓取失败、持久化失败或配置版本冲突都会回滚，不会替换正在服务的代理池。默认策略会只隔离单个不支持或无法构建的节点，并在状态接口中给出不含 URI 的安全诊断。外部节点缓存中重复的稳定身份会确定性去重，显式内联节点始终优先，而重复的内联定义仍会报错。

超大节点池建议使用 `management.probe_mode: adaptive`，配合 `probe_max_per_hour`（默认 600）和 `probe_max_per_day`（默认 5000）。两者取剩余预算更严格的一项；近期真实流量成功的节点会跳过重复主动探测，预算耗尽后延迟到下个重置窗口。

管理面板密码为空时，`management.listen` 只允许绑定回环地址；如需对外监听，必须同时配置强密码和原生 TLS 证书/私钥。程序会拒绝启动不安全的远程管理监听。

## 抓取任务 Jobs

WebUI **系统设置 → 抓取任务配置**可编辑全部 Job 参数，与 Profile 和 Endpoint 一起保存；**抓取任务**页面提供运行状态、自动测速、HTTP/SOCKS5 地址生成，以及会话搜索、分页和显式释放。凭据默认遮罩，保存失败或配置冲突保留草稿。手动模式无需填写测速目标。

节点来源与连接策略独立配置：默认 `selection: manual` 直接使用指定 Profile 的可用节点，无需目标 URL 或 Job 测速预热；显式设置 `selection: auto` 才按目标站点实测优选少量节点。两种来源均支持 `pooled`（新连接分配节点）和 `pinned`（固定会话节点）；`pool` / `hybrid` 模式可同时使用，每个 Job 一个固定入口。`pinned` 通过代理用户名 `<username>-session-<id>` 区分会话，普通 HTTP/SOCKS5 客户端无需调用管理 API 或接入 SDK。会话分配持久化，节点失效时暂停，须显式释放才能重新分配。配置、管理 API、限制和 Python 接入示例见 [Jobs 使用文档](docs/jobs.md)。

## 协议支持注意事项

运行时真正支持的协议：

- `vmess`
- `vless`
- `trojan`
- `ss` / `shadowsocks`
- `ssr` / `shadowsocksr`
- `hysteria`
- `hysteria2` / `hy2`
- `socks5` / `socks5h` / `socks`
- `http` / `https`
- `anytls`
- `tuic`

Shadowsocks 支持 SIP002、旧式整段 Base64、明文兼容形式，以及内置的 simple-obfs HTTP/TLS 混淆，无需安装插件程序。插件名称兼容 `obfs-http`、`obfs-tls`、`obfs-local`、`simple-obfs` 和 Clash 的 `obfs`。后三种通过 `obfs=http/tls` 或 `mode=http/tls` 选择模式，省略时默认 HTTP；`obfs-host` 或 `host` 指定混淆域名，省略时使用 SS 服务器地址。冲突的模式、无效域名、重复参数、不支持的参数或其他插件会按订阅的 skip/strict 策略处理，不会静默降级为普通 SS。

可在 WebUI 节点编辑器填写以下 URI，也可用于内联节点、节点文件及明文/Base64 订阅：

```text
ss://aes-128-gcm:example-password@ss.example:8388/?plugin=obfs-http%3Bobfs-host%3Dcdn.example#SS-HTTP
ss://aes-128-gcm:example-password@ss.example:8388/?plugin=obfs-local%3Bobfs%3Dtls%3Bobfs-host%3Dcdn.example#SS-TLS
```

Clash 订阅格式：

```yaml
proxies:
  - name: SS-TLS
    type: ss
    server: ss.example
    port: 8388
    cipher: aes-128-gcm
    password: example-password
    plugin: obfs
    plugin-opts:
      mode: tls # 或 http
      host: cdn.example
```

分享链接遵循 [SIP002](https://shadowsocks.org/doc/sip002.html)，内部转换为 sing-box 的 `obfs-local`。这些节点可直接用于 pooled/pinned Job，无需改变 Profile/Endpoint 的使用方式。混淆仅作用于 TCP，原生 SS UDP 不经过混淆；`obfs-tls` 是协议伪装，不是验证证书的 TLS 隧道。

## WebUI

- 仪表盘显示实时节点、地域/住宅节点可用率、延迟和流量图表。
- 节点监控、配置和诊断表格支持点击列排序、关键词搜索、地域筛选以及每页 25/50/100/200 条分页。
- 中文与英文可在系统设置即时切换，界面以黑白配色和 SVG 图标为主。
- 控制台按 info、warn、error 分色；密码和订阅地址默认遮罩，可通过眼睛按钮临时查看。
- 节点列表默认不返回代理凭据；编辑单个节点时才通过受保护接口读取完整配置。
- 系统设置可维护 Named Profiles，并通过访问助手生成可完整复制的代理 URI/curl 命令。

## 管理 API（核心）

- `POST /api/auth`
- `GET|PUT /api/settings`
- `GET /api/access`（管理员访问助手）
- `GET /api/nodes`
- `POST /api/nodes/{tag}/probe`
- `POST /api/nodes/{tag}/release`
- `POST /api/nodes/{tag}/blacklist`
- `POST /api/nodes/probe-all`（SSE）
- `GET /api/export`
- `GET|PUT /api/subscription/config`
- `GET|POST /api/subscription/status|refresh`
- `GET|POST|PUT|DELETE /api/nodes/config[...]`
- `POST /api/reload`

`management.password` 为空时，Web/API 不要求登录。

## 重要运行说明

- 节点和订阅变更使用节点级差异重载：未变化的监听器与连接保持运行，新候选在切换前测活，移除的出站按 `drain_timeout` 排空。只有不可变的全局监听或日志设置变更才需要经过短暂的完整实例交接。
- Settings API 会把配置写回 `config.yaml`；部分设置需要重载后才能完全生效。
- 省略项默认值可在 `internal/config/config.go` 中查看。
- 日志轮转通过 `log` 配置段设置；当 `output: file` 时，日志同时写入控制台和文件，并自动轮转。

## 更新日志

详见 [CHANGELOG.md](CHANGELOG.md)。

## 开发验证

统一构建入口（需要 Node.js 22.12+，推荐使用；也支持 20.19.x，以及 `go.mod` 指定的 Go 工具链）：

```bash
npm ci
npm run build         # 当前 Windows/Linux 平台：检查并构建 WebUI，再编译完整功能程序
npm run build:release # 一次构建 Windows amd64 和 Linux amd64，不会上传或发布

# 单独交叉编译 Linux arm64；也可以重复 --target 构建多个平台
npm run build -- --target linux/arm64
# 指定版本；工作区有未提交改动时自动追加 -dirty
npm run build -- --version v3.3.3
```

产物固定放在仓库 `dist/`：Windows amd64 为 `proxyfleet.exe`，Linux 为 `proxyfleet-linux-amd64` / `proxyfleet-linux-arm64`。同时生成 `SHA256SUMS.txt` 和 `build-manifest.json`，记录本次构建的文件、版本、提交、UTC 构建时间、完整功能标签和验证方式。未指定版本时从 Git 自动识别；源码压缩包或 Docker 上下文没有 Git 信息时使用 `dev` / `unknown`，可通过 `--version` / `--commit` 指定。`SOURCE_DATE_EPOCH` 可固定构建时间。

所有入口复用 `scripts/build.mjs` 和 `scripts/build-tags.txt`，默认包含 QUIC、gRPC、WireGuard、gVisor、uTLS、Clash API。脚本检查编译产物中的平台与标签，并对本机产物运行 `--version-json`；交叉编译产物只检查 Go 构建信息，清单会区分这两种验证方式。所有目标编译和检查通过后才更新输出；不会清空 `dist/` 或改动运行配置。校验文件仅列出本次请求的目标，旧的其他平台文件会保留。

CI 已提前构建前端时可使用 `--skip-webui`，本地默认不要加这个参数。Docker 会在独立阶段重新构建 WebUI，再调用同一个脚本；镜像版本可通过 `--build-arg VERSION=... --build-arg COMMIT=...` 指定。构建不会替换或重启运行中的服务；之前的 `artifacts/jobs/proxyfleet.exe` 属于历史验证产物，后续使用 `dist/`。

单独运行开发检查：

```bash
npm ci
npm run test:build
npm run check:webui
npm run build:webui
npm run test:e2e
go test ./...
go vet ./...

# webui/dist 会嵌入 EXE；CI 会校验它与 TypeScript/CSS 源码一致。
# 验证生产/完整协议构建
npm run build
```

## 许可证

MIT License。依赖归属声明见 [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md)，也可通过 `proxyfleet -third-party-notices` 从单个 EXE 直接查看。

<p align="center">
  <img src="internal/monitor/assets/proxyfleet-logo.png" alt="ProxyFleet 黑白抖动雪人 Logo" width="180" />
</p>

<h1 align="center">ProxyFleet</h1>

[English](README.md) | 简体中文

> 面向爬虫与自动化任务的生产级 sing-box 代理池：既能提供统一轮换
> 入口，也能为每个节点保留稳定的独立端口。

本仓库基于
[jasonwong1991/easy_proxies](https://github.com/jasonwong1991/easy_proxies)
持续开发。它保留了上游的协议基础，但运行时生命周期、状态持久化、
WebUI 和首次启动体验已经明显分化。上游更新会经过评估后选择性移植，
不会直接覆盖本 fork 的实现。

## 为什么选择这个 Fork

| 方向 | 本 Fork 的增强 |
|------|----------------|
| 首次启动 | 原生二进制无需提前准备 `config.yaml`；自动生成仅监听本机的安全默认配置，在终端显示绝对路径，并以零节点 WebUI 启动 |
| 代理入口 | `pool`、`multi-port`、`hybrid` 三种模式，可同时满足自动轮换和指定节点出口 |
| 在线更新 | 节点级 diff 保持未变化监听器和现有连接，删除的出站会先排空，不会在每次订阅刷新时整体中断 |
| 稳定身份 | 订阅改名、重排和重启不会改变节点独立端口；端口、节点认证、健康状态和黑名单均可持久化 |
| 订阅安全 | 有界并发、私网目标保护、按来源缓存回退、稳定身份去重、候选测活、原子持久化和失败回滚 |
| WebUI | 内置中英文黑白界面、正式 SVG 图标、排序、搜索、地域筛选、分页、诊断、分色日志和敏感字段遮罩 |
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
go build -trimpath -tags "with_utls with_quic with_grpc with_wireguard with_gvisor with_clash_api" -o easy_proxies ./cmd/easy_proxies
./easy_proxies
```

Windows：

```powershell
go build -trimpath -tags "with_utls with_quic with_grpc with_wireguard with_gvisor with_clash_api" -o easy_proxies.exe ./cmd/easy_proxies
.\easy_proxies.exe
```

首次启动时程序会：

1. 当前工作目录缺少 `config.yaml` 时自动创建默认配置。
2. 在终端明确显示实际使用的配置文件绝对路径。
3. 以仅管理模式启动内置 WebUI：`http://127.0.0.1:9091`。
4. 在订阅刷新或节点编辑产生可用节点后，自动启动代理运行时，无需重启进程。

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
./easy_proxies -config /path/to/config.yaml
```

`-config` 可以省略；省略时使用当前工作目录的 `config.yaml`。

### 使用当前 Fork 的 Docker 版本

默认 Compose 会从当前检出的源码构建镜像，避免误拉取原版镜像：

```bash
./start.sh
```

也可以手动准备 bind mount 文件后构建：

```bash
cp config.example.yaml config.yaml
touch nodes.txt
docker compose up -d --build
```

启动后访问 `http://127.0.0.1:9091`。

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

Pool 支持 `sequential`、`random`、`balance` 和有界采样的 `latency` 调度。短暂网络故障会进入独立冷却，不会立即累加长期拉黑计数；统一入口可以在拨号失败时切换其他节点重试，并可选启用有容量和 TTL 的会话保持。每节点独立端口始终只使用对应节点。

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

Shadowsocks 支持 SIP002、旧式整段 Base64 和明文兼容形式，但不支持外部 plugin。单个格式错误的节点只会被跳过，不会让整批订阅节点加载失败。

## WebUI

- 仪表盘显示实时节点、地域/住宅节点可用率、延迟和流量图表。
- 节点监控、配置和诊断表格支持点击列排序、关键词搜索、地域筛选以及每页 25/50/100/200 条分页。
- 中文与英文可在系统设置即时切换，界面以黑白配色和 SVG 图标为主。
- 控制台按 info、warn、error 分色；密码和订阅地址默认遮罩，可通过眼睛按钮临时查看。
- 节点列表默认不返回代理凭据；编辑单个节点时才通过受保护接口读取完整配置。

## 管理 API（核心）

- `POST /api/auth`
- `GET|PUT /api/settings`
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

```bash
go test ./...
go vet ./...

# 验证生产/完整协议构建
go build -trimpath -tags "with_utls with_quic with_grpc with_wireguard with_gvisor with_clash_api" -o easy_proxies ./cmd/easy_proxies
```

## 许可证

MIT License


# Docker 数据目录与升级

默认 `docker-compose.yml` 使用 Linux host 网络，将整个 `./data` 挂载到
`/etc/proxyfleet`。配置、节点缓存、认证覆盖、端口映射、健康状态、SQLite
数据库及其 WAL/SHM、审计和历史文件都应放在这个目录内。文件日志另存于
`./logs`。不要再单独挂载 `config.yaml` 或 `nodes.txt`：程序通过原子 rename
保存文件，而单文件 bind mount 无法替换挂载点。

首次启动或正常升级：

```sh
./start.sh
```

脚本先构建镜像，构建失败时保留当前容器；成功后才执行
`docker compose up -d --no-build`。容器替换仍有短暂中断。脚本可在任意工作目录
调用，会切换到仓库目录。已有配置不会被覆盖，错误的文件/目录不会被删除。
如果需要恢复旧镜像，请在升级前为当前镜像另打保留标签并备份数据。

空数据目录由程序生成安全配置和随机管理密码（首次启动的容器日志中显示），
面板仅监听 `127.0.0.1:9091`。代理节点从 WebUI 添加。新配置使用默认相对路径，
自定义绝对路径须迁入数据目录或另行挂载，否则该路径不会随容器保留。

`start.sh` 默认使用当前宿主用户的 UID/GID；root 调用时使用 10001:10001。
容器入口以 root 整理数据目录权限后立即降权运行：目录 `0700`，文件 `0600`，
不递归跟随符号链接。不要设置 `chmod 666`。对于多人维护，选择固定的服务
UID/GID，并在每次 Compose 操作中使用相同值。Docker Desktop 的共享目录权限
由宿主系统管理。

手动运行 Compose 时，显式设置与数据所有者一致的身份（以下命令用于普通用户）：

```sh
export PROXYFLEET_UID="$(id -u)"
export PROXYFLEET_GID="$(id -g)"
umask 077
mkdir -p data logs
docker compose build
docker compose up -d --no-build
```

可通过 `PROXYFLEET_DATA_DIR` 指定另一个完整数据目录。直接运行 Compose 而未指定
身份时默认为 10001:10001。不要在不同 UID 之间反复启动；需要变更时，先停机并
由目录所有者/管理员调整宿主目录权限。

`docker-compose.dev.yml` 使用桥接端口映射。容器内的 loopback 服务不会通过端口
映射暴露；需要从宿主访问时，先配置管理服务监听 `0.0.0.0:9091`，同时配置强密码
和管理 TLS（证书也放入数据目录）。代理入口同样需要容器内可达的监听地址及认证。
例如 `docker compose -f docker-compose.dev.yml build` 后再运行对应的 `up -d --no-build`。

## 从旧的单文件挂载迁移

先确认旧容器名（默认 `proxyfleet`，开发版 `proxyfleet_dev`）及其自定义状态路径。
**不要先执行 `docker compose down` 或删除旧容器**；旧容器可写层可能保存着唯一的
运行状态。以下示例使用默认容器名和默认路径：

```sh
# 可先构建新镜像，旧容器仍保持运行。
docker compose build
docker stop proxyfleet

umask 077
# 必须是新的空目录，mkdir 失败时停止，避免覆盖已有数据。
mkdir data
docker cp proxyfleet:/etc/proxyfleet/. ./data/
```

在旧容器停止后复制整个目录，才能一起保留配置、挂载的 nodes.txt、SQLite 主文件
与 WAL/SHM，以及其他状态文件。若旧配置把某些状态写在 `/app` 或别的绝对路径，
还须从旧容器复制这些文件到 `data`，并将配置路径调整为相对路径。
原 `./logs` 挂载无需迁移。若旧容器已不存在，则从备份恢复；仅复制宿主的
`config.yaml`、`nodes.txt` 无法恢复已经丢失的容器状态。

检查 `data/config.yaml` 和 `data/nodes.txt` 是普通文件，核对订阅、认证、端口映射
和自定义路径，再启动：

```sh
# 必要时由管理员将 data 与 logs 的所有者调整为运行 start.sh 的用户。
chmod 700 data
./start.sh
```

旧根目录文件可以作为备份暂时保留；脚本识别到有效的 `data/config.yaml` 后会使用
新目录。确认设置保存、订阅刷新、重启后的节点和历史状态正常，再处理旧备份。

## 验证

`python3 tests/deployment/start_test.py` 验证构建失败、目录误建和安全启动行为。
`python3 tests/deployment/persistence_smoke.py --image proxyfleet:local` 在 Linux Docker
中验证实际 API 保存配置、更新节点缓存及容器重建后数据仍在；所有数据均为临时
合成数据。CI 自动运行这两个检查。

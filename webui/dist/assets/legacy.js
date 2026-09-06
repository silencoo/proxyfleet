    // JS Logic - Kept exact API calls from original
    let isAutoRefresh = true;
    let autoRefreshInterval = null;
    let allNodesCache = [];
    let nodePaginationMeta = { page: 1, page_size: 50, total_items: 0, total_pages: 0 };
    let nodeSummaryCache = {};
    let topLatencyNodesCache = [];
    let nodeSearchTimer = null;
    let nodeRequestGeneration = 0;
    let currentRole = 'admin';
    let sweepWasActive = false;
    let currentRegionFilter = 'all';
    let isEditMode = false;
    let configNodes = [];
    let configNodesRevealAll = false;
    let confirmResolver = null;
    let confirmReturnFocus = null;
    let _savedSubSnapshot = '';
    let _savedCoreSnapshot = '';
    let settingsETag = '';
    let subscriptionETag = '';
    let subscriptionSettingsLoaded = false;
    let _lastLogsPayload = null;
    const CHART_FONT_FAMILY = '"Noto Sans SC", "Source Han Sans SC", "Microsoft YaHei UI", "Microsoft YaHei", "PingFang SC", "Hiragino Sans GB", "Segoe UI Variable", "Segoe UI", sans-serif';
    const REGION_CHART_COLORS = Object.freeze({
      HK: '#e11d48',
      JP: '#f97316',
      KR: '#eab308',
      US: '#2563eb',
      TW: '#8b5cf6',
      SG: '#10b981',
      RESIDENT: '#ec4899',
      OTHER: '#64748b'
    });
    const nodeTableState = { search: '', page: 1, pageSize: 50, sortKey: 'latency', sortDir: 'asc' };
    const configNodeTableState = { search: '', page: 1, pageSize: 50 };
    const debugTableState = { search: '', page: 1, pageSize: 50, sortKey: 'name', sortDir: 'asc' };
    const REGION_NAME_PATTERNS = [
      ['resident', /(家宽|家庭宽带|住宅|原生|residential|resident|(?:^|[^a-z])isp(?:$|[^a-z])|(?:^|[^a-z])home(?:$|[^a-z]))/i],
      ['hk', /(香港|hong\s*kong|hongkong|\u{1F1ED}\u{1F1F0}|(?:^|[^a-z])hk(?:$|[^a-z]))/iu],
      ['jp', /(日本|东京|大阪|埼玉|japan|\u{1F1EF}\u{1F1F5}|(?:^|[^a-z])jp(?:$|[^a-z]))/iu],
      ['kr', /(韩国|南韩|首尔|仁川|south\s*korea|korea|\u{1F1F0}\u{1F1F7}|(?:^|[^a-z])kr(?:$|[^a-z]))/iu],
      ['us', /(美国|圣何塞|洛杉矶|阿什本|united\s*states|\u{1F1FA}\u{1F1F8}|(?:^|[^a-z])usa?(?:$|[^a-z]))/iu],
      ['tw', /(台湾|台灣|新北|彰化|taiwan|\u{1F1F9}\u{1F1FC}|(?:^|[^a-z])tw(?:$|[^a-z]))/iu],
      ['sg', /(新加坡|狮城|獅城|singapore|\u{1F1F8}\u{1F1EC}|(?:^|[^a-z])sg(?:$|[^a-z]))/iu]
    ];

    const TRANSLATIONS = {
      '监控看板': 'Dashboard',
      '运行中心': 'Operations',
      '探测预算': 'Probe budget',
      '本轮待探测': 'Probe queue',
      '被动健康跳过': 'Passive skips',
      '活动告警': 'Active alerts',
      '指标历史（节点与 P95 延迟）': 'Metric history (nodes and P95 latency)',
      '可用性告警': 'Availability alerts',
      '管理审计': 'Management audit',
      '自适应队列': 'Adaptive queue',
      '节省主动探测': 'Active probes saved',
      '可用性门槛': 'Availability threshold',
      '暂无告警': 'No alerts',
      '暂无审计记录': 'No audit events',
      '健康节点': 'Healthy nodes',
      '等待健康数据': 'Waiting for health data',
      '暂无节点': 'No nodes',
      '全部节点的 {rate}% · {unknown} 个未知': '{rate}% of all nodes · {unknown} unknown',
      '历史记录成功率': 'Recorded success rate',
      '保留的诊断计数，不代表当前健康节点比例。旧记录可能包含健康探测。': 'Retained diagnostic counters, not the current healthy-node percentage. Older records may include health probes.',
      '暂无记录': 'No recorded attempts',
      '异常节点': 'Unavailable nodes',
      '只读': 'Read only',
      '高风险订阅变更': 'High-risk subscription change',
      '确认订阅变更': 'Confirm subscription change',
      '确认高风险变更': 'Confirm high-risk change',
      '应用候选池': 'Apply candidate pool',
      '订阅预览失败': 'Subscription preview failed',
      '订阅比例必须在 0 到 1 之间': 'Subscription ratios must be between 0 and 1',
      '节点配置': 'Nodes',
      '诊断分析': 'Diagnostics',
      '控制台日志': 'Console logs',
      '系统设置': 'System settings',
      '探测中:': 'Probing:',
      '成功:': 'Succeeded:',
      '失败:': 'Failed:',
      '切换主题': 'Change theme',
      '跟随系统': 'System theme',
      '深色模式': 'Dark theme',
      '浅色模式': 'Light theme',
      '批量探测': 'Probe all',
      '刷新订阅': 'Refresh subscription',
      '导出配置': 'Export configuration',
      '关闭自动刷新': 'Disable auto refresh',
      '开启自动刷新': 'Enable auto refresh',
      '刷新': 'Refresh',
      '系统在线': 'System online',
      '总节点数': 'Total nodes',
      '健康在线': 'Healthy',
      '活跃且就绪': 'Active and ready',
      '活跃连接数': 'Active connections',
      '当前建立会话': 'Current sessions',
      '异常/拉黑': 'Unavailable / blocked',
      '不可用节点': 'Unavailable nodes',
      '↑ 上行速率': 'Upload rate',
      '↓ 下行速率': 'Download rate',
      '实时上传': 'Live upload',
      '实时下载': 'Live download',
      '地域连通率 (Region Availability)': 'Region availability',
      '最优节点延迟 (Top Fastest Nodes)': 'Fastest nodes',
      '实时流量带宽 (Real-time Traffic)': 'Real-time traffic',
      '过滤:': 'Filter:',
      '全部': 'All',
      '其他': 'Other',
      '每页': 'Per page',
      '搜索名称、标签、地域或端口': 'Search name, tag, region, or port',
      '搜索节点名称、URI、端口或来源': 'Search node name, URI, port, or source',
      '搜索诊断节点': 'Search diagnostic nodes',
      '上一页': 'Previous page',
      '下一页': 'Next page',
      '显示敏感信息': 'Show sensitive value',
      '隐藏敏感信息': 'Hide sensitive value',
      '显示全部 URI': 'Reveal all URIs',
      '隐藏全部 URI': 'Hide all URIs',
      '复制 URI': 'Copy URI',
      'URI 已复制': 'URI copied',
      '复制 URI 失败': 'Failed to copy URI',
      '节点监控列表': 'Node monitor',
      '状态': 'Status',
      '地域': 'Region',
      '延迟': 'Latency',
      '上行': 'Upload',
      '下行': 'Download',
      '区域总数': 'Total nodes',
      '区域可用': 'Available',
      '调用失败': 'Failures',
      '名称 (Name/Tag)': 'Name / tag',
      '端口': 'Port',
      '延迟 / 质量': 'Latency / quality',
      '连接': 'Connections',
      '失败': 'Failures',
      '操作': 'Actions',
      '暂无数据': 'No data available',
      '配置管理': 'Configuration management',
      '修改配置后点击上方重载按钮生效': 'Reload the core after changing node configuration.',
      '添加节点': 'Add node',
      '重载核心': 'Reload core',
      '警告：当前使用订阅模式。手动修改配置将在下次订阅刷新时被覆盖。': 'Subscription mode is active. Manual node changes will be overwritten by the next subscription refresh.',
      '名称': 'Name',
      '来源': 'Source',
      '总调用次数': 'Total calls',
      '总成功次数': 'Successful calls',
      '全局成功率': 'Overall success rate',
      '全局质量分数 (Overall Health Score)': 'Overall health score',
      '稳定性掉线排行 (Top Unstable Nodes)': 'Most unstable nodes',
      '节点质量分析': 'Node quality analysis',
      '清空记录': 'Clear records',
      '节点': 'Node',
      '未命名节点': 'Unnamed node',
      '成功率': 'Success rate',
      '成功 / 失败': 'Success / failure',
      '最近调用时间线': 'Recent call timeline',
      '控制台日志 (Console Logs)': 'Console logs',
      '自动滚动': 'Auto scroll',
      '清空日志': 'Clear logs',
      '界面偏好': 'Interface preferences',
      '界面语言': 'Interface language',
      '语言设置保存在当前浏览器并立即生效': 'The language preference is stored in this browser and applied immediately.',
      '基础配置': 'General',
      '运行模式 (Mode)': 'Runtime mode',
      'pool - 单端口负载池': 'pool - single-port load balancing',
      'multi-port - 多端口模式': 'multi-port - one port per node',
      'hybrid - 混合模式': 'hybrid - pool and per-node ports',
      '外部 IP (External IP)': 'External IP',
      '探测策略 (Probe)': 'Probe policy',
      '自动探测模式': 'Automatic probe mode',
      'all - 每轮全量': 'all - full pool each pass',
      'sample - 分批轮转': 'sample - rotating batches',
      'manual - 仅手动': 'manual - explicit probes only',
      '探测目标 (Probe Target)': 'Probe target',
      '自动探测间隔': 'Automatic probe interval',
      '单位支持 ms / s / m / h，可组合填写，例如 30s、5m、1h30m；最短 10s。': 'Units: ms, s, m, and h. Combinations such as 30s, 5m, or 1h30m are supported. Minimum: 10s.',
      '自动探测间隔格式无效，请填写 30s、5m 或 1h30m 等格式。': 'Invalid automatic probe interval. Use a value such as 30s, 5m, or 1h30m.',
      '自动探测间隔不能短于 10s。': 'Automatic probe interval cannot be shorter than 10s.',
      '单节点超时': 'Per-node timeout',
      '例如 500ms 或 10s；最短 100ms。': 'For example, 500ms or 10s. Minimum: 100ms.',
      '单节点超时格式无效，请填写 500ms 或 10s 等格式。': 'Invalid per-node timeout. Use a value such as 500ms or 10s.',
      '单节点超时不能短于 100ms。': 'Per-node timeout cannot be shorter than 100ms.',
      '每轮节点数': 'Nodes per pass',
      '探测并发数': 'Probe concurrency',
      '跳过 SSL 证书验证': 'Skip SSL certificate verification',
      '大池建议使用 adaptive：优先失败或过期节点，复用真实流量的被动健康结果，并受每小时预算约束；sample 仍按顺序轮转；manual 会关闭启动和定时探测，但顶部“批量探测”仍可随时手动执行。策略变更需重启程序生效。': 'For large pools, sample probes a bounded rotating batch each pass. Manual disables startup and scheduled probes; Probe all remains available. Restart the process to apply policy changes.',
      '监听配置 (Listener)': 'Pool listener',
      '监听地址': 'Listen address',
      '监听端口': 'Listen port',
      '用户名 (可选)': 'Username (optional)',
      '密码 (可选)': 'Password (optional)',
      '多端口配置 (Multi-Port)': 'Per-node listeners',
      '起始端口': 'Base port',
      '节点池配置 (Pool)': 'Proxy pool',
      '调度模式': 'Scheduling mode',
      'sequential - 轮询': 'sequential - round robin',
      'random - 随机': 'random',
      'round-robin - 轮询': 'round-robin',
      'balance - 均衡': 'balance',
      'latency - 延迟感知': 'latency-aware',
      '故障阈值': 'Failure threshold',
      '黑名单时长': 'Blacklist duration',
      '临时故障冷却': 'Transient failure cooldown',
      '最大连接尝试次数': 'Maximum dial attempts',
      '延迟模式采样节点数': 'Latency sample size',
      '延迟均衡容差': 'Latency balancing tolerance',
      '会话保持 TTL': 'Session affinity TTL',
      '会话保持最大条目': 'Maximum affinity entries',
      '连接失败时切换节点重试': 'Retry another node after a dial failure',
      '启用有界会话保持': 'Enable bounded session affinity',
      '全部节点不可用时尝试恢复': 'Fail open when every node is unavailable',
      '重试和会话保持只应用于统一池入口；每节点独立端口始终固定到对应节点。延迟模式在小样本内选择低延迟节点，并在容差内优先分配给当前连接较少的节点。': 'Retries and session affinity apply only to the unified pool listener. Dedicated ports always use their assigned node. Latency mode samples a bounded set and balances active connections among nodes inside the latency tolerance.',
      '管理面板 (Management)': 'Management UI',
      '访问密码': 'Access password',
      'GeoIP 地域路由': 'GeoIP region routing',
      '启用 GeoIP 地域路由': 'Enable GeoIP region routing',
      '自动更新数据库': 'Automatically update database',
      '数据库路径': 'Database path',
      '路由监听地址': 'Router listen address',
      '路由端口': 'Router port',
      '更新间隔': 'Update interval',
      '首次启用会自动下载 GeoIP 数据库（约 9MB），需重载核心生效': 'The GeoIP database is downloaded on first use (about 9 MB). Reload the core to apply this setting.',
      '日志配置': 'Logging',
      '日志输出': 'Log output',
      '仅控制台 (stdout)': 'Console only (stdout)',
      '控制台 + 文件 (stdout + file)': 'Console and file',
      '单文件最大 MB': 'Maximum file size (MB)',
      '保留旧日志个数': 'Log backups',
      '保留天数': 'Retention days',
      '定时打包间隔': 'Scheduled archive interval',
      '压缩旧日志': 'Compress old logs',
      '日志设置需重启服务生效': 'Restart the service to apply logging changes.',
      '订阅配置': 'Subscriptions',
      '启用订阅自动刷新': 'Enable automatic subscription refresh',
      '刷新间隔': 'Refresh interval',
      '订阅抓取并发数': 'Subscription fetch concurrency',
      '允许访问内网订阅地址（高风险）': 'Allow private-network subscription URLs (high risk)',
      '保存前会先拉取候选并显示新增/删除差异；默认拒绝回环、私网、链路本地和元数据地址': 'Subscription changes apply immediately; loopback, private, link-local, and metadata destinations are blocked by default.',
      '10 分钟': '10 minutes',
      '30 分钟': '30 minutes',
      '1 小时': '1 hour',
      '3 小时': '3 hours',
      '6 小时': '6 hours',
      '12 小时': '12 hours',
      '24 小时': '24 hours',
      '订阅链接 (每行一个 URL)': 'Subscription URLs (one per line)',
      '订阅配置变更保存后立即生效；并发数作用于订阅链接抓取，最大 32': 'Subscription changes take effect immediately after saving. Concurrency controls parallel subscription fetches and is capped at 32.',
      '保存配置': 'Save configuration',
      '身份验证': 'Authentication',
      '身份验证失败': 'Authentication failed',
      '系统密码': 'System password',
      '请输入密码...': 'Enter password...',
      '验证授权': 'Sign in',
      '节点名称 Name': 'Node name',
      '节点 URI': 'Node URI',
      'vless://... 或 anytls://...': 'vless://... or anytls://...',
      '映射端口 (可选)': 'Mapped port (optional)',
      '取消': 'Cancel',
      '保存': 'Save',
      '确认操作': 'Confirm action',
      '确认删除': 'Confirm deletion',
      '处理中...': 'Processing...',
      '编辑节点': 'Edit node',
      '加载节点失败': 'Failed to load node',
      '编辑': 'Edit',
      '删除': 'Delete',
      '探测': 'Probe',
      '解封': 'Release',
      '拉黑': 'Block',
      '拉黑 Blocked': 'Blocked',
      '冷却 Cooling': 'Cooling down',
      '解除冷却': 'Release cooldown',
      '未测试 Unknown': 'Not tested',
      '异常 Error': 'Error',
      '在线 Healthy': 'Healthy',
      '探测中...': 'Probing...',
      '已解封': 'Node released',
      '拉黑失败': 'Failed to block node',
      '确定拉黑该节点 24 小时？': 'Block this node for 24 hours?',
      '探测完成：成功 {success}，失败 {failed}': 'Probe complete: {success} succeeded, {failed} failed',
      '导出失败': 'Export failed',
      '开始刷新订阅...': 'Refreshing subscription...',
      '本地节点已修改，刷新将覆盖，继续？': 'Local nodes have changed. Refreshing the subscription will overwrite them. Continue?',
      '成功获取 {count} 个节点': 'Loaded {count} nodes',
      '刷新失败：{error}': 'Refresh failed: {error}',
      '成功': 'Success',
      '确定删除节点 {name}？': 'Delete node {name}?',
      '删除节点后需要重载核心才会从运行时移除。': 'Reload the core after deletion to remove the node from the runtime.',
      '删除成功': 'Node deleted',
      '删除诊断记录 {name}？': 'Delete the diagnostic record for {name}?',
      '只会清除诊断计数和时间线，不会删除节点或改变路由状态。': 'This clears diagnostic counters and timeline only. It does not delete the node or change routing state.',
      '诊断记录已删除': 'Diagnostic record deleted',
      '清空全部诊断记录？': 'Clear every diagnostic record?',
      '这会清除所有节点的诊断计数和时间线，不会删除节点。': 'This clears diagnostic counters and timelines for every node. Nodes are not deleted.',
      '诊断记录已清空': 'Diagnostic records cleared',
      '清空 Console 日志？': 'Clear console logs?',
      '只会清除当前内存中的 Console 日志，磁盘日志和归档不受影响。': 'Only the in-memory console log is cleared. Log files and archives are unchanged.',
      'Console 日志已清空': 'Console logs cleared',
      '立即重载核心？': 'Reload the core now?',
      '重载成功': 'Core reloaded',
      '配置未变更': 'No configuration changes',
      '保存中...': 'Saving...',
      '保存失败': 'Save failed',
      '设置已被其他操作更新，已重新载入': 'Settings changed elsewhere and were reloaded.',
      '更新订阅中...': 'Updating subscription...',
      '正在拉取订阅并重载节点，请稍候': 'Fetching subscriptions and reloading nodes.',
      '正在拉取候选订阅并计算节点差异': 'Fetching candidate subscriptions and calculating changes.',
      '正在预检候选池并原子切换节点': 'Validating the candidate pool before switching atomically.',
      '候选节点 {candidate} 个：新增 {added}、删除 {removed}、保留 {unchanged}。删除比例 {removedPercent}%。确认应用此候选池？': 'The candidate pool has {candidate} nodes: {added} added, {removed} removed, and {unchanged} unchanged. Removal ratio: {removedPercent}%. Apply this candidate pool?',
      '候选节点 {candidate} 个：新增 {added}、删除 {removed}、保留 {unchanged}。删除比例 {removedPercent}%，已超过安全阈值。确认应用此候选池？': 'The candidate pool has {candidate} nodes: {added} added, {removed} removed, and {unchanged} unchanged. The {removedPercent}% removal ratio exceeds the safety threshold. Apply this candidate pool?',
      '订阅配置保存失败': 'Failed to save subscription settings',
      '订阅设置加载失败，请刷新页面后重试': 'Failed to load subscription settings. Refresh the page and try again.',
      '订阅源状态加载失败': 'Failed to load subscription source status',
      '订阅已保存，但刷新失败：{error}': 'Subscription saved, but refresh failed: {error}',
      '已保存，获取 {count} 个节点': 'Saved and loaded {count} nodes',
      '设置已保存': 'Settings saved',
      '重载核心中...': 'Reloading core...',
      '正在应用新配置': 'Applying the new configuration.',
      '已保存并重载成功': 'Saved and reloaded successfully',
      '配置已保存；管理监听地址将在重启程序后生效，当前地址和密码保持不变': 'Saved. The management listen address will take effect after restarting the process; the current address and password remain active.',
      '配置已保存；部分设置将在重启程序后生效': 'Saved. Some settings will take effect after restarting the process.',
      '已保存，重载失败': 'Saved, but reload failed',
      '请求失败': 'Request failed',
      '会话已过期，请重新登录': 'Your session has expired. Sign in again.',
      '操作成功': 'Operation completed',
      '加载数据失败': 'Failed to load data',
      '加载日志失败': 'Failed to load logs',
      '结构化流量日志加载失败': 'Failed to load structured traffic logs',
      '清空结构化流量日志失败': 'Failed to clear structured traffic logs',
      '探测状态加载失败': 'Failed to load probe status',
      '指标历史加载失败': 'Failed to load metric history',
      '告警加载失败': 'Failed to load alerts',
      '审计记录加载失败': 'Failed to load audit events',
      '管理密码与订阅设置不能同时保存，请先保存订阅设置，再单独修改管理密码': 'The management password and subscription settings cannot be saved together. Save the subscription settings first, then change the management password separately.',
      '管理密码已更新，请使用新密码重新登录': 'The management password was updated. Sign in again with the new password.',
      '构建信息 (Build Info)': 'Build information',
      '完整发行构建': 'Full release build',
      '功能受限构建': 'Limited-capability build',
      '官方协议能力均已编译': 'All official protocol capabilities are compiled in',
      '缺少编译能力': 'Missing build capabilities',
      '部分编译能力不可用': 'Some build capabilities are unavailable',
      '构建信息加载失败': 'Failed to load build information',
      '无': 'None',
      '版本': 'Version',
      '目标平台': 'Target platform',
      '编译能力': 'Build capabilities',
      '可用协议': 'Supported protocols',
      '简体中文': 'Chinese (Simplified)',
      'adaptive - 自适应预算': 'adaptive - adaptive budget',
      '健康节点复探间隔': 'Healthy-node reprobe interval',
      '近期有真实流量成功的节点会继续跳过主动探测。': 'Nodes with recent successful traffic continue to skip active probes.',
      '失败初始重试': 'Initial failure retry',
      '失败退避上限': 'Failure backoff limit',
      '被动成功宽限期': 'Passive-success grace period',
      '每小时探测预算': 'Hourly probe budget',
      'adaptive 中 0 使用默认值 600；耗尽后等下一小时。': 'In adaptive mode, 0 uses the default of 600. Probing resumes in the next hour after the budget is exhausted.',
      '每天探测预算': 'Daily probe budget',
      'adaptive 中 0 使用默认值 5000；小时与每日限制取更严格者。': 'In adaptive mode, 0 uses the default of 5000. The stricter hourly or daily limit applies.',
      '大池建议使用 adaptive：优先失败或过期节点，复用真实流量的被动健康结果，并同时受每小时与每日预算约束；sample 仍按顺序轮转；manual 会关闭启动和定时探测，但顶部“批量探测”仍可随时手动执行。策略变更需重启程序生效。': 'For large pools, adaptive prioritizes failed or stale nodes, reuses passive health results from real traffic, and respects hourly and daily budgets. Sample still rotates in order. Manual disables startup and scheduled probes, while Probe all remains available. Restart the process to apply policy changes.',
      'quality - 综合质量分': 'quality - composite health score',
      '管理员密码': 'Administrator password',
      '运维密码': 'Operator password',
      '可探测、刷新、清理，但不能修改配置。': 'Can probe, refresh, and clear data, but cannot change configuration.',
      '只读密码': 'Viewer password',
      '只允许查看节点、日志、指标和告警。': 'Can only view nodes, logs, metrics, and alerts.',
      '持久化指标历史': 'Store metric history',
      '指标文件': 'Metrics file',
      '指标保留时长': 'Metric retention',
      '指标采样间隔': 'Metric sample interval',
      '可用节点告警数量': 'Available-node alert threshold',
      '可用节点告警比例': 'Availability-ratio alert threshold',
      '告警冷却时间': 'Alert cooldown',
      '审计文件': 'Audit file',
      '结构化流量日志': 'Structured traffic log',
      '独立 SQLite、异步批量写入；关闭时不创建数据库。适合按节点/Profile 排查连接质量。': 'Uses a separate SQLite database with asynchronous batch writes. No database is created while disabled. Useful for investigating connection quality by node or Profile.',
      '当前未启用；可在设置 → 日志配置中开启。': 'Currently disabled. Enable it under Settings → Logging.',
      '确定清空结构化流量日志？此操作会删除 SQLite 中的全部连接历史，无法恢复。': 'Clear all structured traffic logs? This permanently deletes every connection record from SQLite.',
      '结构化流量日志已清空': 'Structured traffic logs cleared',
      '数据库文件': 'Database file',
      '保留时长': 'Retention',
      '最多记录': 'Maximum entries',
      '哈希脱敏目标地址': 'Hash destination addresses',
      '坏节点处理策略': 'Invalid-node policy',
      'skip - 隔离坏节点并继续': 'skip - isolate invalid nodes and continue',
      'strict - 拒绝整批变更': 'strict - reject the entire update',
      '最大自动删除比例': 'Maximum automatic removal ratio',
      '超过比例时保存前必须二次确认。': 'Changes above this ratio require confirmation before saving.',
      '候选池最小可用比例': 'Minimum candidate availability ratio',
      '新节点通过候选池预检后再切换（隔离验证）': 'Validate new nodes in quarantine before switching the candidate pool',
      'GeoIP 自动更新时间格式无效': 'Invalid GeoIP automatic update interval',
      '管理共享节点池的独立 HTTP / SOCKS5 入口；新增入口不会复制节点，也不会增加健康探测。': 'Manage independent HTTP / SOCKS5 entry points for the shared node pool. Adding an entry point does not duplicate nodes or add health probes.',
      '添加 Endpoint': 'Add Endpoint',
      'Endpoint 只在 pool / hybrid 模式启动；切换到 multi-port 时配置会保留，但监听保持停用。': 'Endpoints start only in pool / hybrid mode. Their configuration is retained in multi-port mode, but their listeners remain inactive.',
      '正在读取 Endpoint…': 'Loading Endpoints…',
      '尚未配置 Endpoint。请添加一个池入口后保存。': 'No Endpoint is configured. Add a pool entry point and save.',
      '运行中': 'Running',
      '等待节点': 'Waiting for nodes',
      '已停用': 'Disabled',
      '当前模式停用': 'Inactive in current mode',
      '启动失败': 'Failed to start',
      '待保存': 'Unsaved',
      '启用': 'Enabled',
      '固定 Profile': 'Pinned Profile',
      '全部节点': 'All nodes',
      '留空时允许访问全部节点，并支持 base@profile。': 'Leave blank to access all nodes and support base@profile.',
      '用户名（可选）': 'Username (optional)',
      '密码（可选）': 'Password (optional)',
      '显示密码': 'Show password',
      '隐藏密码': 'Hide password',
      '保存后由运行时重新确认状态': 'The runtime will confirm the status again after saving.',
      '监听正常': 'Listener is running normally',
      '未命名 Endpoint': 'Unnamed Endpoint',
      '至少需要保留一个 Endpoint。': 'At least one Endpoint is required.',
      'Endpoint 数量不能超过 128 个。': 'No more than 128 Endpoints are allowed.',
      'Endpoint 名称需为 1–32 位小写字母、数字、点、下划线或连字符。': 'Endpoint names must contain 1–32 lowercase letters, digits, dots, underscores, or hyphens.',
      'Endpoint 名称重复：{name}': 'Duplicate Endpoint name: {name}',
      'Endpoint {name} 的监听地址必须是 IP 地址。': 'The listen address for Endpoint {name} must be an IP address.',
      'Endpoint {name} 的端口必须在 1 到 65535 之间。': 'The port for Endpoint {name} must be between 1 and 65535.',
      '监听地址与端口重复：{socket}': 'Duplicate listen address and port: {socket}',
      'Endpoint {name} 的用户名和密码必须同时填写或同时留空。': 'The username and password for Endpoint {name} must both be set or both be blank.',
      'Endpoint {name} 引用了不存在的 Profile：{profile}': 'Endpoint {name} references a missing Profile: {profile}',
      '删除 Endpoint': 'Delete Endpoint',
      '确定删除 {name}？保存后该监听将立即停止，现有连接可能中断。': 'Delete {name}? The listener will stop after saving, and existing connections may be interrupted.',
      '组合地域、协议、来源、质量与节点名称规则；静态成员视图在池创建时预计算。': 'Combine region, protocol, source, quality, and node-name rules. Static membership is precomputed when the pool is created.',
      '添加 Profile': 'Add Profile',
      '尚未配置 Profile。所有连接继续使用全局池。': 'No Profile is configured. All connections continue to use the global pool.',
      '访问助手 (Access Assistant)': 'Access Assistant',
      '生成 HTTP / SOCKS5 URI 与 curl 命令；复制内容来自当前运行配置。': 'Generate HTTP / SOCKS5 URIs and curl commands from the current runtime configuration.',
      '读取中': 'Loading',
      '默认入口': 'Default Endpoint',
      '协议': 'Protocol',
      '代理 URI': 'Proxy URI',
      '复制': 'Copy',
      '正在读取统一入口…': 'Loading the shared Endpoint…',
      '就绪': 'Ready',
      '读取失败': 'Failed to load',
      '可选择 Profile；访问用户名会自动生成为 base@profile。': 'Select a Profile to generate an access username in base@profile format.',
      '该 Endpoint 未启用认证；如需客户端选择 Profile，请为入口配置用户名和密码，或将 Endpoint 固定到一个 Profile。': 'This Endpoint does not use authentication. To let clients select a Profile, configure Endpoint credentials or pin the Endpoint to a Profile.',
      '每个来源可独立启停、设置刷新周期并查看最近一次结果；留空独立周期时继承上方全局值。': 'Each source can be enabled independently, use its own refresh interval, and show its latest result. Leave the source interval blank to inherit the global value.',
      '添加订阅源': 'Add subscription source',
      '正在读取订阅源…': 'Loading subscription sources…',
      '尚未配置订阅源。可以保留为空，仅使用手动节点。': 'No subscription source is configured. Leave this empty to use only manually added nodes.',
      '刷新中': 'Refreshing',
      '刷新失败': 'Refresh failed',
      '使用缓存': 'Using cached data',
      '正常': 'Healthy',
      '等待刷新': 'Waiting to refresh',
      '自定义 Header': 'Custom headers',
      '请求头已保存在配置中；WebUI 不会回显敏感值': 'Headers are stored in the configuration. The WebUI does not reveal sensitive values.',
      '刷新此源': 'Refresh source',
      '保存并完成一次全量刷新后可独立刷新': 'Save and complete one full refresh before refreshing this source independently.',
      '显示 URL': 'Show URL',
      '隐藏 URL': 'Hide URL',
      '复制完整 URL': 'Copy full URL',
      '独立刷新周期': 'Source refresh interval',
      '继承全局，例如 30m': 'Inherit global, e.g. 30m',
      '支持 30m、1h、1h30m，最短 5m。': 'Supports 30m, 1h, and 1h30m. Minimum: 5m.',
      '耗时': 'Duration',
      '上次成功': 'Last success',
      '下次刷新': 'Next refresh',
      '这个 Endpoint': 'this Endpoint',
      'Endpoint {endpoint} 已固定到 Profile {profile}，客户端无需修改用户名。': 'Endpoint {endpoint} is pinned to Profile {profile}; clients do not need to change the username.',
      ' 当前状态：{status}{message}。': ' Current status: {status}{message}.',
      '状态异常': 'Unexpected status',
      '无法读取访问配置': 'Unable to load access configuration',
      '尚未预览': 'Not previewed',
      '刷新预览': 'Refresh preview',
      '地域（逗号分隔）': 'Regions (comma-separated)',
      '来源': 'Source',
      '最低质量分': 'Minimum quality score',
      'ANY · 至少命中一条': 'ANY · match at least one',
      'MUST · 每条都要命中': 'MUST · match every rule',
      'MUST NOT · 任一命中即排除': 'MUST NOT · exclude on any match',
      '留空表示不限制；每行一条正则。': 'Leave blank for no restriction. Enter one regular expression per line.',
      '适合叠加套餐、线路或用途条件。': 'Use this to combine plan, route, or usage requirements.',
      '优先排除到期、流量提示或不需要的节点。': 'Exclude expired, traffic-limited, or unwanted nodes first.',
      '正在准备匹配预览…': 'Preparing match preview…',
      '计算中': 'Calculating',
      '正在按当前运行节点计算…': 'Calculating against the current runtime nodes…',
      '{matched}/{total} 命中': '{matched}/{total} matched',
      '预览失败': 'Preview failed',
      '无法计算匹配预览': 'Unable to calculate the match preview',
      '当前运行配置中没有可预览节点。': 'There are no runtime nodes to preview.',
      '名称规则': 'Name rules',
      '质量': 'Quality',
      '质量 {quality}': 'Quality {quality}',
      '{reason} excluded: {count}': '{reason} excluded: {count}',
      '命中': 'Matched',
      '排除': 'Excluded',
      '命中样本': 'Matched samples',
      '未命中样本': 'Excluded samples',
      '已复制': 'Copied',
      '这个订阅源': 'this subscription source',
      '删除订阅源': 'Delete subscription source',
      '确定删除 {name}？保存后该来源的缓存节点会从候选池移除。': 'Delete {name}? Its cached nodes will be removed from the candidate pool after saving.',
      '未命名订阅源': 'Unnamed subscription source',
      '订阅 URL 已复制': 'Subscription URL copied',
      '复制失败': 'Copy failed',
      '{name} 刷新成功': '{name} refreshed successfully',
      '订阅源刷新失败': 'Subscription source refresh failed',
      '订阅源数量不能超过 128 个。': 'No more than 128 subscription sources are allowed.',
      '订阅源名称需为 1–32 位小写字母、数字、点、下划线或连字符。': 'Subscription source names must contain 1–32 lowercase letters, digits, dots, underscores, or hyphens.',
      '订阅源名称重复：{name}': 'Duplicate subscription source name: {name}',
      '订阅地址重复：{name}': 'Duplicate subscription URL: {name}',
      '订阅源 {name} 的 URL 无效，仅支持 HTTP/HTTPS。': 'Subscription source {name} has an invalid URL. Only HTTP and HTTPS are supported.',
      '订阅源 {name} 的独立周期格式无效或小于 5 分钟。': 'Subscription source {name} has an invalid refresh interval or one shorter than 5 minutes.',
      'Profile 名称需为 1–32 位小写字母、数字、点、下划线或连字符。': 'Profile names must contain 1–32 lowercase letters, digits, dots, underscores, or hyphens.',
      'Profile 名称重复：{name}': 'Duplicate Profile name: {name}',
      'Profile {name} 的 {group} 规则不能超过 64 条。': 'Profile {name} cannot have more than 64 {group} rules.',
      'Profile {name} 的 {group} 第 {index} 条正则无效。': 'Profile {name} has an invalid regular expression at {group} rule {index}.',
      '最低质量分必须在 0 到 100 之间。': 'The minimum quality score must be between 0 and 100.',
      '结果': 'Result',
      '结果筛选': 'Filter by result',
      '全部结果': 'All results',
      '仅成功': 'Successful only',
      '仅失败': 'Failed only',
      '时间': 'Time',
      '目标': 'Destination',
      '总时长': 'Total duration',
      '流量 ↑ / ↓': 'Traffic ↑ / ↓',
      '尝试': 'Attempts',
      '失败分类': 'Failure class',
      '级别': 'Severity',
      '消息': 'Message',
      '更新时间': 'Updated',
      '角色': 'Role',
      '方法': 'Method',
      '路径': 'Path',
      '活动': 'Active',
      '已恢复': 'Recovered',
      '最近 {count} 条 · 写入队列累计丢弃 {dropped} 条': '{count} most recent · {dropped} dropped by the write queue'
    };

    let currentLanguage = localStorage.getItem('uiLanguage') || (navigator.language && navigator.language.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en');

    function tr(source, values) {
      let result = currentLanguage === 'en' ? (TRANSLATIONS[source] || source) : source;
      if (values) {
        Object.keys(values).forEach(key => { result = result.replace(`{${key}}`, values[key]); });
      }
      return result;
    }

    function translateTree(root) {
      if (!root) return;
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      let node;
      while ((node = walker.nextNode())) {
        const parent = node.parentElement;
        if (!parent || parent.closest('script, style, svg')) continue;
        const trimmed = node.nodeValue.trim();
        if (!trimmed) continue;
        if (!node.__i18nSource) node.__i18nSource = trimmed;
        const translated = currentLanguage === 'en' ? (TRANSLATIONS[node.__i18nSource] || node.__i18nSource) : node.__i18nSource;
        node.nodeValue = node.nodeValue.replace(trimmed, translated);
      }
      root.querySelectorAll('[placeholder], [title], [aria-label]').forEach(element => {
        ['placeholder', 'title', 'aria-label'].forEach(attribute => {
          if (!element.hasAttribute(attribute)) return;
          const dataKey = `i18n${attribute.replace(/(^|-)([a-z])/g, (_, __, letter) => letter.toUpperCase())}`;
          if (!element.dataset[dataKey]) element.dataset[dataKey] = element.getAttribute(attribute);
          const source = element.dataset[dataKey];
          element.setAttribute(attribute, currentLanguage === 'en' ? (TRANSLATIONS[source] || source) : source);
        });
      });
    }

    function iconMarkup(name) {
      return `<svg class="icon" aria-hidden="true"><use href="#i-${name}"></use></svg>`;
    }

    function normalizeNodeName(value) {
      let text = String(value || '');
      for (let attempt = 0; attempt < 3 && /%[0-9a-f]{2}/i.test(text); attempt += 1) {
        try { text = decodeURIComponent(text); } catch (_) {}
      }
      return typeof text.normalize === 'function' ? text.normalize('NFC') : text;
    }

    function getNodeDisplayName(node) {
      return normalizeNodeName((node && (node.name || node.tag)) || '');
    }

    function sanitizeChartLabel(value) {
      return normalizeNodeName(value)
        .replace(/[\u{1F1E6}-\u{1F1FF}]/gu, '')
        .replace(/\p{Extended_Pictographic}/gu, '')
        .replace(/[\u200D\u20E3\uFE0E\uFE0F\uFFFD]/g, '')
        .replace(/\s+/g, ' ')
        .trim();
    }

    function getChartNodeDisplayName(node) {
      return sanitizeChartLabel(getNodeDisplayName(node)) ||
        sanitizeChartLabel(node && node.tag) ||
        tr('未命名节点');
    }

    function getNodeRegion(node) {
      const reported = String((node && node.region) || '').toLowerCase();
      if (reported && reported !== 'other' && reported !== 'unknown') return reported;
      const searchable = [node && node.name, node && node.tag, node && node.country].map(normalizeNodeName).join(' ');
      for (const [region, pattern] of REGION_NAME_PATTERNS) {
        if (pattern.test(searchable)) return region;
      }
      return 'other';
    }

    function getNodeStatusRank(node) {
      if (node.blacklisted) return 3;
      if (node.cooling_down) return 2.5;
      if (!node.initial_check_done) return 2;
      if (!node.available) return 1;
      return 0;
    }

    function compareValues(left, right) {
      const leftMissing = left === null || left === undefined || Number.isNaN(left);
      const rightMissing = right === null || right === undefined || Number.isNaN(right);
      if (leftMissing || rightMissing) return leftMissing === rightMissing ? 0 : (leftMissing ? 1 : -1);
      if (typeof left === 'number' && typeof right === 'number') return left - right;
      return String(left).localeCompare(String(right), currentLanguage === 'en' ? 'en' : 'zh-CN', { numeric: true, sensitivity: 'base' });
    }

    function sortRows(rows, state, valueForKey) {
      const direction = state.sortDir === 'desc' ? -1 : 1;
      return rows.map((row, index) => ({ row, index })).sort((a, b) => {
        const result = compareValues(valueForKey(a.row, state.sortKey), valueForKey(b.row, state.sortKey));
        return result === 0 ? a.index - b.index : result * direction;
      }).map(item => item.row);
    }

    function updateSortIndicators(tableName, state) {
      document.querySelectorAll(`.sort-button[data-sort-table="${tableName}"]`).forEach(button => {
        const active = button.dataset.sortKey === state.sortKey;
        const header = button.closest('th');
        if (header) header.setAttribute('aria-sort', active ? (state.sortDir === 'asc' ? 'ascending' : 'descending') : 'none');
        const use = button.querySelector('use');
        if (use) use.setAttribute('href', active ? (state.sortDir === 'asc' ? '#i-chevron-up' : '#i-chevron-down') : '#i-sort');
      });
    }

    function pageSlice(rows, state) {
      const pageCount = Math.max(1, Math.ceil(rows.length / state.pageSize));
      state.page = Math.min(Math.max(1, state.page), pageCount);
      const start = (state.page - 1) * state.pageSize;
      return { rows: rows.slice(start, start + state.pageSize), start, pageCount };
    }

    function updatePagination(prefix, total, state, start, pageCount) {
      const info = document.getElementById(`${prefix}PageInfo`);
      const prev = document.getElementById(`${prefix}PrevPage`);
      const next = document.getElementById(`${prefix}NextPage`);
      const visibleStart = total ? start + 1 : 0;
      const visibleEnd = Math.min(total, start + state.pageSize);
      if (info) info.textContent = `${visibleStart}–${visibleEnd} / ${total} · ${state.page}/${pageCount}`;
      if (prev) prev.disabled = state.page <= 1;
      if (next) next.disabled = state.page >= pageCount;
    }

    function toggleSensitiveField(fieldId, button) {
      const field = document.getElementById(fieldId);
      if (!field) return;
      const reveal = button.getAttribute('aria-pressed') !== 'true';
      if (field.tagName === 'TEXTAREA') field.classList.toggle('masked', !reveal);
      else field.type = reveal ? 'text' : 'password';
      button.setAttribute('aria-pressed', String(reveal));
      button.setAttribute('aria-label', tr(reveal ? '隐藏敏感信息' : '显示敏感信息'));
      button.setAttribute('title', tr(reveal ? '隐藏敏感信息' : '显示敏感信息'));
      const use = button.querySelector('use');
      if (use) use.setAttribute('href', reveal ? '#i-eye-off' : '#i-eye');
    }

    function syncAutoRefreshButton() {
      document.getElementById('autoRefreshIcon').setAttribute('href', isAutoRefresh ? '#i-pause' : '#i-play');
      document.getElementById('autoRefreshLabel').textContent = tr(isAutoRefresh ? '关闭自动刷新' : '开启自动刷新');
    }

    function setLanguage(language) {
      currentLanguage = language === 'en' ? 'en' : 'zh-CN';
      localStorage.setItem('uiLanguage', currentLanguage);
      document.documentElement.lang = currentLanguage;
      document.title = currentLanguage === 'en' ? 'ProxyFleet - Control Center' : 'ProxyFleet - 监控中心';
      translateTree(document.body);
      const selector = document.getElementById('settingLanguage');
      if (selector) selector.value = currentLanguage;
      syncAutoRefreshButton();
      syncToggleButton(localStorage.getItem('themeMode') || 'auto');
      if (document.getElementById('dashboardTab').classList.contains('active')) filterByRegion(currentRegionFilter);
      if (document.getElementById('manageTab').classList.contains('active')) loadConfigNodes();
      if (document.getElementById('debugTab').classList.contains('active')) loadDebugData();
      reRenderCharts();
    }

    function showFullscreenLoading(text, subtext) {
      document.getElementById('fullscreenLoadingText').textContent = text || tr('处理中...');
      document.getElementById('fullscreenLoadingSubtext').textContent = subtext || '';
      const el = document.getElementById('fullscreenLoading');
      el.style.display = 'flex';
    }
    function hideFullscreenLoading() {
      document.getElementById('fullscreenLoading').style.display = 'none';
    }

    // Chart instances
    let regionChartInst = null;
    let latencyChartInst = null;
    let successRateChartInst = null;
    let failureChartInst = null;
    let trafficChartInst = null;
    let operationsHistoryChartInst = null;
    let trafficSource = null;
    let trafficRetryTimer = null;
    let trafficRetryAttempts = 0;
    let trafficDataUp = [];
    let trafficDataDown = [];
    let trafficTime = [];
    let regionStatsCache = {};
    let regionHealthyCache = {};
    let debugSummaryCache = {success_rate: 0, total_calls: 0};

    // Resize observer for charts
    window.addEventListener('resize', () => {
      regionChartInst && regionChartInst.resize();
      latencyChartInst && latencyChartInst.resize();
      successRateChartInst && successRateChartInst.resize();
      failureChartInst && failureChartInst.resize();
      trafficChartInst && trafficChartInst.resize();
      operationsHistoryChartInst && operationsHistoryChartInst.resize();
    });

    function showToast(msg, type='success') {
      const c = document.getElementById('toastContainer');
      const t = document.createElement('div');
      t.className = `toast ${type}`; t.textContent = msg;
      c.appendChild(t);
      setTimeout(() => { t.style.opacity=0; setTimeout(()=>t.remove(),300); }, 3000);
    }

    function requestConfirmation(title, message, confirmLabel) {
      if (confirmResolver) resolveConfirmation(false);
      confirmReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      document.getElementById('confirmTitle').textContent = title || tr('确认操作');
      document.getElementById('confirmMessage').textContent = message || '';
      document.getElementById('confirmAccept').textContent = confirmLabel || tr('确认删除');
      const overlay = document.getElementById('confirmOverlay');
      overlay.classList.add('show');
      return new Promise(resolve => {
        confirmResolver = resolve;
        requestAnimationFrame(() => document.getElementById('confirmCancel').focus());
      });
    }

    function resolveConfirmation(confirmed) {
      const resolver = confirmResolver;
      confirmResolver = null;
      document.getElementById('confirmOverlay').classList.remove('show');
      if (confirmReturnFocus && document.contains(confirmReturnFocus)) confirmReturnFocus.focus();
      confirmReturnFocus = null;
      if (resolver) resolver(Boolean(confirmed));
    }

    document.addEventListener('keydown', event => {
      const overlay = document.getElementById('confirmOverlay');
      if (!overlay.classList.contains('show')) return;
      if (event.key === 'Escape') {
        event.preventDefault();
        resolveConfirmation(false);
        return;
      }
      if (event.key === 'Tab') {
        const focusable = [document.getElementById('confirmCancel'), document.getElementById('confirmAccept')];
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first.focus();
        } else if (!focusable.includes(document.activeElement)) {
          event.preventDefault();
          first.focus();
        }
      }
    });

    function showLoginOverlay() {
      document.getElementById('loginOverlay').classList.add('show');
      stopAutoRefresh();
    }

    function localizedAPIMessage(message, fallback='请求失败') {
      const raw = String(message || '').trim();
      if (!raw) return tr(fallback);
      if (currentLanguage !== 'en') return raw;
      if (TRANSLATIONS[raw]) return TRANSLATIONS[raw];
      return /[\u3400-\u9fff]/.test(raw) ? tr(fallback) : raw;
    }

    window.proxyFleetI18n = { tr, translateTree, localizedAPIMessage };

    async function readAPIJSON(response, fallback='请求失败') {
      let payload = {};
      try { payload = await response.json(); } catch (_) {}
      if (response.status === 401) {
        showLoginOverlay();
        throw new Error(tr('会话已过期，请重新登录'));
      }
      if (!response.ok) throw new Error(localizedAPIMessage(payload.error, fallback));
      return payload;
    }

    async function requireAPIResponse(response, fallback='请求失败') {
      if (response.ok) return response;
      await readAPIJSON(response, fallback);
      return response;
    }

    const ROLE_RANK = Object.freeze({ viewer: 1, operator: 2, admin: 3 });
    function applyRolePermissions(role) {
      currentRole = ROLE_RANK[role] ? role : 'viewer';
      const badge = document.getElementById('roleBadge');
      if (badge) {
        badge.textContent = currentRole.toUpperCase();
        badge.className = `badge ${currentRole === 'admin' ? 'badge-healthy' : (currentRole === 'operator' ? 'badge-warning' : 'badge-offline')}`;
      }
      document.querySelectorAll('[data-min-role]').forEach(element => {
        const allowed = (ROLE_RANK[currentRole] || 0) >= (ROLE_RANK[element.dataset.minRole] || 99);
        element.hidden = !allowed;
      });
    }

    async function loadCurrentRole() {
      const response = await fetch('/api/session');
      if (!response.ok) return false;
      const session = await response.json().catch(() => ({}));
      applyRolePermissions(session.role || 'viewer');
      return true;
    }

    async function checkAuth() {
      try {
        const res = await fetch('/api/nodes');
        if (res.status === 401) { showLoginOverlay(); return false; }
        if (!res.ok) return false;
        await loadCurrentRole();
        return true;
      } catch (e) { return false; }
    }

    async function handleLogin(e) {
      e.preventDefault();
      const pwd = document.getElementById('password').value;
      const err = document.getElementById('loginError');
      try {
        const res = await fetch('/api/auth', { method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({password:pwd})});
        if (res.ok) {
          const payload = await res.json().catch(() => ({}));
          applyRolePermissions(payload.role || 'viewer');
          document.getElementById('loginOverlay').classList.remove('show');
          refresh();
          if (isAutoRefresh) startAutoRefresh();
        }
        else { err.textContent = tr('身份验证失败'); err.style.display = 'block'; }
      } catch (ex) { err.textContent = ex.message; err.style.display = 'block'; }
    }

    function switchTab(name) {
      document.querySelectorAll('.tab-content').forEach(el => el.classList.remove('active'));
      document.querySelectorAll('.nav-item').forEach(el => { el.classList.remove('active'); el.removeAttribute('aria-current'); });
      document.getElementById(`${name}Tab`).classList.add('active');
      const activeNav = document.querySelector(`.nav-item[data-tab="${name}"]`);
      activeNav.classList.add('active');
      activeNav.setAttribute('aria-current', 'page');
      if (name === 'dashboard') {
        setTimeout(updateDashboardCharts, 100);
      }
      if (name === 'manage') loadConfigNodes();
      if (name === 'operations') {
        loadOperations();
        setTimeout(renderOperationsHistory, 100);
      }
      if (name === 'debug') {
        loadDebugData();
        setTimeout(updateDebugCharts, 100);
      }
      if (name === 'logs') {
        startLogPolling();
      } else {
        stopLogPolling();
      }
      if (name === 'settings') loadSettingsPage();
    }

    function toggleAutoRefresh() {
      isAutoRefresh = !isAutoRefresh;
      syncAutoRefreshButton();
      if(isAutoRefresh) startAutoRefresh(); else stopAutoRefresh();
    }
    function startAutoRefresh() { if(!autoRefreshInterval) autoRefreshInterval = setInterval(()=> { if(!document.hidden && document.getElementById('dashboardTab').classList.contains('active')) refresh(); }, 10000); }
    function stopAutoRefresh() { clearInterval(autoRefreshInterval); autoRefreshInterval = null; }

    function getQualityWidth(ms) {
      if(ms < 0) return 0; if(ms > 1000) return 100;
      return (ms/1000)*100;
    }
    function getQualityColor(ms) {
      if(ms < 0) return 'transparent';
      if(ms < 100) return 'var(--success)';
      if(ms < 200) return 'var(--primary)';
      if(ms < 500) return 'var(--warning)';
      return 'var(--error)';
    }
    function formatTime(ts) {
      if(!ts || ts==='0001-01-01T00:00:00Z') return '-';
      return new Date(ts).toLocaleTimeString();
    }

    function escapeHtml(text) {
      if(!text) return '';
      const div = document.createElement('div'); div.textContent = text; return div.innerHTML;
    }

    function maskNodeURI(uri) {
      const value = String(uri || '').trim();
      if (!value) return '';
      const separator = value.indexOf('://');
      return separator >= 0 ? `${value.slice(0, separator + 3)}********` : '********';
    }

    function inlineStringArg(value) {
      return JSON.stringify(String(value || '')).replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    }

    async function refresh() {
      const requestGeneration = ++nodeRequestGeneration;
      const params = new URLSearchParams({
        page: String(nodeTableState.page),
        page_size: String(nodeTableState.pageSize),
        region: currentRegionFilter,
        status: 'all',
        sort: nodeTableState.sortKey,
        order: nodeTableState.sortDir,
      });
      if (nodeTableState.search) params.set('search', nodeTableState.search);
      try {
        const res = await fetch(`/api/nodes?${params.toString()}`);
        const data = await readAPIJSON(res, '加载数据失败');
        if (requestGeneration !== nodeRequestGeneration) return;
        allNodesCache = Array.isArray(data.nodes) ? data.nodes : [];
        nodePaginationMeta = data.pagination || { page: 1, page_size: nodeTableState.pageSize, total_items: allNodesCache.length, total_pages: allNodesCache.length ? 1 : 0 };
        nodeTableState.page = Number(nodePaginationMeta.page) || 1;
        nodeSummaryCache = data.summary || {};
        topLatencyNodesCache = Array.isArray(data.top_latency_nodes) ? data.top_latency_nodes : [];
        regionStatsCache = data.region_stats || {};
        regionHealthyCache = data.region_healthy || {};
        updateSweepProgress(data.probe_sweep);
        updateDashboardStats(data);
        renderNodes();
        document.getElementById('lastUpdate').textContent = `Sync: ${new Date().toLocaleTimeString()}`;
        fetchSubscriptionStatus();
      } catch(e) { console.error('Failed to refresh dashboard:', e); }
    }

    function updateSweepProgress(sweep) {
      if (isProbing) return;
      const wrap = document.getElementById('probeProgress');
      if (sweep && sweep.active && sweep.total > 0) {
        document.getElementById('probeStats').textContent = `${sweep.done}/${sweep.total}`;
        document.getElementById('probeSuccess').textContent = sweep.available;
        document.getElementById('probeFail').textContent = sweep.failed;
        document.getElementById('probeProgressFill').style.width = `${sweep.done / sweep.total * 100}%`;
        wrap.classList.add('show');
        sweepWasActive = true;
      } else if (sweepWasActive) {
        document.getElementById('probeProgressFill').style.width = '100%';
        sweepWasActive = false;
        setTimeout(() => { if (!isProbing) wrap.classList.remove('show'); }, 1500);
      }
    }

    function updateDashboardStats(data) {
      const summary = data.summary || nodeSummaryCache || {};
      const total = Number(summary.total_nodes) || 0;
      const healthy = Number(summary.healthy_nodes) || 0;
      const unavailable = Number(summary.unavailable_nodes ?? summary.blacklisted_nodes) || 0;
      const unknown = Number(summary.unknown_nodes ?? Math.max(0, total - healthy - unavailable)) || 0;
      document.getElementById('healthyNodeRate').textContent = total > 0
        ? tr('全部节点的 {rate}% · {unknown} 个未知', {rate: (100 * healthy / total).toFixed(1), unknown})
        : tr('暂无节点');
      document.getElementById('totalNodes').textContent = Number(summary.total_nodes) || 0;
      document.getElementById('healthyNodes').textContent = Number(summary.healthy_nodes) || 0;
      document.getElementById('activeConnections').textContent = Number(summary.active_connections) || 0;
      document.getElementById('blacklistedNodes').textContent = unavailable;

      if(document.getElementById('dashboardTab').classList.contains('active')) {
        updateDashboardCharts();
      }
    }

    function initEchart(id) {
      const el = document.getElementById(id);
      if(!el) return null;
      try {
        let chart = echarts.getInstanceByDom(el);
        if(!chart) chart = echarts.init(el, null, { renderer: 'canvas' });
        return chart;
      } catch (error) {
        console.error(`Failed to initialize chart ${id}:`, error && error.stack ? error.stack : error);
        return null;
      }
    }

    function getCssVar(name) { return getComputedStyle(document.documentElement).getPropertyValue(name).trim(); }

    function getRegionChartStyle(region, stat) {
      const total = Number(stat && stat.total) || 0;
      const healthy = Math.max(0, Number(stat && stat.healthy) || 0);
      const healthyRatio = total > 0 ? Math.min(1, healthy / total) : 0;
      return {
        color: REGION_CHART_COLORS[region] || '#0891b2',
        opacity: 0.55 + (healthyRatio * 0.45)
      };
    }

    function setChartOption(chart, option, chartId) {
      if (!chart) return;
      try {
        chart.setOption(option);
      } catch (error) {
        console.error(`Failed to update chart ${chartId}:`, error && error.stack ? error.stack : error);
      }
    }

    function ensureTrafficChart() {
      if (trafficChartInst) return;
      const chart = initEchart('trafficChart');
      if (!chart) return;
      trafficChartInst = chart;
      setChartOption(chart, {
        backgroundColor: 'transparent',
        textStyle: { fontFamily: CHART_FONT_FAMILY },
        tooltip: { trigger: 'axis', backgroundColor: getCssVar('--bg-panel'), borderColor: getCssVar('--border'), textStyle: { color: getCssVar('--text-main') },
          formatter: function(p) {
            return p[0].axisValue + '<br/>' + p.map(s => s.marker + s.seriesName + ': ' + formatBytes(s.value) + '/s').join('<br/>');
          }
        },
        legend: { top: 'bottom', textStyle: { color: getCssVar('--text-muted') } },
        grid: { left: '4%', right: '4%', bottom: '15%', top: '15%', containLabel: true },
        xAxis: { type: 'category', boundaryGap: false, data: trafficTime, axisLine: { lineStyle: { color: getCssVar('--border') } }, axisLabel: { color: getCssVar('--text-muted') } },
        yAxis: { type: 'value', splitLine: { lineStyle: { color: getCssVar('--border'), type: 'dashed' } }, axisLabel: { color: getCssVar('--text-muted'), formatter: (v) => formatBytes(v) + '/s' } },
        series: [
          { name: tr('上行'), type: 'line', smooth: true, symbol: 'none', lineStyle: { width: 2, color: getCssVar('--chart-primary') }, areaStyle: { color: getCssVar('--chart-primary'), opacity: 0.12 }, data: trafficDataUp },
          { name: tr('下行'), type: 'line', smooth: true, symbol: 'none', lineStyle: { width: 2, type: 'dashed', color: getCssVar('--chart-secondary') }, areaStyle: { color: getCssVar('--chart-secondary'), opacity: 0.08 }, data: trafficDataDown }
        ]
      }, 'trafficChart');
      connectTrafficSSE();
    }

    function updateDashboardCharts() {
      if(!regionChartInst) regionChartInst = initEchart('regionChart');
      if(!latencyChartInst) latencyChartInst = initEchart('latencyChart');
      ensureTrafficChart();


      // 1. Region Chart Data
      const regionStats = {};
      Object.entries(regionStatsCache).forEach(([region, total]) => {
        const normalized = String(region || 'other').toUpperCase();
        const healthyEntry = Object.entries(regionHealthyCache).find(([key]) => String(key).toUpperCase() === normalized);
        regionStats[normalized] = {
          total: Number(total) || 0,
          healthy: healthyEntry ? (Number(healthyEntry[1]) || 0) : 0
        };
      });

      const regionData = Object.keys(regionStats).map(k => ({
        name: k,
        value: regionStats[k].total,
        itemStyle: getRegionChartStyle(k, regionStats[k])
      }));

      setChartOption(regionChartInst, {
        backgroundColor: 'transparent',
        textStyle: { fontFamily: CHART_FONT_FAMILY },
        tooltip: { trigger: 'item',
          backgroundColor: getCssVar('--bg-panel'), borderColor: getCssVar('--border'), textStyle: { color: getCssVar('--text-main') },
          formatter: (params) => {
             const stat = regionStats[params.name];
             return `${params.name}<br/>${tr('区域总数')}: ${stat.total}<br/>${tr('区域可用')}: ${stat.healthy}`;
          }
        },
        legend: { bottom: 0, textStyle: { color: getCssVar('--text-muted') } },
        series: [{
          name: tr('地域'), type: 'pie', radius: ['36%', '64%'], center: ['50%', '42%'],
          itemStyle: { borderRadius: 10, borderColor: getCssVar('--bg-panel'), borderWidth: 2 },
          label: { show: false, position: 'center' },
          emphasis: { label: { show: true, fontSize: '20', fontWeight: 'bold', color: getCssVar('--text-main') } },
          data: regionData
        }]
      }, 'regionChart');

      // 2. Latency Chart Data
      const sorted = topLatencyNodesCache.length ? topLatencyNodesCache : allNodesCache.filter(n => n.last_latency_ms > 0 && !n.blacklisted).sort((a,b) => a.last_latency_ms - b.last_latency_ms).slice(0, 10);
      const latencyX = sorted.map(getChartNodeDisplayName);
      const latencyY = sorted.map(n => n.last_latency_ms);

      setChartOption(latencyChartInst, {
        backgroundColor: 'transparent',
        textStyle: { fontFamily: CHART_FONT_FAMILY },
        tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' }, backgroundColor: getCssVar('--bg-panel'), borderColor: getCssVar('--border'), textStyle: { color: getCssVar('--text-main') } },
        grid: { left: '3%', right: '4%', bottom: '5%', top: '25%', containLabel: true },
        xAxis: { type: 'value', splitLine: { lineStyle: { color: getCssVar('--border'), type: 'dashed' } }, axisLabel: { color: getCssVar('--text-muted'), formatter: '{value} ms' } },
        yAxis: { type: 'category', data: latencyX.slice().reverse(), axisLabel: { color: getCssVar('--text-muted'), width: 110, overflow: 'truncate', fontFamily: CHART_FONT_FAMILY } },
        series: [{
          name: tr('延迟'), type: 'bar', data: latencyY.slice().reverse(),
          itemStyle: {
            color: new echarts.graphic.LinearGradient(1, 0, 0, 0, [
              { offset: 0, color: getCssVar('--chart-secondary') },
              { offset: 1, color: getCssVar('--chart-primary') }
            ]),
            borderRadius: [0, 4, 4, 0]
          }
        }]
      }, 'latencyChart');
    }

    function formatBytes(bytes, decimals = 2) {
      if (!bytes || isNaN(bytes) || bytes <= 0) return '0 B';
      const k = 1024;
      const dm = decimals < 0 ? 0 : decimals;
      const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
      if (bytes < k) return parseFloat(bytes.toFixed(dm)) + ' B';

      const i = Math.floor(Math.log(bytes) / Math.log(k));
      const safeI = Math.min(i, sizes.length - 1);
      return parseFloat((bytes / Math.pow(k, safeI)).toFixed(dm)) + ' ' + sizes[safeI];
    }

    function connectTrafficSSE() {
      if (trafficSource || trafficRetryTimer) return;
      trafficSource = new EventSource('/api/traffic');
      trafficSource.onmessage = function(e) {
        try {
          const d = JSON.parse(e.data);
          const up = d.up || 0;
          const down = d.down || 0;
          const now = new Date().toLocaleTimeString([], {hour12: false});
          trafficRetryAttempts = 0;
          trafficTime.push(now);
          trafficDataUp.push(up);
          trafficDataDown.push(down);

          if (trafficTime.length > 60) { trafficTime.shift(); trafficDataUp.shift(); trafficDataDown.shift(); }

          // Update live speed stat panels
          document.getElementById('liveUpSpeed').textContent = formatBytes(up) + '/s';
          document.getElementById('liveDownSpeed').textContent = formatBytes(down) + '/s';

          if(trafficChartInst) {
            trafficChartInst.setOption({ xAxis: { data: trafficTime }, series: [{ data: trafficDataUp }, { data: trafficDataDown }] });
          }
        } catch(err){}
      };
      trafficSource.onerror = function() {
        trafficSource.close();
        trafficSource = null;
        trafficRetryAttempts++;
        const retryDelay = Math.min(60000, 2000 * Math.pow(2, Math.min(trafficRetryAttempts - 1, 5)));
        trafficRetryTimer = setTimeout(() => {
          trafficRetryTimer = null;
          connectTrafficSSE();
        }, retryDelay);
      };
    }

    function filterByRegion(region) {
      if (currentRegionFilter !== region) nodeTableState.page = 1;
      currentRegionFilter = region;
      document.querySelectorAll('.region-btn').forEach(btn => btn.classList.toggle('active', btn.dataset.region === region));
      refresh();
    }

    function setNodeSearch(value) {
      nodeTableState.search = normalizeNodeName(value).trim().toLocaleLowerCase();
      nodeTableState.page = 1;
      clearTimeout(nodeSearchTimer);
      nodeSearchTimer = setTimeout(refresh, 300);
    }

    function setNodePageSize(value) {
      nodeTableState.pageSize = Math.max(1, Math.min(500, parseInt(value, 10) || 50));
      nodeTableState.page = 1;
      refresh();
    }

    function changeNodePage(delta) {
      const pageCount = Math.max(1, Number(nodePaginationMeta.total_pages) || 1);
      nodeTableState.page = Math.min(pageCount, Math.max(1, nodeTableState.page + delta));
      refresh();
    }

    function sortNodeTable(key) {
      if (nodeTableState.sortKey === key) nodeTableState.sortDir = nodeTableState.sortDir === 'asc' ? 'desc' : 'asc';
      else { nodeTableState.sortKey = key; nodeTableState.sortDir = key === 'name' || key === 'region' ? 'asc' : 'desc'; }
      nodeTableState.page = 1;
      refresh();
    }

    function nodeSortValue(node, key) {
      switch (key) {
        case 'status': return getNodeStatusRank(node);
        case 'region': return getNodeRegion(node);
        case 'name': return getNodeDisplayName(node);
        case 'port': return Number(node.port) || null;
        case 'latency': return Number(node.last_latency_ms) > 0 ? Number(node.last_latency_ms) : null;
        case 'connections': return Number(node.active_connections) || 0;
        case 'failures': return Number(node.failure_count) || 0;
        default: return getNodeDisplayName(node);
      }
    }

    function renderNodes() {
      const tbody = document.getElementById('nodesTableBody');
      const nodes = allNodesCache;
      const total = Number(nodePaginationMeta.total_items) || 0;
      const pageCount = Math.max(1, Number(nodePaginationMeta.total_pages) || 1);
      const start = (nodeTableState.page - 1) * nodeTableState.pageSize;
      updatePagination('node', total, nodeTableState, start, pageCount);
      updateSortIndicators('nodes', nodeTableState);
      if (!nodes.length) { tbody.innerHTML = ''; document.getElementById('emptyState').style.display='block'; return; }
      document.getElementById('emptyState').style.display='none';
      const canOperate = (ROLE_RANK[currentRole] || 0) >= ROLE_RANK.operator;
      tbody.innerHTML = nodes.map(n => {
        const ms = Number(n.last_latency_ms) || -1;
        const score = Math.max(0, Math.min(100, Number(n.quality_score) || 0));
        let badge = '', statusText = '';
        if (n.blacklisted) { badge = 'badge-error'; statusText = tr('拉黑 Blocked'); }
        else if (n.cooling_down) { badge = 'badge-warning'; statusText = tr('冷却 Cooling'); }
        else if (!n.initial_check_done) { badge = 'badge-offline'; statusText = tr('未测试 Unknown'); }
        else if (!n.available) { badge = 'badge-error'; statusText = tr('异常 Error'); }
        else { badge = 'badge-healthy'; statusText = tr('在线 Healthy'); }
        const actions = canOperate ? `
            <button type="button" class="btn btn-sm" onclick="probeNode(${inlineStringArg(n.tag)})">${iconMarkup('search')}<span>${tr('探测')}</span></button>
            ${(n.blacklisted || n.cooling_down) ? `<button type="button" class="btn btn-sm btn-primary" onclick="releaseNode(${inlineStringArg(n.tag)})">${iconMarkup('unlock')}<span>${tr(n.cooling_down && !n.blacklisted ? '解除冷却' : '解封')}</span></button>` : `<button type="button" class="btn btn-sm btn-danger" onclick="blacklistNode(${inlineStringArg(n.tag)})">${iconMarkup('ban')}<span>${tr('拉黑')}</span></button>`}` : `<span class="badge badge-offline">${tr('只读')}</span>`;
        return `<tr>
          <td><span class="badge ${badge}">${statusText}</span></td>
          <td><span style="display:inline-flex;align-items:center;gap:6px">${iconMarkup('map')} ${getNodeRegion(n).toUpperCase()}</span></td>
          <td><div style="font-weight:500">${escapeHtml(getNodeDisplayName(n))}</div><div style="font-size:11px;color:var(--text-muted)">${escapeHtml(n.tag)}</div></td>
          <td class="tt-mono">${n.port || '-'}</td>
          <td><div style="display:flex;align-items:center;gap:8px"><span class="tt-mono" style="width:48px">${ms>=0 ? ms+'ms' : '-'}</span><div class="lat-bar"><div class="lat-fill" style="width:${score}%;background:${score >= 80 ? 'var(--success)' : (score >= 55 ? 'var(--warning)' : 'var(--error)')}"></div></div><span class="tt-mono" title="综合质量分">${score.toFixed(0)}</span></div></td>
          <td class="tt-mono">${n.active_connections||0}</td>
          <td class="tt-mono" style="color:${n.failure_count>0 ? 'var(--error)' : 'inherit'}">${n.failure_count||0}</td>
          <td>${actions}</td>
        </tr>`;
      }).join('');
    }

    async function probeNode(tag) {
      showToast(tr('探测中...'));
      try {
        const r = await fetch('/api/nodes/'+encodeURIComponent(tag)+'/probe', {method:'POST'});
        const d = await readAPIJSON(r, '请求失败');
        showToast(`Latency: ${d.latency_ms}ms`); refresh();
      } catch(e) { showToast(e.message, 'error'); }
    }

    async function releaseNode(tag) {
      try {
        const r = await fetch('/api/nodes/'+encodeURIComponent(tag)+'/release', {method:'POST'});
        await requireAPIResponse(r, '请求失败');
        showToast(tr('已解封')); refresh();
      } catch(e) { showToast(e.message, 'error'); }
    }

    async function blacklistNode(tag) {
      if(!confirm(tr('确定拉黑该节点 24 小时？'))) return;
      try {
        const r = await fetch('/api/nodes/'+encodeURIComponent(tag)+'/blacklist', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({duration:'24h'})});
        const d = await readAPIJSON(r, '拉黑失败');
        showToast(localizedAPIMessage(d.message, '操作成功')); refresh();
      } catch(e) { showToast(e.message || tr('拉黑失败'), 'error'); }
    }

    // Probe All
    let isProbing = false;
    async function probeAllNodes() {
      if(isProbing) return; isProbing = true;
      document.getElementById('probeProgress').classList.add('show');
      document.getElementById('probeProgressFill').style.width = '0%';
      let total = 0, current = 0, suc = 0, fail = 0;
      try {
        const res = await fetch('/api/nodes/probe-all', { method: 'POST' });
        await requireAPIResponse(res, '请求失败');
        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buf = '';
        while(true) {
          const {done, value} = await reader.read();
          if(done) break;
          buf += decoder.decode(value, {stream:true});
          const lines = buf.split('\n');
          buf = lines.pop() || '';
          for(const line of lines) {
            if(line.startsWith('data: ')) {
               try {
                 const d = JSON.parse(line.slice(6));
                 if(d.type === 'start') { total = d.total; }
                 else if(d.type === 'progress') {
                   current = d.current;
                   if(Number.isFinite(d.total)) total = d.total;
                   if(Number.isFinite(d.success)) suc = d.success;
                   else if(d.error) fail++; else suc++;
                   if(Number.isFinite(d.failed)) fail = d.failed;
                   document.getElementById('probeProgressFill').style.width = `${d.progress}%`;
                   document.getElementById('probeStats').textContent = `${current}/${total}`;
                   document.getElementById('probeSuccess').textContent = suc;
                   document.getElementById('probeFail').textContent = fail;
                 } else if (d.type === 'complete') {
                   showToast(tr('探测完成：成功 {success}，失败 {failed}', {success: d.success, failed: d.failed}));
                 }
               } catch(e){}
            }
          }
        }
      } catch(e){ showToast(e.message, 'error'); }
      finally {
        isProbing = false;
        setTimeout(()=>{ document.getElementById('probeProgress').classList.remove('show'); refresh(); }, 2000);
      }
    }

    async function exportNodes() {
      try {
        const res = await fetch('/api/export');
        await requireAPIResponse(res, '导出失败');
        const blob = await res.blob();
        const url = window.URL.createObjectURL(blob);
        const a = document.createElement('a'); a.href = url; a.download = 'nodes.txt';
        document.body.appendChild(a); a.click(); document.body.removeChild(a); window.URL.revokeObjectURL(url);
      } catch(e) { showToast(tr('导出失败'), 'error');}
    }

    // Subscriptions
    async function fetchSubscriptionStatus() {
      try {
        const res = await fetch('/api/subscription/status');
        const d = await readAPIJSON(res, '请求失败');
        const btn = document.getElementById('refreshSubBtn');
        if(!d.enabled) { btn.style.display='none'; return; }
        btn.style.display='inline-flex';
        const skipped = Math.max(0, Number(d.skipped_nodes) || 0);
        document.getElementById('subscriptionStatus').textContent = `[Sub: ${d.node_count}${skipped ? ` · Skip: ${skipped}` : ''}]`;
        const trend = document.getElementById('subscriptionTrend');
        trend.textContent = d.is_refreshing ? 'Syncing...' : (d.last_error ? 'ERR' : (skipped ? `SKIP ${skipped}` : 'OK'));
        trend.title = skipped && Array.isArray(d.node_failures) ? d.node_failures.map(item => `${item.name || item.tag}: ${item.error}`).join('\n') : '';
      } catch(e){ console.error('Failed to load subscription status:', e); }
    }

    async function refreshSubscription() {
      try {
        const statusResponse = await fetch('/api/subscription/status');
        const s = await readAPIJSON(statusResponse, '请求失败');
        if(s.nodes_modified && !confirm(tr('本地节点已修改，刷新将覆盖，继续？'))) return;
        showToast(tr('开始刷新订阅...'));
        const r = await fetch('/api/subscription/refresh', {method:'POST'});
        const res = await readAPIJSON(r, '请求失败');
        showToast(tr('成功获取 {count} 个节点', {count: res.node_count})); refresh();
      } catch(e) { showToast(tr('刷新失败：{error}', {error: e.message || e}), 'error'); }
    }

    // Manage Nodes
    function setConfigNodeSearch(value) {
      configNodeTableState.search = normalizeNodeName(value).trim().toLocaleLowerCase();
      configNodeTableState.page = 1;
      renderConfigNodes();
    }

    function setConfigNodePageSize(value) {
      configNodeTableState.pageSize = Math.max(1, parseInt(value, 10) || 50);
      configNodeTableState.page = 1;
      renderConfigNodes();
    }

    function changeConfigNodePage(delta) {
      configNodeTableState.page += delta;
      renderConfigNodes();
    }

    function syncConfigNodesRevealButton() {
      const button = document.getElementById('configNodesRevealAll');
      if (!button) return;
      const label = tr(configNodesRevealAll ? '隐藏全部 URI' : '显示全部 URI');
      button.setAttribute('aria-label', label);
      button.setAttribute('title', label);
      button.setAttribute('aria-pressed', String(configNodesRevealAll));
      const use = button.querySelector('use');
      if (use) use.setAttribute('href', configNodesRevealAll ? '#i-eye-off' : '#i-eye');
    }

    function renderConfigNodes() {
      const query = configNodeTableState.search;
      let nodes = configNodes;
      if (query) {
        nodes = nodes.filter(node => [node.name, node.uri, node.port, node.source]
          .map(normalizeNodeName).join(' ').toLocaleLowerCase().includes(query));
      }
      const page = pageSlice(nodes, configNodeTableState);
      updatePagination('configNode', nodes.length, configNodeTableState, page.start, page.pageCount);
      syncConfigNodesRevealButton();
      const tbody = document.getElementById('configNodesTableBody');
      tbody.innerHTML = page.rows.length ? page.rows.map(n => {
        const revealed = n._uriRevealed === true;
        const uriText = revealed ? n.uri : maskNodeURI(n.uri);
        const revealLabel = tr(revealed ? '隐藏敏感信息' : '显示敏感信息');
        const copyLabel = tr('复制 URI');
        return `
        <tr>
          <td><strong>${escapeHtml(normalizeNodeName(n.name))}</strong></td>
          <td class="tt-mono"><div class="uri-cell"><span class="uri-value">${escapeHtml(uriText)}</span><button type="button" class="btn btn-sm icon-only" onclick="toggleConfigNodeURI(${inlineStringArg(n.id)})" aria-label="${escapeHtml(revealLabel)}" title="${escapeHtml(revealLabel)}" aria-pressed="${revealed}">${iconMarkup(revealed ? 'eye-off' : 'eye')}</button><button type="button" class="btn btn-sm icon-only" onclick="copyConfigNodeURI(${inlineStringArg(n.id)}, this)" aria-label="${escapeHtml(copyLabel)}" title="${escapeHtml(copyLabel)}">${iconMarkup('copy')}</button></div></td>
          <td class="tt-mono">${n.port || '-'}</td>
          <td><span class="badge ${n.source==='subscription'?'badge-warning':'badge-healthy'}">${n.source||'manual'}</span></td>
          <td>
            <button type="button" class="btn btn-sm" onclick="showEditNodeModal(${inlineStringArg(n.id)})">${iconMarkup('edit')}<span>${tr('编辑')}</span></button>
            <button type="button" class="btn btn-sm btn-danger" onclick="deleteNode(${inlineStringArg(n.id)}, ${inlineStringArg(n.name)})">${iconMarkup('trash')}<span>${tr('删除')}</span></button>
          </td>
        </tr>`;
      }).join('') : `<tr><td colspan="5" style="text-align:center;color:var(--text-muted)">${tr('暂无数据')}</td></tr>`;
    }

    async function loadConfigNodes(reveal = configNodesRevealAll) {
      try {
        const res = await fetch(reveal ? '/api/nodes/config?reveal=true' : '/api/nodes/config');
        const data = await readAPIJSON(res, '加载数据失败');
        configNodes = (data.nodes || []).map(node => ({...node, _uriRevealed: Boolean(reveal)}));
        renderConfigNodes();
        document.getElementById('subscriptionWarning').style.display = configNodes.some(n=>n.source==='subscription') ? 'block' : 'none';
        return true;
      } catch(e){ showToast(e.message, 'error'); return false; }
    }

    async function toggleAllConfigNodeURIs() {
      const previous = configNodesRevealAll;
      configNodesRevealAll = !previous;
      syncConfigNodesRevealButton();
      if (!(await loadConfigNodes(configNodesRevealAll))) {
        configNodesRevealAll = previous;
        syncConfigNodesRevealButton();
      }
    }

    async function getConfigNodeURIForCopy(id) {
      const index = configNodes.findIndex(node => String(node.id) === String(id));
      if (index < 0) throw new Error(tr('加载节点失败'));
      const cachedURI = String(configNodes[index].uri || '');
      if (cachedURI && !cachedURI.includes('********')) return cachedURI;
      const response = await fetch('/api/nodes/config/' + encodeURIComponent(id));
      const payload = await readAPIJSON(response, '加载节点失败');
      if (!payload.node || !payload.node.uri) throw new Error(tr('加载节点失败'));
      configNodes[index] = {...configNodes[index], ...payload.node, _uriRevealed: configNodes[index]._uriRevealed === true};
      return payload.node.uri;
    }

    async function writeClipboardText(value) {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(value);
        return;
      }
      const textarea = document.createElement('textarea');
      textarea.value = value;
      textarea.setAttribute('readonly', '');
      textarea.style.position = 'fixed';
      textarea.style.opacity = '0';
      document.body.appendChild(textarea);
      textarea.select();
      const copied = document.execCommand('copy');
      textarea.remove();
      if (!copied) throw new Error(tr('复制 URI 失败'));
    }

    async function copyConfigNodeURI(id, button) {
      const use = button && button.querySelector('use');
      const copyLabel = tr('复制 URI');
      if (button) {
        button.disabled = true;
        button.setAttribute('aria-busy', 'true');
      }
      try {
        const uri = await getConfigNodeURIForCopy(id);
        await writeClipboardText(uri);
        if (use) use.setAttribute('href', '#i-check');
        if (button) {
          button.setAttribute('aria-label', tr('URI 已复制'));
          button.setAttribute('title', tr('URI 已复制'));
        }
        showToast(tr('URI 已复制'));
        setTimeout(() => {
          if (!button || !document.contains(button)) return;
          const currentUse = button.querySelector('use');
          if (currentUse) currentUse.setAttribute('href', '#i-copy');
          button.setAttribute('aria-label', copyLabel);
          button.setAttribute('title', copyLabel);
        }, 1500);
      } catch (error) {
        showToast(error.message || tr('复制 URI 失败'), 'error');
      } finally {
        if (button) {
          button.disabled = false;
          button.removeAttribute('aria-busy');
        }
      }
    }

    async function toggleConfigNodeURI(id) {
      const index = configNodes.findIndex(node => String(node.id) === String(id));
      if (index < 0) return;
      if (configNodes[index]._uriRevealed) {
        configNodes[index]._uriRevealed = false;
        configNodesRevealAll = false;
        renderConfigNodes();
        return;
      }
      try {
        const response = await fetch('/api/nodes/config/' + encodeURIComponent(id));
        const payload = await readAPIJSON(response, '加载节点失败');
        if (!payload.node) throw new Error(tr('加载节点失败'));
        configNodes[index] = {...configNodes[index], ...payload.node, _uriRevealed: true};
        renderConfigNodes();
      } catch (error) {
        showToast(error.message || tr('加载节点失败'), 'error');
      }
    }

    function showAddNodeModal() { isEditMode=false; document.getElementById('nodeModalTitle').textContent=tr('添加节点'); document.getElementById('nodeName').value=''; document.getElementById('nodeUri').value=''; document.getElementById('nodePort').value=''; document.getElementById('nodeModalOverlay').classList.add('show');}
    async function showEditNodeModal(id) {
      try {
        const response = await fetch('/api/nodes/config/' + encodeURIComponent(id));
        const payload = await readAPIJSON(response, '加载节点失败');
        if (!payload.node) throw new Error(tr('加载节点失败'));
        const n = payload.node;
        isEditMode=true; document.getElementById('nodeModalTitle').textContent=tr('编辑节点');
        document.getElementById('nodeName').value=n.name; document.getElementById('nodeUri').value=n.uri; document.getElementById('nodePort').value=n.port||''; document.getElementById('nodeEditName').value=n.id;
        document.getElementById('nodeModalOverlay').classList.add('show');
      } catch (error) {
        showToast(error.message || tr('加载节点失败'), 'error');
      }
    }
    async function handleNodeSubmit(e) {
      e.preventDefault();
      const p = { name: document.getElementById('nodeName').value, uri: document.getElementById('nodeUri').value, port: parseInt(document.getElementById('nodePort').value)||0 };
      try {
        const editName = document.getElementById('nodeEditName').value;
        const url = isEditMode ? '/api/nodes/config/'+encodeURIComponent(editName) : '/api/nodes/config';
        const method = isEditMode ? 'PUT' : 'POST';
        const r = await fetch(url, {method, headers:{'Content-Type':'application/json'}, body:JSON.stringify(p)});
        await readAPIJSON(r, '请求失败');
        document.getElementById('nodeModalOverlay').classList.remove('show'); showToast(tr('成功')); loadConfigNodes();
      } catch(e){ showToast(e.message, 'error'); }
    }
    async function deleteNode(id, name) {
      const confirmed = await requestConfirmation(
        tr('确认删除'),
        `${tr('确定删除节点 {name}？', {name})} ${tr('删除节点后需要重载核心才会从运行时移除。')}`,
        tr('确认删除')
      );
      if (!confirmed) return;
      try {
        const r = await fetch('/api/nodes/config/'+encodeURIComponent(id), {method:'DELETE'});
        await readAPIJSON(r, '请求失败');
        showToast(tr('删除成功')); loadConfigNodes();
      } catch(e){ showToast(e.message, 'error'); }
    }
    async function triggerReload() {
      if(!confirm(tr('立即重载核心？'))) return;
      try { const r = await fetch('/api/reload', {method:'POST'}); await readAPIJSON(r, '请求失败'); showToast(tr('重载成功')); refresh(); } catch(e){ showToast(e.message, 'error'); }
    }

    // Debug
    function setDebugNodeSearch(value) {
      debugTableState.search = normalizeNodeName(value).trim().toLocaleLowerCase();
      debugTableState.page = 1;
      renderDebugNodes();
    }

    function setDebugNodePageSize(value) {
      debugTableState.pageSize = Math.max(1, parseInt(value, 10) || 50);
      debugTableState.page = 1;
      renderDebugNodes();
    }

    function changeDebugNodePage(delta) {
      debugTableState.page += delta;
      renderDebugNodes();
    }

    function sortDebugTable(key) {
      if (debugTableState.sortKey === key) debugTableState.sortDir = debugTableState.sortDir === 'asc' ? 'desc' : 'asc';
      else { debugTableState.sortKey = key; debugTableState.sortDir = key === 'name' ? 'asc' : 'desc'; }
      debugTableState.page = 1;
      renderDebugNodes();
    }

    function debugNodeSuccessRate(node) {
      const calls = (node.success_count || 0) + (node.failure_count || 0);
      return calls ? (node.success_count || 0) / calls * 100 : 0;
    }

    function debugSortValue(node, key) {
      switch (key) {
        case 'successRate': return debugNodeSuccessRate(node);
        case 'calls': return (node.success_count || 0) + (node.failure_count || 0);
        case 'connections': return Number(node.active_connections) || 0;
        default: return getNodeDisplayName(node);
      }
    }

    function renderDebugNodes() {
      const query = debugTableState.search;
      let nodes = window._debugNodes || [];
      if (query) nodes = nodes.filter(node => [getNodeDisplayName(node), node.tag].map(normalizeNodeName).join(' ').toLocaleLowerCase().includes(query));
      nodes = sortRows(nodes, debugTableState, debugSortValue);
      const page = pageSlice(nodes, debugTableState);
      updatePagination('debugNode', nodes.length, debugTableState, page.start, page.pageCount);
      updateSortIndicators('debug', debugTableState);
      const tb = document.getElementById('debugNodesTableBody');
      tb.innerHTML = page.rows.length ? page.rows.map(n => {
        const sr = debugNodeSuccessRate(n).toFixed(1);
        const tl = (n.timeline||[]).map(t => `<div class="tl-dot ${t.success?'success':'error'}" title="${t.latency_ms}ms"></div>`).join('');
        return `<tr>
          <td><strong>${escapeHtml(getNodeDisplayName(n))}</strong><br><span style="font-size:11px;color:var(--text-muted)">${escapeHtml(n.tag)}</span></td>
          <td class="tt-mono ${sr>80?'':'badge-error'}">${sr}%</td>
          <td class="tt-mono"><span style="color:var(--success)">${n.success_count||0}</span> / <span style="color:var(--error)">${n.failure_count||0}</span></td>
          <td class="tt-mono">${n.active_connections||0}</td>
          <td><div class="timeline">${tl||'-'}</div></td>
          <td><button type="button" class="btn btn-sm btn-danger" onclick="clearDiagnostic(${inlineStringArg(n.tag)}, ${inlineStringArg(getNodeDisplayName(n))})">${iconMarkup('trash')}<span>${tr('删除')}</span></button></td>
        </tr>`;
      }).join('') : `<tr><td colspan="6" style="text-align:center;color:var(--text-muted)">${tr('暂无数据')}</td></tr>`;
    }

    async function clearDiagnostic(tag, name) {
      const confirmed = await requestConfirmation(
        tr('确认删除'),
        `${tr('删除诊断记录 {name}？', {name})} ${tr('只会清除诊断计数和时间线，不会删除节点或改变路由状态。')}`,
        tr('确认删除')
      );
      if (!confirmed) return;
      try {
        const response = await fetch('/api/debug/' + encodeURIComponent(tag), {method: 'DELETE'});
        await readAPIJSON(response, '请求失败');
        showToast(tr('诊断记录已删除'));
        await loadDebugData();
      } catch (error) {
        showToast(error.message || tr('请求失败'), 'error');
      }
    }

    async function clearAllDiagnostics() {
      const confirmed = await requestConfirmation(
        tr('清空记录'),
        `${tr('清空全部诊断记录？')} ${tr('这会清除所有节点的诊断计数和时间线，不会删除节点。')}`,
        tr('清空记录')
      );
      if (!confirmed) return;
      try {
        const response = await fetch('/api/debug', {method: 'DELETE'});
        await readAPIJSON(response, '请求失败');
        showToast(tr('诊断记录已清空'));
        await loadDebugData();
      } catch (error) {
        showToast(error.message || tr('请求失败'), 'error');
      }
    }

    async function loadDebugData() {
      try {
        const res = await fetch('/api/debug');
        const d = await readAPIJSON(res, '加载数据失败');
        document.getElementById('debugTotalCalls').textContent = d.total_calls||0;
        document.getElementById('debugTotalSuccess').textContent = d.total_success||0;
        document.getElementById('debugSuccessRate').textContent = d.total_calls > 0 ? (d.success_rate||0).toFixed(1)+'%' : '—';
        window._debugNodes = d.nodes || [];
        debugSummaryCache = {success_rate: Number(d.success_rate) || 0, total_calls: Number(d.total_calls) || 0};
        renderDebugNodes();
        updateDebugCharts();

      } catch(e){ showToast(e.message, 'error'); }
    }

    function updateDebugCharts() {
      if(!successRateChartInst) successRateChartInst = initEchart('successRateChart');
      if(!failureChartInst) failureChartInst = initEchart('failureChart');

      // Delayed tab/theme redraws must use the same server summary as the
      // numeric card, including its no-attempts state.
      const rate = debugSummaryCache.success_rate;
      const hasAttempts = debugSummaryCache.total_calls > 0;
      const animate = !window.matchMedia('(prefers-reduced-motion: reduce)').matches;

      successRateChartInst && successRateChartInst.setOption({
        animation: animate,
        backgroundColor: 'transparent',
        textStyle: { fontFamily: CHART_FONT_FAMILY },
        series: [{
          type: 'gauge',
          startAngle: 180, endAngle: 0,
          center: ['50%', '75%'], radius: '90%',
          min: 0, max: 100, splitNumber: 10,
          axisLine: { lineStyle: { width: 12, color: [ [0.7, getCssVar('--error')], [0.9, getCssVar('--chart-secondary')], [1, getCssVar('--chart-primary')] ] } },
          pointer: { icon: 'path://M12.8,0.7l12,40.1H0.7L12.8,0.7z', length: '12%', width: 10, offsetCenter: [0, '-60%'], itemStyle: { color: 'auto' } },
          axisTick: { length: 12, lineStyle: { color: 'auto', width: 2 } },
          splitLine: { length: 20, lineStyle: { color: 'auto', width: 3 } },
          axisLabel: { color: getCssVar('--text-muted'), fontSize: 10, distance: -50 },
          title: { offsetCenter: [0, '-20%'], fontSize: 14, color: getCssVar('--text-muted') },
          detail: { fontSize: 36, offsetCenter: [0, '0%'], valueAnimation: animate, formatter: function (value) { return hasAttempts ? Math.round(value) + '%' : '—'; }, color: 'auto' },
          data: [{ value: rate, name: tr(hasAttempts ? '成功率' : '暂无记录') }]
        }]
      });

      // Top failures
      if (!window._debugNodes) return;
      const failSorted = window._debugNodes.filter(n => (n.failure_count||0) > 0).sort((a,b) => (b.failure_count||0) - (a.failure_count||0)).slice(0, 10);
      const failX = failSorted.map(getChartNodeDisplayName);
      const failY = failSorted.map(n => n.failure_count);

      failureChartInst && failureChartInst.setOption({
        backgroundColor: 'transparent',
        textStyle: { fontFamily: CHART_FONT_FAMILY },
        tooltip: { trigger: 'axis', backgroundColor: getCssVar('--bg-panel'), borderColor: getCssVar('--border'), textStyle: { color: getCssVar('--text-main') } },
        grid: { left: '3%', right: '4%', bottom: '5%', top: '25%', containLabel: true },
        xAxis: { type: 'category', data: failX, axisLabel: { color: getCssVar('--text-muted'), width: 80, overflow: 'truncate', fontFamily: CHART_FONT_FAMILY } },
        yAxis: { type: 'value', splitLine: { lineStyle: { color: getCssVar('--border'), type: 'dashed' } }, axisLabel: { color: getCssVar('--text-muted') } },
        series: [{
          name: tr('调用失败'), type: 'bar', data: failY,
          itemStyle: {
            color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
              { offset: 0, color: getCssVar('--chart-primary') },
              { offset: 1, color: getCssVar('--chart-secondary') }
            ]),
            borderRadius: [4, 4, 0, 0]
          }
        }]
      });
    }

    // Settings
    async function loadBuildInfo() {
      const status = document.getElementById('buildInfoStatus');
      const error = document.getElementById('buildInfoError');
      const capabilities = document.getElementById('buildInfoCapabilities');
      if (!status || !error || !capabilities) return;
      status.className = 'badge badge-offline';
      status.textContent = tr('读取中');
      error.classList.remove('show');
      error.textContent = '';
      capabilities.replaceChildren();
      try {
        const response = await fetch('/api/build-info');
        const info = await readAPIJSON(response, '构建信息加载失败');
        document.getElementById('buildInfoIdentity').textContent = `${info.product || 'ProxyFleet'} ${info.version || 'dev'}`;
        document.getElementById('buildInfoTarget').textContent = `${info.goos || 'unknown'}/${info.goarch || 'unknown'}`;
        document.getElementById('buildInfoCommit').textContent = info.commit || 'unknown';
        document.getElementById('buildInfoGo').textContent = info.go_version || 'unknown';
        document.getElementById('buildInfoProtocols').textContent = Array.isArray(info.protocols) && info.protocols.length ? info.protocols.join(', ') : tr('无');
        const entries = Object.entries(info.capabilities || {}).sort(([left], [right]) => left.localeCompare(right));
        for (const [name, enabled] of entries) {
          const badge = document.createElement('span');
          badge.className = `capability-badge ${enabled ? 'enabled' : 'disabled'}`;
          badge.textContent = `${name.toUpperCase()} ${enabled ? 'ON' : 'OFF'}`;
          capabilities.appendChild(badge);
        }
        if (info.official_release_ready) {
          status.className = 'badge badge-healthy';
          status.textContent = tr('完整发行构建');
          status.title = tr('官方协议能力均已编译');
        } else {
          const missing = Array.isArray(info.missing_features) ? info.missing_features : [];
          status.className = 'badge badge-warning';
          status.textContent = tr('功能受限构建');
          status.title = missing.length ? `${tr('缺少编译能力')}: ${missing.join(', ')}` : tr('部分编译能力不可用');
          error.textContent = status.title;
          error.classList.add('show');
        }
      } catch (buildInfoError) {
        status.className = 'badge badge-error';
        status.textContent = tr('读取失败');
        error.textContent = buildInfoError.message || tr('构建信息加载失败');
        error.classList.add('show');
      }
    }

    async function loadSettingsPage() {
      subscriptionSettingsLoaded = false;
      _savedSubSnapshot = '';
      subscriptionETag = '';
      loadBuildInfo();
      try {
        const r = await fetch('/api/settings');
        const d = await readAPIJSON(r, '加载数据失败');
        settingsETag = r.headers.get('ETag') || '';
        document.getElementById('settingLanguage').value = currentLanguage;
        // General
        document.getElementById('settingMode').value = d.mode || 'pool';
        document.getElementById('settingExternalIP').value = d.external_ip||'';
        document.getElementById('settingProbeTarget').value = d.probe_target||'';
        document.getElementById('settingSkipCertVerify').checked = d.skip_cert_verify||false;
        document.getElementById('settingProbeConcurrency').value = d.probe_concurrency || 32;
        document.getElementById('settingProbeMode').value = d.probe_mode || 'all';
        document.getElementById('settingProbeInterval').value = formatDurationForInput(d.probe_interval || '5m');
        document.getElementById('settingProbeTimeout').value = formatDurationForInput(d.probe_timeout || '10s');
        document.getElementById('settingProbeBatchSize').value = d.probe_batch_size || 100;
        toggleProbePolicySettings();
        // Endpoint Manager (legacy listener is accepted as an API fallback)
        const ls = d.listener || {};
        window.proxyFleetProfiles?.load(d.profiles || []);
        const endpoints = Array.isArray(d.endpoints) && d.endpoints.length ? d.endpoints : [{
          name: 'default', enabled: true, address: ls.address || '127.0.0.1', port: ls.port || 2323,
          username: ls.username || '', password: ls.password || '', profile: '', status: 'waiting',
        }];
        window.proxyFleetEndpoints?.load(endpoints, d.profiles || []);
        // Multi-port
        const mp = d.multi_port || {};
        document.getElementById('settingMPAddr').value = mp.address || '';
        document.getElementById('settingMPBasePort').value = mp.base_port || '';
        document.getElementById('settingMPUser').value = mp.username || '';
        document.getElementById('settingMPPass').value = mp.password || '';
        // Pool
        const pl = d.pool || {};
        document.getElementById('settingPoolMode').value = pl.mode || 'random';
        document.getElementById('settingPoolFailure').value = pl.failure_threshold || 3;
        document.getElementById('settingPoolBlacklist').value = pl.blacklist_duration || '24h';
        document.getElementById('settingPoolCooldown').value = pl.transient_cooldown || '1m0s';
        document.getElementById('settingPoolRetry').checked = pl.retry_enabled !== false;
        document.getElementById('settingPoolRetryAttempts').value = pl.retry_attempts || 3;
        document.getElementById('settingPoolFailOpen').checked = pl.fail_open || false;
        document.getElementById('settingLatencySamples').value = pl.latency_sample_size || 4;
        document.getElementById('settingLatencyTolerance').value = pl.latency_tolerance || '50ms';
        const sticky = pl.sticky || {};
        document.getElementById('settingPoolSticky').checked = sticky.enabled || false;
        document.getElementById('settingStickyTTL').value = sticky.ttl || '30m0s';
        document.getElementById('settingStickyMax').value = sticky.max_entries || 4096;
        togglePoolStrategySettings();
        // Management
        const mgmt = d.management || {};
        document.getElementById('settingMgmtListen').value = mgmt.listen || '';
        document.getElementById('settingMgmtPassword').value = mgmt.password || '';
        document.getElementById('settingOperatorPassword').value = mgmt.operator_password || '';
        document.getElementById('settingViewerPassword').value = mgmt.viewer_password || '';
        document.getElementById('settingProbeHealthyInterval').value = formatDurationForInput(mgmt.probe_healthy_interval || '30m');
        document.getElementById('settingProbeFailureRetry').value = formatDurationForInput(mgmt.probe_failure_retry_interval || '1m');
        document.getElementById('settingProbeFailureMax').value = formatDurationForInput(mgmt.probe_failure_max_interval || '1h');
        document.getElementById('settingProbePassiveGrace').value = formatDurationForInput(mgmt.probe_passive_grace || '10m');
        document.getElementById('settingProbeMaxPerHour').value = Number(mgmt.probe_max_per_hour) || 0;
        document.getElementById('settingProbeMaxPerDay').value = Number(mgmt.probe_max_per_day) || 0;
        document.getElementById('settingHistoryEnabled').checked = mgmt.history_enabled !== false;
        document.getElementById('settingHistoryFile').value = mgmt.history_file || 'monitor-history.json';
        document.getElementById('settingHistoryRetention').value = formatDurationForInput(mgmt.history_retention || '24h');
        document.getElementById('settingHistoryInterval').value = formatDurationForInput(mgmt.history_interval || '1m');
        document.getElementById('settingAlertMinAvailable').value = Number(mgmt.alert_min_available) || 0;
        document.getElementById('settingAlertMinRatio').value = Number(mgmt.alert_min_available_ratio) || 0;
        document.getElementById('settingAlertCooldown').value = formatDurationForInput(mgmt.alert_cooldown || '10m');
        document.getElementById('settingAuditFile').value = mgmt.audit_file || 'audit.log';
        // GeoIP
        const geo = d.geoip || {};
        document.getElementById('settingGeoIPEnabled').checked = geo.enabled || false;
        document.getElementById('settingGeoIPDBPath').value = geo.database_path || '';
        document.getElementById('settingGeoIPListen').value = geo.listen || '';
        document.getElementById('settingGeoIPPort').value = geo.port || '';
        document.getElementById('settingGeoIPAutoUpdate').checked = geo.auto_update_enabled || false;
        document.getElementById('settingGeoIPUpdateInterval').value = !geo.auto_update_interval || geo.auto_update_interval === '0s' ? '24h' : geo.auto_update_interval;
        // Log
        const logCfg = d.log || {};
        document.getElementById('settingLogOutput').value = logCfg.output || 'stdout';
        document.getElementById('settingLogMaxSize').value = logCfg.max_size || 50;
        document.getElementById('settingLogMaxBackups').value = logCfg.max_backups || 3;
        document.getElementById('settingLogMaxAge').value = logCfg.max_age || 7;
        document.getElementById('settingLogRotateInterval').value = !logCfg.rotate_interval || logCfg.rotate_interval === '0s' ? '0' : logCfg.rotate_interval;
        document.getElementById('settingLogCompress').checked = logCfg.compress || false;
        const trafficLogCfg = d.traffic_log || {};
        document.getElementById('settingTrafficLogEnabled').checked = trafficLogCfg.enabled === true;
        document.getElementById('settingTrafficLogFile').value = trafficLogCfg.file || 'traffic-log.db';
        document.getElementById('settingTrafficLogRetention').value = formatDurationForInput(trafficLogCfg.retention || '24h');
        document.getElementById('settingTrafficLogMaxEntries').value = Number(trafficLogCfg.max_entries) || 100000;
        document.getElementById('settingTrafficLogRedact').checked = trafficLogCfg.redact_destination !== false;
        toggleLogFileSettings();
        // Subscription
        try {
          const sr = await fetch('/api/subscription/config');
          const sd = await readAPIJSON(sr, '订阅设置加载失败，请刷新页面后重试');
          if (!Array.isArray(sd.subscriptions)) throw new Error('invalid subscription settings response');
          subscriptionETag = sr.headers.get('ETag') || '';
          document.getElementById('settingSubEnabled').checked = sd.enabled || false;
          const iv = sd.interval || '';
          const sel = document.getElementById('settingSubInterval');
          let matched = false;
          for (let opt of sel.options) { if (iv === opt.value || iv === opt.value.replace('m','m0s') || iv.startsWith(opt.value)) { sel.value = opt.value; matched = true; break; } }
          if (!matched) sel.value = '1h';
          const fetchConcurrency = Number(sd.fetch_concurrency) || 16;
          document.getElementById('settingSubFetchConcurrency').value = fetchConcurrency;
          document.getElementById('settingSubAllowPrivate').checked = sd.allow_private_networks === true;
          document.getElementById('settingSubMaxRemovedRatio').value = Number(sd.max_removed_ratio) || 0.5;
          document.getElementById('settingSubMinAvailableRatio').value = Number(sd.min_available_ratio) || 0;
          document.getElementById('settingSubQuarantine').checked = sd.quarantine_new_nodes !== false;
          document.getElementById('settingSubNodeFailurePolicy').value = sd.node_failure_policy === 'strict' ? 'strict' : 'skip';
          const sourceStatusResponse = await fetch('/api/subscription/status');
          const sourceStatus = await readAPIJSON(sourceStatusResponse, '订阅源状态加载失败');
          const sources = Array.isArray(sd.sources) ? sd.sources : (sd.subscriptions || []).map((url, index) => ({name: `source-${index + 1}`, url, enabled: true, refresh_interval: ''}));
          window.proxyFleetSubscriptions?.load(sources, sourceStatus.sources || []);
          _savedSubSnapshot = JSON.stringify({sources: window.proxyFleetSubscriptions?.serialize() || sources, enabled: sd.enabled || false, interval: sel.value, fetch_concurrency: fetchConcurrency, allow_private_networks: sd.allow_private_networks === true, max_removed_ratio: Number(sd.max_removed_ratio) || 0.5, min_available_ratio: Number(sd.min_available_ratio) || 0, quarantine_new_nodes: sd.quarantine_new_nodes !== false, node_failure_policy: sd.node_failure_policy === 'strict' ? 'strict' : 'skip'});
          subscriptionSettingsLoaded = true;
        } catch(e){
          console.error('Failed to load subscription settings:', e);
          showToast(tr('订阅设置加载失败，请刷新页面后重试'), 'error');
        }
        // Take core snapshot after all fields are populated
        _savedCoreSnapshot = _buildCoreSnapshot();
      } catch(e){ console.error('Failed to load settings:', e); showToast(e.message || tr('加载数据失败'), 'error'); }
    }
    function _buildCoreSnapshot() {
      return JSON.stringify({
        profiles: window.proxyFleetProfiles?.serialize() || [],
        endpoints: window.proxyFleetEndpoints?.serialize() || [],
        mode: document.getElementById('settingMode').value,
        external_ip: document.getElementById('settingExternalIP').value,
        probe_target: document.getElementById('settingProbeTarget').value,
        skip_cert_verify: document.getElementById('settingSkipCertVerify').checked,
        probe_concurrency: document.getElementById('settingProbeConcurrency').value,
        probe_mode: document.getElementById('settingProbeMode').value,
        probe_interval: document.getElementById('settingProbeInterval').value,
        probe_timeout: document.getElementById('settingProbeTimeout').value,
        probe_batch_size: document.getElementById('settingProbeBatchSize').value,
        probe_healthy_interval: document.getElementById('settingProbeHealthyInterval').value,
        probe_failure_retry: document.getElementById('settingProbeFailureRetry').value,
        probe_failure_max: document.getElementById('settingProbeFailureMax').value,
        probe_passive_grace: document.getElementById('settingProbePassiveGrace').value,
        probe_max_per_hour: document.getElementById('settingProbeMaxPerHour').value,
        probe_max_per_day: document.getElementById('settingProbeMaxPerDay').value,
        history_enabled: document.getElementById('settingHistoryEnabled').checked,
        history_file: document.getElementById('settingHistoryFile').value,
        history_retention: document.getElementById('settingHistoryRetention').value,
        history_interval: document.getElementById('settingHistoryInterval').value,
        alert_min_available: document.getElementById('settingAlertMinAvailable').value,
        alert_min_ratio: document.getElementById('settingAlertMinRatio').value,
        alert_cooldown: document.getElementById('settingAlertCooldown').value,
        audit_file: document.getElementById('settingAuditFile').value,
        mp_addr: document.getElementById('settingMPAddr').value,
        mp_base_port: document.getElementById('settingMPBasePort').value,
        mp_user: document.getElementById('settingMPUser').value,
        mp_pass: document.getElementById('settingMPPass').value,
        pool_mode: document.getElementById('settingPoolMode').value,
        pool_failure: document.getElementById('settingPoolFailure').value,
        pool_blacklist: document.getElementById('settingPoolBlacklist').value,
        pool_cooldown: document.getElementById('settingPoolCooldown').value,
        pool_retry: document.getElementById('settingPoolRetry').checked,
        pool_retry_attempts: document.getElementById('settingPoolRetryAttempts').value,
        pool_fail_open: document.getElementById('settingPoolFailOpen').checked,
        latency_samples: document.getElementById('settingLatencySamples').value,
        latency_tolerance: document.getElementById('settingLatencyTolerance').value,
        pool_sticky: document.getElementById('settingPoolSticky').checked,
        sticky_ttl: document.getElementById('settingStickyTTL').value,
        sticky_max: document.getElementById('settingStickyMax').value,
        mgmt_listen: document.getElementById('settingMgmtListen').value,
        mgmt_password: document.getElementById('settingMgmtPassword').value,
        operator_password: document.getElementById('settingOperatorPassword').value,
        viewer_password: document.getElementById('settingViewerPassword').value,
        geoip_enabled: document.getElementById('settingGeoIPEnabled').checked,
        geoip_db: document.getElementById('settingGeoIPDBPath').value,
        geoip_listen: document.getElementById('settingGeoIPListen').value,
        geoip_port: document.getElementById('settingGeoIPPort').value,
        geoip_auto: document.getElementById('settingGeoIPAutoUpdate').checked,
        geoip_interval: document.getElementById('settingGeoIPUpdateInterval').value,
        log_output: document.getElementById('settingLogOutput').value,
        log_max_size: document.getElementById('settingLogMaxSize').value,
        log_max_backups: document.getElementById('settingLogMaxBackups').value,
        log_max_age: document.getElementById('settingLogMaxAge').value,
        log_rotate_interval: document.getElementById('settingLogRotateInterval').value,
        log_compress: document.getElementById('settingLogCompress').checked,
        traffic_log_enabled: document.getElementById('settingTrafficLogEnabled').checked,
        traffic_log_file: document.getElementById('settingTrafficLogFile').value,
        traffic_log_retention: document.getElementById('settingTrafficLogRetention').value,
        traffic_log_max_entries: document.getElementById('settingTrafficLogMaxEntries').value,
        traffic_log_redact: document.getElementById('settingTrafficLogRedact').checked,
      });
    }
    const DURATION_UNIT_MS = Object.freeze({ h: 3600000, m: 60000, s: 1000, ms: 1 });

    function parseDurationMillis(value) {
      const text = String(value || '').trim().toLowerCase().replace(/\s+/g, '');
      if (!text) return NaN;
      const tokenPattern = /(\d+(?:\.\d+)?)(ms|h|m|s)/gy;
      let cursor = 0;
      let total = 0;
      while (cursor < text.length) {
        tokenPattern.lastIndex = cursor;
        const match = tokenPattern.exec(text);
        if (!match) return NaN;
        total += Number(match[1]) * DURATION_UNIT_MS[match[2]];
        cursor = tokenPattern.lastIndex;
      }
      return Number.isFinite(total) && total > 0 ? total : NaN;
    }

    function formatDurationForInput(value) {
      const total = parseDurationMillis(value);
      if (!Number.isFinite(total)) return String(value || '').trim();
      let remaining = total;
      const parts = [];
      for (const [unit, size] of [['h', 3600000], ['m', 60000], ['s', 1000]]) {
        const amount = Math.floor(remaining / size);
        if (amount > 0) {
          parts.push(`${amount}${unit}`);
          remaining -= amount * size;
        }
      }
      if (remaining > 0) parts.push(`${Number(remaining.toFixed(3))}ms`);
      return parts.join('') || '0ms';
    }

    function clearDurationError(fieldId, errorId) {
      const field = document.getElementById(fieldId);
      const error = document.getElementById(errorId);
      if (field) field.removeAttribute('aria-invalid');
      if (error) error.textContent = '';
    }

    function normalizeDurationInput(fieldId) {
      const field = document.getElementById(fieldId);
      if (!field || field.disabled) return;
      const parsed = parseDurationMillis(field.value);
      if (Number.isFinite(parsed)) field.value = formatDurationForInput(field.value);
    }

    function validateDurationField(fieldId, errorId, minimum, invalidMessage, minimumMessage) {
      const field = document.getElementById(fieldId);
      const error = document.getElementById(errorId);
      if (!field || field.disabled) {
        clearDurationError(fieldId, errorId);
        return true;
      }
      const parsed = parseDurationMillis(field.value);
      let message = '';
      if (!Number.isFinite(parsed)) message = tr(invalidMessage);
      else if (parsed < minimum) message = tr(minimumMessage);
      if (message) {
        field.setAttribute('aria-invalid', 'true');
        if (error) error.textContent = message;
        return false;
      }
      field.value = formatDurationForInput(field.value);
      clearDurationError(fieldId, errorId);
      return true;
    }

    function validateProbeDurationSettings() {
      if (document.getElementById('settingProbeMode').value === 'manual') return true;
      const intervalValid = validateDurationField(
        'settingProbeInterval', 'settingProbeIntervalError', 10000,
        '自动探测间隔格式无效，请填写 30s、5m 或 1h30m 等格式。',
        '自动探测间隔不能短于 10s。'
      );
      const timeoutValid = validateDurationField(
        'settingProbeTimeout', 'settingProbeTimeoutError', 100,
        '单节点超时格式无效，请填写 500ms 或 10s 等格式。',
        '单节点超时不能短于 100ms。'
      );
      if (!intervalValid) document.getElementById('settingProbeInterval').focus();
      else if (!timeoutValid) document.getElementById('settingProbeTimeout').focus();
      return intervalValid && timeoutValid;
    }

    function toggleLogFileSettings() {
      const output = document.getElementById('settingLogOutput').value;
      document.getElementById('logFileSettings').style.display = output === 'file' ? 'grid' : 'none';
    }

    function toggleProbePolicySettings() {
      const mode = document.getElementById('settingProbeMode').value;
      const automatic = mode !== 'manual';
      document.getElementById('settingProbeInterval').disabled = !automatic;
      document.getElementById('settingProbeTimeout').disabled = !automatic;
      document.getElementById('settingProbeBatchSize').disabled = mode !== 'sample' && mode !== 'adaptive';
      document.querySelectorAll('.adaptive-probe-setting').forEach(element => { element.style.display = mode === 'adaptive' ? '' : 'none'; });
      if (!automatic) {
        clearDurationError('settingProbeInterval', 'settingProbeIntervalError');
        clearDurationError('settingProbeTimeout', 'settingProbeTimeoutError');
      }
    }

    function togglePoolStrategySettings() {
      const retryEnabled = document.getElementById('settingPoolRetry').checked;
      const stickyEnabled = document.getElementById('settingPoolSticky').checked;
      const latencyEnabled = ['latency', 'quality'].includes(document.getElementById('settingPoolMode').value);
      document.getElementById('settingPoolRetryAttempts').disabled = !retryEnabled;
      document.getElementById('settingStickyTTL').disabled = !stickyEnabled;
      document.getElementById('settingStickyMax').disabled = !stickyEnabled;
      document.getElementById('settingLatencySamples').disabled = !latencyEnabled;
      document.getElementById('settingLatencyTolerance').disabled = !latencyEnabled;
    }

    async function handleSettingsSave(e) {
      e.preventDefault();
      if (!validateProbeDurationSettings()) return;
      if (window.proxyFleetProfiles && !window.proxyFleetProfiles.validate()) return;
      if (window.proxyFleetEndpoints && !window.proxyFleetEndpoints.validate()) return;
      if (window.proxyFleetSubscriptions && !window.proxyFleetSubscriptions.validate()) return;
      const saveBtn = e.target.querySelector('button[type="submit"]');
      const saveLabel = document.getElementById('settingsSaveLabel');
      const originalText = saveLabel.textContent;

      // Build current snapshots for change detection
      const currentCoreSnapshot = _buildCoreSnapshot();
      const subSources = window.proxyFleetSubscriptions?.serialize() || [];
      const subEnabled = document.getElementById('settingSubEnabled').checked;
      const subInterval = document.getElementById('settingSubInterval').value;
      const subFetchConcurrency = parseInt(document.getElementById('settingSubFetchConcurrency').value) || 16;
      const subAllowPrivate = document.getElementById('settingSubAllowPrivate').checked;
      const subMaxRemovedRatio = Number(document.getElementById('settingSubMaxRemovedRatio').value) || 0.5;
      const subMinAvailableRatio = Number(document.getElementById('settingSubMinAvailableRatio').value) || 0;
      const subQuarantine = document.getElementById('settingSubQuarantine').checked;
      const subNodeFailurePolicy = document.getElementById('settingSubNodeFailurePolicy').value;
      if (subMaxRemovedRatio <= 0 || subMaxRemovedRatio > 1 || subMinAvailableRatio < 0 || subMinAvailableRatio > 1) {
        showToast(tr('订阅比例必须在 0 到 1 之间'), 'error');
        return;
      }
      const currentSubSnapshot = JSON.stringify({sources: subSources, enabled: subEnabled, interval: subInterval, fetch_concurrency: subFetchConcurrency, allow_private_networks: subAllowPrivate, max_removed_ratio: subMaxRemovedRatio, min_available_ratio: subMinAvailableRatio, quarantine_new_nodes: subQuarantine, node_failure_policy: subNodeFailurePolicy});

      const coreChanged = currentCoreSnapshot !== _savedCoreSnapshot;
      const subChanged = subscriptionSettingsLoaded && currentSubSnapshot !== _savedSubSnapshot;
      let coreRestartRequired = false;
      let coreAuthChanged = false;
      let savedCore = {};
      try { savedCore = JSON.parse(_savedCoreSnapshot || '{}'); } catch (_) {}
      const managementPasswordChanged = coreChanged && (
        savedCore.mgmt_password !== document.getElementById('settingMgmtPassword').value ||
        savedCore.operator_password !== document.getElementById('settingOperatorPassword').value ||
        savedCore.viewer_password !== document.getElementById('settingViewerPassword').value
      );

      // Rotating the password invalidates this browser session immediately. Do
      // not start a deterministic partial save by attempting subscriptions
      // after that rotation; keep both edits in the form for separate saves.
      if (managementPasswordChanged && subChanged) {
        showToast(tr('管理密码与订阅设置不能同时保存，请先保存订阅设置，再单独修改管理密码'), 'warning');
        return;
      }

      if (!coreChanged && !subChanged) {
        if (!subscriptionSettingsLoaded) {
          showToast(tr('订阅设置加载失败，请刷新页面后重试'), 'error');
          return;
        }
        showToast(tr('配置未变更')); return;
      }

      // Save core settings if changed
      if (coreChanged) {
        saveBtn.disabled = true; saveLabel.textContent = tr('保存中...'); saveBtn.style.opacity = '0.6';
        const p = {
          profiles: window.proxyFleetProfiles?.serialize() || [],
          endpoints: window.proxyFleetEndpoints?.serialize() || [],
          external_ip: document.getElementById('settingExternalIP').value,
          probe_target: document.getElementById('settingProbeTarget').value,
          skip_cert_verify: document.getElementById('settingSkipCertVerify').checked,
          probe_concurrency: parseInt(document.getElementById('settingProbeConcurrency').value) || 32,
          probe_mode: document.getElementById('settingProbeMode').value,
          probe_interval: document.getElementById('settingProbeInterval').value.trim() || '5m',
          probe_timeout: document.getElementById('settingProbeTimeout').value.trim() || '10s',
          probe_batch_size: parseInt(document.getElementById('settingProbeBatchSize').value) || 100,
          mode: document.getElementById('settingMode').value,
          multi_port: {
            address: document.getElementById('settingMPAddr').value,
            base_port: parseInt(document.getElementById('settingMPBasePort').value) || 0,
            username: document.getElementById('settingMPUser').value,
            password: document.getElementById('settingMPPass').value,
          },
          pool: {
            mode: document.getElementById('settingPoolMode').value,
            failure_threshold: parseInt(document.getElementById('settingPoolFailure').value) || 3,
            blacklist_duration: document.getElementById('settingPoolBlacklist').value || '24h',
            transient_cooldown: document.getElementById('settingPoolCooldown').value || '1m',
            retry_enabled: document.getElementById('settingPoolRetry').checked,
            retry_attempts: parseInt(document.getElementById('settingPoolRetryAttempts').value) || 3,
            fail_open: document.getElementById('settingPoolFailOpen').checked,
            latency_sample_size: parseInt(document.getElementById('settingLatencySamples').value) || 4,
            latency_tolerance: document.getElementById('settingLatencyTolerance').value || '50ms',
            sticky: {
              enabled: document.getElementById('settingPoolSticky').checked,
              ttl: document.getElementById('settingStickyTTL').value || '30m',
              max_entries: parseInt(document.getElementById('settingStickyMax').value) || 4096,
            },
          },
          management: {
            listen: document.getElementById('settingMgmtListen').value,
            password: document.getElementById('settingMgmtPassword').value,
            operator_password: document.getElementById('settingOperatorPassword').value,
            viewer_password: document.getElementById('settingViewerPassword').value,
            probe_concurrency: parseInt(document.getElementById('settingProbeConcurrency').value) || 32,
            probe_mode: document.getElementById('settingProbeMode').value,
            probe_interval: document.getElementById('settingProbeInterval').value.trim() || '5m',
            probe_timeout: document.getElementById('settingProbeTimeout').value.trim() || '10s',
            probe_batch_size: parseInt(document.getElementById('settingProbeBatchSize').value) || 100,
            probe_healthy_interval: document.getElementById('settingProbeHealthyInterval').value.trim() || '30m',
            probe_failure_retry_interval: document.getElementById('settingProbeFailureRetry').value.trim() || '1m',
            probe_failure_max_interval: document.getElementById('settingProbeFailureMax').value.trim() || '1h',
            probe_passive_grace: document.getElementById('settingProbePassiveGrace').value.trim() || '10m',
            probe_max_per_hour: parseInt(document.getElementById('settingProbeMaxPerHour').value) || 0,
            probe_max_per_day: parseInt(document.getElementById('settingProbeMaxPerDay').value) || 0,
            history_enabled: document.getElementById('settingHistoryEnabled').checked,
            history_file: document.getElementById('settingHistoryFile').value.trim(),
            history_retention: document.getElementById('settingHistoryRetention').value.trim() || '24h',
            history_interval: document.getElementById('settingHistoryInterval').value.trim() || '1m',
            alert_min_available: parseInt(document.getElementById('settingAlertMinAvailable').value) || 0,
            alert_min_available_ratio: Number(document.getElementById('settingAlertMinRatio').value) || 0,
            alert_cooldown: document.getElementById('settingAlertCooldown').value.trim() || '10m',
            audit_file: document.getElementById('settingAuditFile').value.trim(),
          },
          log: {
            output: document.getElementById('settingLogOutput').value,
            max_size: parseInt(document.getElementById('settingLogMaxSize').value) || 50,
            max_backups: parseInt(document.getElementById('settingLogMaxBackups').value) || 3,
            max_age: parseInt(document.getElementById('settingLogMaxAge').value) || 7,
            compress: document.getElementById('settingLogCompress').checked,
            rotate_interval: document.getElementById('settingLogRotateInterval').value.trim() || '0',
          },
          traffic_log: {
            enabled: document.getElementById('settingTrafficLogEnabled').checked,
            file: document.getElementById('settingTrafficLogFile').value.trim() || 'traffic-log.db',
            retention: document.getElementById('settingTrafficLogRetention').value.trim() || '24h',
            max_entries: parseInt(document.getElementById('settingTrafficLogMaxEntries').value) || 100000,
            redact_destination: document.getElementById('settingTrafficLogRedact').checked,
          },
          geoip: {
            enabled: document.getElementById('settingGeoIPEnabled').checked,
            database_path: document.getElementById('settingGeoIPDBPath').value,
            listen: document.getElementById('settingGeoIPListen').value,
            port: parseInt(document.getElementById('settingGeoIPPort').value) || 0,
            auto_update_enabled: document.getElementById('settingGeoIPAutoUpdate').checked,
            auto_update_interval: document.getElementById('settingGeoIPAutoUpdate').checked ? (document.getElementById('settingGeoIPUpdateInterval').value || '24h') : '',
          }
        };
        try {
          const r = await fetch('/api/settings', {method:'PUT', headers:{'Content-Type':'application/json', 'If-Match': settingsETag}, body:JSON.stringify(p)});
          const result = await r.json().catch(() => ({}));
          if(!r.ok) {
            if (r.status === 412 || r.status === 428) {
              await loadSettingsPage();
              showToast(tr('设置已被其他操作更新，已重新载入'), 'warning');
            } else {
              showToast(localizedAPIMessage(result.error, '保存失败'), 'error');
            }
            saveBtn.disabled = false; saveLabel.textContent = originalText; saveBtn.style.opacity = '1'; return;
          }
          const committedETag = r.headers.get('ETag') || settingsETag;
          settingsETag = committedETag;
          // A successful partial core update preserves the subscription fields,
          // so its committed revision is also the correct subscription base.
          subscriptionETag = committedETag || subscriptionETag;
          coreRestartRequired = !!result.need_restart;
          coreAuthChanged = !!result.auth_changed;
        } catch(e){ showToast(e.message || tr('保存失败'), 'error'); saveBtn.disabled = false; saveLabel.textContent = originalText; saveBtn.style.opacity = '1'; return; }
        _savedCoreSnapshot = currentCoreSnapshot;
      }

      // Save subscription config through a fetched, one-time preview plan.
      if (subChanged) {
        saveBtn.disabled = true; saveLabel.textContent = tr('保存中...'); saveBtn.style.opacity = '0.6';
        const subPayload = {
          sources: subSources, enabled: subEnabled, interval: subInterval,
          fetch_concurrency: subFetchConcurrency, allow_private_networks: subAllowPrivate,
          max_removed_ratio: subMaxRemovedRatio, min_available_ratio: subMinAvailableRatio,
          quarantine_new_nodes: subQuarantine, node_failure_policy: subNodeFailurePolicy,
        };
        try {
          showFullscreenLoading(tr('更新订阅中...'), tr('正在拉取候选订阅并计算节点差异'));
          const previewResponse = await fetch('/api/subscription/preview', {method:'POST', headers:{'Content-Type':'application/json', 'If-Match': subscriptionETag}, body:JSON.stringify(subPayload)});
          if (previewResponse.status === 412 || previewResponse.status === 428) {
            hideFullscreenLoading(); await loadSettingsPage();
            showToast(tr('设置已被其他操作更新，已重新载入'), 'warning');
            saveBtn.disabled = false; saveLabel.textContent = originalText; saveBtn.style.opacity = '1'; return;
          }
          const preview = await readAPIJSON(previewResponse, '订阅预览失败');
          hideFullscreenLoading();
          const removedPercent = ((Number(preview.removed_ratio) || 0) * 100).toFixed(1);
          const previewMessage = tr(preview.risky
            ? '候选节点 {candidate} 个：新增 {added}、删除 {removed}、保留 {unchanged}。删除比例 {removedPercent}%，已超过安全阈值。确认应用此候选池？'
            : '候选节点 {candidate} 个：新增 {added}、删除 {removed}、保留 {unchanged}。删除比例 {removedPercent}%。确认应用此候选池？', {
            candidate: preview.candidate_total || 0,
            added: preview.added || 0,
            removed: preview.removed || 0,
            unchanged: preview.unchanged || 0,
            removedPercent,
          });
          const confirmed = await requestConfirmation(preview.risky ? tr('高风险订阅变更') : tr('确认订阅变更'), previewMessage, preview.risky ? tr('确认高风险变更') : tr('应用候选池'));
          if (!confirmed) {
            saveBtn.disabled = false; saveLabel.textContent = originalText; saveBtn.style.opacity = '1'; return;
          }
          showFullscreenLoading(tr('更新订阅中...'), tr('正在预检候选池并原子切换节点'));
          const applyPayload = {...subPayload, preview_token: preview.token, confirm_risky: preview.risky === true};
          const sr = await fetch('/api/subscription/config', {method:'PUT', headers:{'Content-Type':'application/json', 'If-Match': subscriptionETag}, body:JSON.stringify(applyPayload)});
          hideFullscreenLoading();
          if (sr.status === 412 || sr.status === 428) {
            await loadSettingsPage();
            showToast(tr('设置已被其他操作更新，已重新载入'), 'warning');
            saveBtn.disabled = false; saveLabel.textContent = originalText; saveBtn.style.opacity = '1'; return;
          }
          const sd = await readAPIJSON(sr, '订阅配置保存失败');
          subscriptionETag = sr.headers.get('ETag') || subscriptionETag;
          const statusResponse = await fetch('/api/subscription/status');
          const statusData = await readAPIJSON(statusResponse, '订阅源状态加载失败');
          window.proxyFleetSubscriptions?.load(sd.sources || subSources, statusData.sources || []);
          _savedSubSnapshot = JSON.stringify({sources: window.proxyFleetSubscriptions?.serialize() || subSources, enabled: subEnabled, interval: subInterval, fetch_concurrency: subFetchConcurrency, allow_private_networks: subAllowPrivate, max_removed_ratio: subMaxRemovedRatio, min_available_ratio: subMinAvailableRatio, quarantine_new_nodes: subQuarantine, node_failure_policy: subNodeFailurePolicy});
          showToast(sd.node_count !== undefined ? tr('已保存，获取 {count} 个节点', {count: sd.node_count}) : tr('设置已保存'));
        } catch(e){ hideFullscreenLoading(); showToast(e.message || tr('订阅配置保存失败'), 'error'); saveBtn.disabled = false; saveLabel.textContent = originalText; saveBtn.style.opacity = '1'; return; }
      } else if (coreChanged && !coreRestartRequired) {
        showToast(tr('已保存并重载成功'));
      }
      if (coreRestartRequired) {
        showToast(tr('配置已保存；部分设置将在重启程序后生效'), 'warning');
      }
      saveBtn.disabled = false; saveLabel.textContent = originalText; saveBtn.style.opacity = '1';
      if (coreAuthChanged) {
        showToast(tr('管理密码已更新，请使用新密码重新登录'), 'warning');
        showLoginOverlay();
        return;
      }
      refresh();
    }

    let logPollInterval = null;
    let trafficLogPollInterval = null;
    function startLogPolling() {
      pollLogs();
      if (!logPollInterval) logPollInterval = setInterval(pollLogs, 2000);
      loadTrafficLogs();
      if (!trafficLogPollInterval) trafficLogPollInterval = setInterval(loadTrafficLogs, 5000);
    }
    function stopLogPolling() {
      if (logPollInterval) { clearInterval(logPollInterval); logPollInterval = null; }
      if (trafficLogPollInterval) { clearInterval(trafficLogPollInterval); trafficLogPollInterval = null; }
    }

    async function loadTrafficLogs() {
      const body = document.getElementById('trafficLogTableBody');
      const summary = document.getElementById('trafficLogSummary');
      if (!body || !summary) return;
      const success = document.getElementById('trafficLogSuccessFilter')?.value || '';
      try {
        const response = await fetch(`/api/traffic/logs?limit=200${success ? `&success=${success}` : ''}`);
        const data = await readAPIJSON(response, '结构化流量日志加载失败');
        const events = Array.isArray(data.events) ? data.events : [];
        if (!data.enabled) {
          summary.textContent = '当前未启用；可在设置 → 日志配置中开启。';
          body.innerHTML = `<tr><td colspan="11" class="table-empty">结构化流量日志未启用</td></tr>`;
          return;
        }
        summary.textContent = tr('最近 {count} 条 · 写入队列累计丢弃 {dropped} 条', {count: events.length, dropped: Number(data.dropped) || 0});
        body.innerHTML = events.length ? events.map(event => `<tr>
          <td>${formatDateTime(event.timestamp)}</td>
          <td><span class="badge ${event.success ? 'badge-healthy' : 'badge-error'}">${event.success ? tr('成功') : tr('失败')}</span></td>
          <td class="tt-mono">${escapeHtml(event.node_id || '-')}</td><td>${escapeHtml(event.profile || '-')}</td>
          <td class="tt-mono traffic-log-destination" title="${escapeHtml(event.destination || '')}">${escapeHtml(event.destination || '-')}</td>
          <td>${Number(event.connect_ms) || 0} ms</td><td>${Number(event.ttfb_ms) || 0} ms</td><td>${Number(event.duration_ms) || 0} ms</td>
          <td class="tt-mono">${formatBytes(Number(event.upload_bytes) || 0)} / ${formatBytes(Number(event.download_bytes) || 0)}</td>
          <td>${Number(event.attempt) || 1}${event.retried ? ' · retry' : ''}</td><td>${escapeHtml(event.error_category || '-')}</td>
        </tr>`).join('') : `<tr><td colspan="11" class="table-empty">暂无流量记录</td></tr>`;
      } catch (error) {
        summary.textContent = error.message || '加载失败';
        body.innerHTML = `<tr><td colspan="11" class="table-empty">${escapeHtml(error.message || '加载失败')}</td></tr>`;
      }
    }

    async function clearTrafficLogs() {
      const confirmed = await requestConfirmation(tr('清空记录'), tr('确定清空结构化流量日志？此操作会删除 SQLite 中的全部连接历史，无法恢复。'), tr('清空记录'));
      if (!confirmed) return;
      try {
        const response = await fetch('/api/traffic/logs/clear', {method:'DELETE'});
        await readAPIJSON(response, '清空结构化流量日志失败');
        await loadTrafficLogs();
        showToast(tr('结构化流量日志已清空'));
      } catch (error) { showToast(error.message || tr('请求失败'), 'error'); }
    }

    function classifyLogLine(line) {
      const match = line.match(/\b(TRACE|DEBUG|INFO|WARN(?:ING)?|ERROR|FATAL|PANIC)\b/i);
      if (!match) return 'info';
      const level = match[1].toLowerCase();
      return level === 'warning' ? 'warn' : level;
    }

    function renderConsoleLogs(payload) {
      if (payload === _lastLogsPayload) return;
      _lastLogsPayload = payload;
      const viewer = document.getElementById('consoleLogs');
      const autoScroll = document.getElementById('autoScrollLogs').checked;
      const previousTop = viewer.scrollTop;
      const fragment = document.createDocumentFragment();
      String(payload || '').replace(/\u001b\[[0-?]*[ -\/]*[@-~]/g, '').split('\n').forEach(line => {
        const level = classifyLogLine(line);
        const row = document.createElement('span');
        row.className = `log-line log-${level}`;
        row.dataset.level = level;
        const badge = document.createElement('span');
        badge.className = 'log-level';
        badge.textContent = level.toUpperCase();
        const message = document.createElement('span');
        message.textContent = line;
        row.append(badge, message);
        fragment.appendChild(row);
      });
      viewer.replaceChildren(fragment);
      viewer.scrollTop = autoScroll ? viewer.scrollHeight : previousTop;
    }

    async function pollLogs() {
      try {
        const r = await fetch('/api/logs');
        const d = await readAPIJSON(r, '加载日志失败');
        renderConsoleLogs(d.logs || '');
      } catch(e){ console.error('Failed to load logs:', e); stopLogPolling(); showToast(e.message, 'error'); }
    }

    async function clearConsoleLogs() {
      const confirmed = await requestConfirmation(
        tr('清空日志'),
        `${tr('清空 Console 日志？')} ${tr('只会清除当前内存中的 Console 日志，磁盘日志和归档不受影响。')}`,
        tr('清空日志')
      );
      if (!confirmed) return;
      try {
        const response = await fetch('/api/logs', {method: 'DELETE'});
        await readAPIJSON(response, '请求失败');
        _lastLogsPayload = null;
        renderConsoleLogs('');
        showToast(tr('Console 日志已清空'));
      } catch (error) {
        showToast(error.message || tr('请求失败'), 'error');
      }
    }

    let operationsHistoryCache = [];

    async function loadOperations() {
      try {
        const [probeResponse, historyResponse, alertsResponse] = await Promise.all([
          fetch('/api/probe/status'), fetch('/api/metrics/history?limit=720'), fetch('/api/alerts')
        ]);
        const [probeData, historyData, alertsData] = await Promise.all([
          readAPIJSON(probeResponse, '探测状态加载失败'),
          readAPIJSON(historyResponse, '指标历史加载失败'),
          readAPIJSON(alertsResponse, '告警加载失败'),
        ]);
        const budget = probeData.budget || {};
        document.getElementById('opsProbeBudget').textContent = `${Number(budget.used) || 0} / ${Number(budget.limit) > 0 ? budget.limit : '∞'}`;
        document.getElementById('opsProbeWindow').textContent = budget.next_reset ? `Reset ${new Date(budget.next_reset).toLocaleTimeString()}` : (budget.mode || '-');
        document.getElementById('opsProbeDue').textContent = Number(budget.due) || 0;
        document.getElementById('opsPassiveSkipped').textContent = Number(budget.passive_skipped) || 0;
        operationsHistoryCache = Array.isArray(historyData.history) ? historyData.history : [];
        const alerts = Array.isArray(alertsData.alerts) ? alertsData.alerts : [];
        document.getElementById('opsActiveAlerts').textContent = alerts.filter(alert => alert.active).length;
        document.getElementById('operationsAlertsBody').innerHTML = alerts.length ? alerts.map(alert => `<tr>
          <td><span class="badge ${alert.active ? 'badge-error' : 'badge-healthy'}">${alert.active ? tr('活动') : tr('已恢复')}</span></td>
          <td>${escapeHtml(alert.severity || '-')}</td><td>${escapeHtml(alert.message || '-')}</td><td>${formatDateTime(alert.updated_at)}</td>
        </tr>`).join('') : `<tr><td colspan="4" style="text-align:center;color:var(--text-muted)">${tr('暂无告警')}</td></tr>`;
        if (currentRole === 'admin') {
          const auditResponse = await fetch('/api/audit?limit=200');
          const auditData = await readAPIJSON(auditResponse, '审计记录加载失败');
          const events = Array.isArray(auditData.events) ? auditData.events : [];
          document.getElementById('operationsAuditBody').innerHTML = events.length ? events.map(event => `<tr>
            <td>${formatDateTime(event.timestamp)}</td><td>${escapeHtml(event.role || '-')}</td><td class="tt-mono">${escapeHtml(event.method || '-')}</td>
            <td class="tt-mono">${escapeHtml(event.path || '-')}</td><td>${Number(event.status) || '-'}</td><td>${escapeHtml(event.remote_ip || '-')}</td>
          </tr>`).join('') : `<tr><td colspan="6" style="text-align:center;color:var(--text-muted)">${tr('暂无审计记录')}</td></tr>`;
        }
        renderOperationsHistory();
      } catch (error) {
        console.error('Failed to load operations:', error);
        showToast(error.message || tr('加载数据失败'), 'error');
      }
    }

    function formatDateTime(value) {
      if (!value || value === '0001-01-01T00:00:00Z') return '-';
      const date = new Date(value);
      return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString();
    }

    function renderOperationsHistory() {
      if (!document.getElementById('operationsTab').classList.contains('active')) return;
      if (!operationsHistoryChartInst) operationsHistoryChartInst = initEchart('operationsHistoryChart');
      if (!operationsHistoryChartInst) return;
      const times = operationsHistoryCache.map(point => new Date(point.timestamp).toLocaleTimeString([], {hour:'2-digit', minute:'2-digit'}));
      setChartOption(operationsHistoryChartInst, {
        backgroundColor: 'transparent', textStyle: { fontFamily: CHART_FONT_FAMILY },
        tooltip: { trigger: 'axis', backgroundColor: getCssVar('--bg-panel'), borderColor: getCssVar('--border'), textStyle: { color: getCssVar('--text-main') } },
        legend: { top: 4, textStyle: { color: getCssVar('--text-muted') } },
        grid: { left: '4%', right: '5%', bottom: '8%', top: '18%', containLabel: true },
        xAxis: { type: 'category', boundaryGap: false, data: times, axisLabel: { color: getCssVar('--text-muted') } },
        yAxis: [
          { type: 'value', name: tr('节点'), axisLabel: { color: getCssVar('--text-muted') }, splitLine: { lineStyle: { color: getCssVar('--border'), type: 'dashed' } } },
          { type: 'value', name: 'P95 ms', axisLabel: { color: getCssVar('--text-muted') }, splitLine: { show: false } }
        ],
        series: [
          { name: tr('健康节点'), type: 'line', smooth: true, showSymbol: false, data: operationsHistoryCache.map(point => point.healthy_nodes), lineStyle: { color: getCssVar('--success') } },
          { name: tr('异常节点'), type: 'line', smooth: true, showSymbol: false, data: operationsHistoryCache.map(point => point.unavailable_nodes), lineStyle: { color: getCssVar('--error') } },
          { name: 'P95', type: 'line', yAxisIndex: 1, smooth: true, showSymbol: false, data: operationsHistoryCache.map(point => point.p95_latency_ms), lineStyle: { color: getCssVar('--chart-secondary'), type: 'dashed' } }
        ]
      }, 'operationsHistoryChart');
    }

    // ─── Theme System: Dark / Light / Auto (follow OS) ───
    const THEME_LABELS = { dark: '深色模式', light: '浅色模式', auto: '跟随系统' };
    const osMediaQuery = window.matchMedia('(prefers-color-scheme: dark)');

    function getOsTheme() { return osMediaQuery.matches ? 'dark' : 'light'; }

    function reRenderCharts() {
      if (regionChartInst) { regionChartInst.dispose(); regionChartInst = null; }
      if (latencyChartInst) { latencyChartInst.dispose(); latencyChartInst = null; }
      if (trafficChartInst) { trafficChartInst.dispose(); trafficChartInst = null; }
      if (successRateChartInst) { successRateChartInst.dispose(); successRateChartInst = null; }
      if (failureChartInst) { failureChartInst.dispose(); failureChartInst = null; }
      if (operationsHistoryChartInst) { operationsHistoryChartInst.dispose(); operationsHistoryChartInst = null; }

      const dash = document.getElementById('dashboardTab');
      if (dash && dash.classList.contains('active') && typeof updateDashboardCharts === 'function') {
        updateDashboardCharts();
      }
      const dbg = document.getElementById('debugTab');
      if (dbg && dbg.classList.contains('active')) {
        if (trafficSource) { trafficSource.close(); trafficSource = null; trafficTime = []; trafficDataUp = []; trafficDataDown = []; }
        if (typeof updateDebugCharts === 'function') updateDebugCharts();
      }
      const operations = document.getElementById('operationsTab');
      if (operations && operations.classList.contains('active')) renderOperationsHistory();
    }

    function applyTheme(visualTheme, shouldReRender) {
      document.documentElement.setAttribute('data-theme', visualTheme);
      if (shouldReRender) reRenderCharts();
    }

    function syncToggleButton(mode) {
      const btn = document.getElementById('themeToggleBtn');
      const label = document.getElementById('themeModeLabel');
      if (btn) btn.setAttribute('data-mode', mode);
      if (label) label.textContent = tr(THEME_LABELS[mode] || mode);
    }

    function toggleTheme() {
      // Cycle: auto → dark → light → auto
      const saved = localStorage.getItem('themeMode') || 'auto';
      const next = saved === 'auto' ? 'dark' : (saved === 'dark' ? 'light' : 'auto');
      localStorage.setItem('themeMode', next);
      // Remove legacy key
      localStorage.removeItem('theme');

      const visual = next === 'auto' ? getOsTheme() : next;
      applyTheme(visual, true);
      syncToggleButton(next);
    }

    // Listen for OS theme changes — only affects UI when in auto mode
    osMediaQuery.addEventListener('change', () => {
      const mode = localStorage.getItem('themeMode') || 'auto';
      if (mode === 'auto') {
        applyTheme(getOsTheme(), true);
      }
    });

    // Init Theme on page load
    (function initTheme() {
      // Migrate from legacy 'theme' key
      const legacyTheme = localStorage.getItem('theme');
      let mode = localStorage.getItem('themeMode');
      if (!mode) {
        if (legacyTheme) {
          mode = legacyTheme; // 'dark' or 'light'
          localStorage.setItem('themeMode', mode);
          localStorage.removeItem('theme');
        } else {
          mode = 'auto';
        }
      }
      const visual = mode === 'auto' ? getOsTheme() : mode;
      applyTheme(visual, false);
      syncToggleButton(mode);
    })();

    // Init App
    setLanguage(currentLanguage);
    checkAuth().then(ok => { if(ok) { refresh(); startAutoRefresh(); } });

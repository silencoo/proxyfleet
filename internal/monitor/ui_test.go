package monitor

import (
	iofs "io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"easy_proxies/webui"
)

func readWebUIBundle(t testing.TB) string {
	t.Helper()
	names := []string{"index.html", "assets/legacy.js"}
	for _, pattern := range []string{"assets/index-*.js", "assets/index-*.css"} {
		matches, err := iofs.Glob(webui.Files(), pattern)
		if err != nil {
			t.Fatalf("glob WebUI bundle: %v", err)
		}
		names = append(names, matches...)
	}
	var bundle strings.Builder
	for _, name := range names {
		data, err := webui.ReadFile(name)
		if err != nil {
			t.Fatalf("read WebUI asset %s: %v", name, err)
		}
		bundle.Write(data)
		bundle.WriteByte('\n')
	}
	return bundle.String()
}

func TestIndexForbidsEmbedding(t *testing.T) {
	server := &Server{}
	recorder := httptest.NewRecorder()
	server.handleIndex(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") || !strings.Contains(got, "frame-ancestors 'none'") {
		t.Fatalf("Content-Security-Policy = %q", got)
	}
	if got := recorder.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestEmbeddedWebUIUsesBundledECharts(t *testing.T) {
	html := readWebUIBundle(t)
	if !strings.Contains(html, `<script src="/assets/echarts.min.js"></script>`) {
		t.Fatal("embedded WebUI does not load the bundled ECharts asset")
	}
	if strings.Contains(html, "cdn.jsdelivr.net") {
		t.Fatal("embedded WebUI still depends on the ECharts CDN")
	}

	server := &Server{}
	recorder := httptest.NewRecorder()
	server.handleEChartsAsset(recorder, httptest.NewRequest(http.MethodGet, "/assets/echarts.min.js", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("asset status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/javascript") {
		t.Fatalf("asset Content-Type = %q", got)
	}
	if recorder.Body.Len() < 100_000 {
		t.Fatalf("bundled ECharts asset is unexpectedly small: %d bytes", recorder.Body.Len())
	}
}

func TestEmbeddedWebUIUsesProxyFleetLogo(t *testing.T) {
	html := readWebUIBundle(t)
	for _, value := range []string{
		`<link rel="icon" type="image/png" href="/assets/proxyfleet-logo.png" />`,
		`<img class="brand-logo" src="/assets/proxyfleet-logo.png" alt="" width="36" height="36" />`,
		`ProxyFleet - 监控中心`,
	} {
		if !strings.Contains(html, value) {
			t.Errorf("embedded WebUI missing ProxyFleet branding %q", value)
		}
	}

	logo, err := webui.ReadFile("assets/proxyfleet-logo.png")
	if err != nil {
		t.Fatalf("read embedded ProxyFleet logo: %v", err)
	}
	if len(logo) < 8 || string(logo[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatal("embedded ProxyFleet logo is not a PNG")
	}

	server := &Server{}
	recorder := httptest.NewRecorder()
	server.handleLogoAsset(recorder, httptest.NewRequest(http.MethodGet, "/assets/proxyfleet-logo.png", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("logo status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("logo Content-Type = %q", got)
	}
	if recorder.Body.Len() != len(logo) {
		t.Fatalf("logo response length = %d, want %d", recorder.Body.Len(), len(logo))
	}
}

func TestEmbeddedWebUIUsesReadableLocalFontStack(t *testing.T) {
	html := readWebUIBundle(t)

	required := []string{
		`--font-ui: "Noto Sans SC", "Source Han Sans SC", "Microsoft YaHei UI"`,
		`--font-mono: "Cascadia Mono", "Cascadia Code", "JetBrains Mono"`,
		`font-size: 14px;`,
		`line-height:1.5`,
		`text-rendering:optimizeLegibility`,
		`button,input,select,textarea{font-family:var(--font-ui)}`,
		`.sensitive-textarea{font-family:var(--font-mono)}`,
		`const CHART_FONT_FAMILY = '"Noto Sans SC", "Source Han Sans SC", "Microsoft YaHei UI"`,
	}
	for _, value := range required {
		if !strings.Contains(html, value) {
			t.Errorf("embedded WebUI is missing readable typography rule %q", value)
		}
	}
	if strings.Contains(html, `--font-ui: "Inter"`) {
		t.Error("embedded WebUI still prefers Inter for mixed Chinese and English content")
	}
}
func TestEmbeddedWebUIHasMonochromeIconsAndLanguageSwitcher(t *testing.T) {
	html := readWebUIBundle(t)

	required := []string{
		`--bg-base: #090909`,
		`class="icon-sprite"`,
		`id="settingLanguage"`,
		`const TRANSLATIONS`,
		`'监控看板': 'Dashboard'`,
		`'系统设置': 'System settings'`,
		`localStorage.setItem('uiLanguage'`,
	}
	for _, value := range required {
		if !strings.Contains(html, value) {
			t.Errorf("embedded WebUI is missing %q", value)
		}
	}

	forbidden := []string{
		"github.com/jasonwong1991/easy_proxies",
		"api.github.com",
		"githubStars",
		"⚡", "➕", "⚠", "⏹", "▶", "↺",
		"🇯🇵", "🇰🇷", "🇺🇸", "🇭🇰", "🇹🇼", "🇸🇬",
	}
	for _, value := range forbidden {
		if strings.Contains(html, value) {
			t.Errorf("embedded WebUI still contains %q", value)
		}
	}
}

func TestEmbeddedWebUIHasScalableNodeOperations(t *testing.T) {
	html := readWebUIBundle(t)

	required := []string{
		`id="nodeSearch"`,
		`id="nodePageSize"`,
		`id="configNodeSearch"`,
		`id="debugNodeSearch"`,
		`data-sort-table="nodes"`,
		`data-sort-table="debug"`,
		`function sortNodeTable(key)`,
		`function sortDebugTable(key)`,
		`const REGION_NAME_PATTERNS`,
		`['resident',`,
		`function getNodeRegion(node)`,
		`function getChartNodeDisplayName(node)`,
		`function sanitizeChartLabel(value)`,
		`sorted.map(getChartNodeDisplayName)`,
		`const REGION_CHART_COLORS = Object.freeze({`,
		`function getRegionChartStyle(region, stat)`,
		`itemStyle: getRegionChartStyle(k, regionStats[k])`,
		`CHART_FONT_FAMILY`,
		`function renderConsoleLogs(payload)`,
		`.log-warn`,
		`.log-error`,
		`class="setting-input sensitive-textarea masked"`,
		`function toggleSensitiveField(fieldId, button)`,
		`regionStatsCache = data.region_stats || {}`,
		`regionHealthyCache = data.region_healthy || {}`,
		`subscriptionSettingsLoaded && currentSubSnapshot !== _savedSubSnapshot`,
		`if (isAutoRefresh) startAutoRefresh()`,
		`id="trafficPanel"`,
		`trafficRetryAttempts++`,
		`Math.min(60000, 2000 * Math.pow(2`,
		`function maskNodeURI(uri)`,
		`if (!node.available) return 1;`,
		`else if (!n.available) { badge = 'badge-error';`,
		`fetch('/api/nodes/config/' + encodeURIComponent(id))`,
		`function localizedAPIMessage(message, fallback='请求失败')`,
		`async function readAPIJSON(response, fallback='请求失败')`,
		`const managementPasswordChanged =`,
		`managementPasswordChanged && subChanged`,
		`'If-Match': subscriptionETag`,
		`coreAuthChanged = !!result.auth_changed`,
	}
	for _, value := range required {
		if !strings.Contains(html, value) {
			t.Errorf("embedded WebUI is missing %q", value)
		}
	}

	forbidden := []string{
		`<textarea id="consoleLogs"`,
		`panel.hidden = true`,
	}
	for _, value := range forbidden {
		if strings.Contains(html, value) {
			t.Errorf("embedded WebUI still contains %q", value)
		}
	}
	if strings.Contains(html, `else if (n.failure_count >= 1)`) {
		t.Error("node status still treats historical failures as a current outage")
	}
}

func TestEmbeddedWebUIExposesAdaptivePoolSettings(t *testing.T) {
	html := readWebUIBundle(t)
	required := []string{
		`value="latency"`,
		`id="settingPoolRetry"`,
		`id="settingPoolCooldown"`,
		`id="settingPoolSticky"`,
		`id="settingStickyTTL"`,
		`id="settingStickyMax"`,
		`function togglePoolStrategySettings()`,
		`n.cooling_down`,
		`'冷却 Cooling': 'Cooling down'`,
	}
	for _, value := range required {
		if !strings.Contains(html, value) {
			t.Errorf("embedded WebUI is missing %q", value)
		}
	}
}

func TestEmbeddedWebUIExposesInputConcurrencySettings(t *testing.T) {
	html := readWebUIBundle(t)
	for _, value := range []string{
		`id="settingProbeConcurrency"`,
		`id="settingSubFetchConcurrency"`,
		`id="settingSubAllowPrivate"`,
		`fetch_concurrency: subFetchConcurrency`,
		`allow_private_networks: subAllowPrivate`,
		`'订阅抓取并发数': 'Subscription fetch concurrency'`,
		`'允许访问内网订阅地址（高风险）': 'Allow private-network subscription URLs (high risk)'`,
	} {
		if !strings.Contains(html, value) {
			t.Errorf("embedded WebUI is missing %q", value)
		}
	}
}
func TestEmbeddedWebUIExposesP0P1P2Operations(t *testing.T) {
	html := readWebUIBundle(t)
	for _, value := range []string{
		`value="adaptive"`, `value="quality"`, `id="settingProbeMaxPerHour"`, `id="settingProbeMaxPerDay"`,
		`id="operationsTab"`, `id="operationsHistoryChart"`, `fetch('/api/metrics/history?limit=720')`,
		`data-min-role="admin"`, `function applyRolePermissions(role)`, `fetch('/api/session')`,
		`new URLSearchParams({`, `page_size: String(nodeTableState.pageSize)`, `nodePaginationMeta = data.pagination`,
		`id="settingSubMaxRemovedRatio"`, `id="settingSubNodeFailurePolicy"`, `fetch('/api/subscription/preview'`, `preview_token: preview.token`,
		`id="buildInfoPanel"`, `fetch('/api/build-info')`, `function ensureTrafficChart()`,
	} {
		if !strings.Contains(html, value) {
			t.Errorf("embedded WebUI is missing P0/P1/P2 control %q", value)
		}
	}
	if strings.Contains(html, `let nodes = allNodesCache.filter`) {
		t.Error("dashboard still filters the entire node pool in the browser")
	}
}

package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"easy_proxies/internal/config"
)

func TestProfilePreviewExplainsComposableRuleExclusions(t *testing.T) {
	cfg := &config.Config{
		Mode: "pool", Listener: config.ListenerConfig{Username: "fleet", Password: "secret"},
		Nodes: []config.NodeConfig{
			{Name: "HK Premium", URI: "socks5://127.0.0.1:1080", Source: config.NodeSourceInline},
			{Name: "JP Expired", URI: "socks5://127.0.0.1:1081", Source: config.NodeSourceInline},
			{Name: "US Premium", URI: "socks5://127.0.0.1:1082", Source: config.NodeSourceInline},
		},
	}
	server := &Server{cfgSrc: cfg}
	body := `{"name":"premium","tag_rules":{"any":["HK|JP"],"must":["Premium"],"must_not":["Expired"]}}`
	recorder := httptest.NewRecorder()
	server.handleProfilePreview(recorder, httptest.NewRequest(http.MethodPost, "/api/profiles/preview", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var preview profilePreviewResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Total != 3 || preview.Matched != 1 || preview.Excluded["tag_rule"] != 2 {
		t.Fatalf("preview=%#v", preview)
	}
}

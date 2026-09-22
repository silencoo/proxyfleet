package monitor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/silencoo/proxyfleet/internal/config"
)

func TestSettingsCertificateVerificationPolicyPersists(t *testing.T) {
	cfg := newSettingsTransactionConfig(t)
	manager := &settingsTransactionNodeManager{cfg: cfg.Clone(), revision: 1}
	server := newSettingsTransactionServer(cfg, manager)
	assertState := func(mode string, skip bool, revision uint64) {
		t.Helper()
		response := httptest.NewRecorder()
		server.handleSettings(response, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["skip_cert_verify_mode"] != mode || body["skip_cert_verify"] != skip || response.Header().Get("ETag") != settingsETag(revision) {
			t.Fatalf("GET state=%v", body)
		}
		disk, err := config.Load(cfg.FilePath())
		if err != nil {
			t.Fatal(err)
		}
		if disk.CertVerifyModeOrDefault() != mode || disk.SkipCertVerify != skip {
			t.Fatalf("persisted policy=%s skip=%t", disk.SkipCertVerifyMode, disk.SkipCertVerify)
		}
	}
	assertState("default", false, 1)
	for i, tc := range []struct {
		body, mode string
		skip       bool
	}{
		{`{"skip_cert_verify_mode":"override","skip_cert_verify":false}`, "override", false},
		{`{"skip_cert_verify":true}`, "override", true},
		{`{"skip_cert_verify_mode":"default"}`, "default", true},
	} {
		r := httptest.NewRecorder()
		server.handleSettings(r, settingsPutRequest(bytes.NewBufferString(tc.body), uint64(i+1)))
		if r.Code != http.StatusOK {
			t.Fatalf("PUT status=%d body=%s", r.Code, r.Body.String())
		}
		assertState(tc.mode, tc.skip, uint64(i+2))
	}
	for _, body := range []string{`{"skip_cert_verify_mode":"invalid"}`, `{"skip_cert_verify_mode":true}`} {
		r := httptest.NewRecorder()
		server.handleSettings(r, settingsPutRequest(bytes.NewBufferString(body), 4))
		if r.Code != http.StatusBadRequest {
			t.Fatalf("invalid mode status=%d body=%s", r.Code, r.Body.String())
		}
		assertState("default", true, 4)
	}
}

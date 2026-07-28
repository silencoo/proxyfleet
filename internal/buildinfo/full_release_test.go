//go:build with_utls && with_quic && with_grpc && with_wireguard && with_gvisor && with_clash_api

package buildinfo

import "testing"

func TestOfficialReleaseFeatureSet(t *testing.T) {
	info := Current()
	if !info.OfficialReleaseReady {
		t.Fatalf("full-tag build missing official capabilities: %v", info.MissingFeatures)
	}
	if !info.Capabilities["quic"] || !info.Capabilities["clash_api"] {
		t.Fatalf("critical release capabilities missing: %+v", info.Capabilities)
	}
}

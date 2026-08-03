package builder

import (
	"testing"

	"easy_proxies/internal/config"
	poolout "easy_proxies/internal/outbound/pool"

	"github.com/sagernet/sing-box/option"
)

func boolPointer(value bool) *bool { return &value }

func TestBuildCreatesManagedEndpointsOverOneSharedPool(t *testing.T) {
	cfg := &config.Config{
		Mode: "pool",
		Endpoints: []config.EndpointConfig{
			{Name: "public", Enabled: boolPointer(true), Address: "127.0.0.1", Port: 2323, Username: "fleet", Password: "secret"},
			{Name: "hk-only", Enabled: boolPointer(true), Address: "127.0.0.1", Port: 2324, Username: "hk", Password: "secret", Profile: "hk-fast"},
			{Name: "disabled", Enabled: boolPointer(false), Address: "127.0.0.1", Port: 2325},
		},
		Profiles: []config.ProfileConfig{{Name: "hk-fast", Regions: []string{"hk"}}},
		Pool:     config.PoolConfig{Mode: "sequential"},
		Nodes: []config.NodeConfig{{
			Name: "upstream", URI: "socks5://127.0.0.1:1080#upstream",
		}},
	}

	opts, err := Build(cfg)
	if err != nil {
		t.Fatalf("build endpoint config: %v", err)
	}
	if len(opts.Inbounds) != 2 {
		t.Fatalf("inbound count=%d, want two enabled endpoints", len(opts.Inbounds))
	}
	byTag := make(map[string]*option.HTTPMixedInboundOptions, len(opts.Inbounds))
	for _, inbound := range opts.Inbounds {
		mixed, ok := inbound.Options.(*option.HTTPMixedInboundOptions)
		if !ok {
			t.Fatalf("inbound %q has options %T", inbound.Tag, inbound.Options)
		}
		byTag[inbound.Tag] = mixed
	}
	public := byTag[config.EndpointInboundTag("public")]
	if public == nil || len(public.Users) != 2 || public.Users[1].Username != "fleet@hk-fast" {
		t.Fatalf("global endpoint users=%#v", public)
	}
	hkOnly := byTag[config.EndpointInboundTag("hk-only")]
	if hkOnly == nil || len(hkOnly.Users) != 1 || hkOnly.Users[0].Username != "hk" {
		t.Fatalf("profile-bound endpoint users=%#v", hkOnly)
	}

	poolCount := 0
	for _, outbound := range opts.Outbounds {
		poolOptions, ok := outbound.Options.(*poolout.Options)
		if !ok || outbound.Tag != poolout.Tag {
			continue
		}
		poolCount++
		if poolOptions.EndpointProfiles[config.EndpointInboundTag("hk-only")] != "hk-fast" {
			t.Fatalf("profile-bound inbound map=%#v", poolOptions.EndpointProfiles)
		}
		if len(poolOptions.Members) != 1 {
			t.Fatalf("shared pool members=%#v", poolOptions.Members)
		}
	}
	if poolCount != 1 {
		t.Fatalf("shared pool outbound count=%d, want 1", poolCount)
	}
}

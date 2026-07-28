package buildinfo

import (
	"runtime"
	"sort"
	"strings"
)

var (
	Version = "dev"
	Commit  = "unknown"
	BuiltAt = "unknown"
)

var enabledFeatures = map[string]bool{
	"utls":      false,
	"quic":      false,
	"grpc":      false,
	"wireguard": false,
	"gvisor":    false,
	"clash_api": false,
}

var officialFeatureSet = []string{"utls", "quic", "grpc", "wireguard", "gvisor", "clash_api"}

type Info struct {
	Product              string          `json:"product"`
	Version              string          `json:"version"`
	Commit               string          `json:"commit"`
	BuiltAt              string          `json:"built_at"`
	GoVersion            string          `json:"go_version"`
	GOOS                 string          `json:"goos"`
	GOARCH               string          `json:"goarch"`
	Capabilities         map[string]bool `json:"capabilities"`
	Protocols            []string        `json:"protocols"`
	OfficialReleaseReady bool            `json:"official_release_ready"`
	MissingFeatures      []string        `json:"missing_features,omitempty"`
}

func enableFeature(name string) {
	if _, exists := enabledFeatures[name]; exists {
		enabledFeatures[name] = true
	}
}

func Current() Info {
	capabilities := make(map[string]bool, len(enabledFeatures))
	for name, enabled := range enabledFeatures {
		capabilities[name] = enabled
	}
	missing := make([]string, 0)
	for _, name := range officialFeatureSet {
		if !capabilities[name] {
			missing = append(missing, name)
		}
	}
	protocols := []string{"anytls", "http", "shadowsocks", "shadowsocksr", "socks5", "trojan", "vless", "vmess"}
	if capabilities["quic"] {
		protocols = append(protocols, "hysteria2", "tuic")
	}
	if capabilities["wireguard"] {
		protocols = append(protocols, "wireguard")
	}
	sort.Strings(protocols)
	return Info{
		Product:              "ProxyFleet",
		Version:              normalized(Version, "dev"),
		Commit:               normalized(Commit, "unknown"),
		BuiltAt:              normalized(BuiltAt, "unknown"),
		GoVersion:            runtime.Version(),
		GOOS:                 runtime.GOOS,
		GOARCH:               runtime.GOARCH,
		Capabilities:         capabilities,
		Protocols:            protocols,
		OfficialReleaseReady: len(missing) == 0,
		MissingFeatures:      missing,
	}
}

func normalized(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func OfficialFeatures() []string {
	return append([]string(nil), officialFeatureSet...)
}

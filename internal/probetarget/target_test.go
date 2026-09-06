package probetarget

import "testing"

func TestParseProbeTargets(t *testing.T) {
	tests := []struct {
		value string
		host  string
		port  uint16
		tls   bool
		path  string
	}{
		{value: "example.com:80", host: "example.com", port: 80, path: "/"},
		{value: "https://example.com/check", host: "example.com", port: 443, tls: true, path: "/check"},
		{value: "http://[2001:db8::1]:8080/", host: "2001:db8::1", port: 8080, path: "/"},
		{value: "https://example.com/a%2Fb?check=1#fragment", host: "example.com", port: 443, tls: true, path: "/a%2Fb?check=1"},
	}
	for _, test := range tests {
		got, ready, err := Parse(test.value)
		if err != nil || !ready || got.Host != test.host || got.Port != test.port || got.TLS != test.tls || got.RequestURI != test.path {
			t.Errorf("Parse(%q)=(%+v,%v,%v)", test.value, got, ready, err)
		}
	}
}

func TestParseProbeTargetRejectsAmbiguousOrUnsafeValues(t *testing.T) {
	for _, value := range []string{
		"example.com", "example.com:abc", "example.com:0", "example.com:65536",
		"ftp://example.com/file", "https://user:secret@example.com/", "https://example.com:",
		"example.com:80\r\nInjected: true",
	} {
		if _, ready, err := Parse(value); err == nil || ready {
			t.Errorf("Parse(%q) unexpectedly succeeded: ready=%v err=%v", value, ready, err)
		}
	}
}

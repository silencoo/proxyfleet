package probetarget

import "testing"

func TestHTTPAuthority(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"http://example.test:8080/check", "example.test:8080"},
		{"http://127.0.0.1:8080/check", "127.0.0.1:8080"},
		{"http://[2001:db8::1]:8080/check", "[2001:db8::1]:8080"},
		{"http://example.test:443", "example.test:443"},
		{"https://example.test:80", "example.test:80"},
		{"http://example.test:80", "example.test"},
		{"https://example.test:443", "example.test"},
		{"https://[2001:db8::1]", "[2001:db8::1]"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			target, _, err := Parse(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if got := HTTPAuthority(target.Host, target.Port, target.TLS); got != tc.want {
				t.Fatalf("authority=%q want=%q", got, tc.want)
			}
		})
	}
}

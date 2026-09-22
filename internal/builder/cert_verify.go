package builder

import (
	"net/url"
	"strings"

	"github.com/sagernet/sing-box/option"
	"github.com/silencoo/proxyfleet/internal/config"
)

// Absence inherits the global default; explicit false is just as significant
// as true. This also applies when TLS is implicit in the protocol's scheme.
func nodeSkipCertVerify(query url.Values, global bool) bool {
	for _, key := range []string{"allowInsecure", "insecure", "skip-cert-verify", "skip_cert_verify"} {
		if value := query.Get(key); value != "" {
			return value == "1" || strings.EqualFold(value, "true")
		}
	}
	return global
}

func buildNodeOutboundWithPolicy(tag, rawURI string, skipCertVerify bool, mode string) (option.Outbound, error) {
	out, err := buildNodeOutbound(tag, rawURI, skipCertVerify)
	if err != nil {
		return out, err
	}
	if mode == config.CertVerifyOverride {
		if wrapper, ok := out.Options.(option.OutboundTLSOptionsWrapper); ok {
			if tls := wrapper.TakeOutboundTLSOptions(); tls != nil {
				tls.Insecure = skipCertVerify
			}
		}
	}
	return out, nil
}

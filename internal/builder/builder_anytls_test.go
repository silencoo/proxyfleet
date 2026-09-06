package builder

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"

	boxtls "github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	"github.com/silencoo/proxyfleet/internal/config"
)

func TestAnyTLSImplicitTLSDoesNotEnablePreviouslyIgnoredBypass(t *testing.T) {
	for _, query := range []string{"allowInsecure=1", "insecure=true", "security=none&allowInsecure=1"} {
		outbound, err := buildNodeOutbound("anytls", "anytls://test-password@192.0.2.1:443?sni=tls.example.test&"+query, false)
		if err != nil {
			t.Fatal(err)
		}
		if outbound.Options.(*option.AnyTLSOutboundOptions).TLS.Insecure {
			t.Fatalf("implicit TLS parsing enabled a previously ignored bypass: %s", query)
		}
	}
}

func TestAnyTLSSNIUsesVerifiedCertificate(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test certificate"},
		DNSNames:  []string{"tls.example.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	certificate := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: private}
	for _, test := range []struct {
		name, serverName string
		trust, wantOK    bool
	}{
		{"correct SNI", "tls.example.test", true, true},
		{"wrong SNI rejected", "wrong.example.test", true, false},
		{"untrusted certificate rejected", "tls.example.test", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			outbound, err := buildNodeOutbound("anytls", "anytls://test-password@192.0.2.1:443?sni="+test.serverName+"&alpn=h2", false)
			if err != nil {
				t.Fatal(err)
			}
			opts := outbound.Options.(*option.AnyTLSOutboundOptions)
			if opts.TLS.Insecure {
				t.Fatal("certificate verification is disabled")
			}
			if test.trust {
				opts.TLS.Certificate = []string{string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			clientConfig, err := boxtls.NewClient(ctx, opts.Server, *opts.TLS)
			if err != nil {
				t.Fatal(err)
			}
			// Real loopback TCP lets TLS alerts cross the peer's handshake
			// writes; an unbuffered net.Pipe can deadlock those writes and turn
			// a certificate rejection into a misleading context timeout.
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverResult := make(chan error, 1)
			go func() {
				serverRaw, err := listener.Accept()
				if err != nil {
					serverResult <- err
					return
				}
				defer serverRaw.Close()
				server := tls.Server(serverRaw, &tls.Config{Certificates: []tls.Certificate{certificate}, NextProtos: []string{"h2"}, SessionTicketsDisabled: true})
				serverResult <- server.HandshakeContext(ctx)
			}()
			clientRaw, err := (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer clientRaw.Close()
			client, err := clientConfig.Client(clientRaw)
			if err != nil {
				t.Fatal(err)
			}
			err = client.HandshakeContext(ctx)
			_ = clientRaw.Close()
			serverErr := <-serverResult
			if (err == nil) != test.wantOK {
				t.Fatalf("TLS handshake = %v, want success=%t", err, test.wantOK)
			}
			if !test.wantOK {
				var verificationError *tls.CertificateVerificationError
				if !errors.As(err, &verificationError) {
					t.Fatalf("expected certificate rejection, got %v", err)
				}
			}
			if test.wantOK && (serverErr != nil || client.ConnectionState().NegotiatedProtocol != "h2") {
				t.Fatalf("TLS server/ALPN mismatch: error=%v, protocol=%q", serverErr, client.ConnectionState().NegotiatedProtocol)
			}
		})
	}
}

func TestAnyTLSImplicitTLSPreservesHandshakeOptions(t *testing.T) {
	for _, security := range []string{"", "&security=none", "&security=tls"} {
		t.Run(security, func(t *testing.T) {
			outbound, err := buildNodeOutbound("anytls", "anytls://test-password@192.0.2.1:443?sni=tls.example.test&alpn=h2,http/1.1&fp=chrome"+security, false)
			if err != nil {
				t.Fatal(err)
			}
			opts := outbound.Options.(*option.AnyTLSOutboundOptions)
			if opts.TLS == nil || !opts.TLS.Enabled || opts.TLS.ServerName != "tls.example.test" {
				t.Fatalf("configured TLS hostname was lost: %+v", opts.TLS)
			}
			if len(opts.TLS.ALPN) != 2 || opts.TLS.ALPN[0] != "h2" || opts.TLS.ALPN[1] != "http/1.1" {
				t.Fatalf("ALPN was lost: %v", opts.TLS.ALPN)
			}
			if opts.TLS.UTLS == nil || !opts.TLS.UTLS.Enabled || opts.TLS.UTLS.Fingerprint != "chrome" {
				t.Fatal("TLS client fingerprint was lost")
			}
			if opts.TLS.Insecure {
				t.Fatal("TLS option parsing unexpectedly disabled certificate verification")
			}
		})
	}
}

func TestAnyTLSClashConversionPreservesSNIThroughBuilder(t *testing.T) {
	nodes, err := config.ParseSubscriptionContent(`proxies:
  - name: anytls-test
    type: anytls
    server: 192.0.2.1
    port: 443
    password: test-password
    sni: tls.example.test
    client-fingerprint: chrome
`)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("parse subscription: nodes=%d, error=%v", len(nodes), err)
	}
	outbound, err := buildNodeOutbound("anytls", nodes[0].URI, false)
	if err != nil {
		t.Fatal(err)
	}
	opts := outbound.Options.(*option.AnyTLSOutboundOptions)
	if opts.TLS.ServerName != "tls.example.test" || opts.TLS.UTLS == nil || opts.TLS.Insecure {
		t.Fatalf("Clash TLS options were lost: %+v", opts.TLS)
	}
}

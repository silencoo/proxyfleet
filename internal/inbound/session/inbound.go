// Package session provides authenticated HTTP/SOCKS5 ingress for pinned Jobs
// and password-routed shared applications.
// Session names travel in ordinary proxy credentials; no management API call is
// required. Only the pool owns and persists the session-to-node assignment.
package session

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"slices"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/common/listener"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/protocol/socks"
	"github.com/sagernet/sing/protocol/socks/socks5"
	"github.com/silencoo/proxyfleet/internal/config"
)

const Type = "session-mixed"
const SharedType = "application-mixed"

type ApplicationCredential struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}
type SharedOptions struct {
	option.HTTPMixedInboundOptions
	Applications []ApplicationCredential `json:"applications"`
}
type credential struct {
	username, application string
	digest                [32]byte
}

var errAuthentication = errors.New("proxy session authentication failed")

func Register(registry *inbound.Registry) {
	inbound.Register[option.HTTPMixedInboundOptions](registry, Type, NewInbound)
	inbound.Register[SharedOptions](registry, SharedType, NewSharedInbound)
}

type Inbound struct {
	inbound.Adapter
	router       adapter.Router
	listener     *listener.Listener
	httpListener *httpListener
	server       *http.Server
	username     string
	passwordHash [32]byte
	shared       bool
	credentials  []credential
}

type metadataKey struct{}

func NewInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.HTTPMixedInboundOptions) (adapter.Inbound, error) {
	if len(options.Users) != 1 || !config.ValidSessionCredentials(options.Users[0].Username, options.Users[0].Password) || options.TLS != nil {
		return nil, errors.New("session ingress requires one valid credential pair and a plain HTTP/SOCKS5 listener")
	}
	h := newInbound(ctx, router, logger, tag, Type, options)
	h.username, h.passwordHash = options.Users[0].Username, sha256.Sum256([]byte(options.Users[0].Password))
	return h, nil
}

func NewSharedInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options SharedOptions) (adapter.Inbound, error) {
	if options.TLS != nil || len(options.Users) == 0 || len(options.Applications) == 0 || len(options.Applications) > 64 {
		return nil, errors.New("invalid shared application ingress")
	}
	entries := make([]credential, 0, len(options.Users)+len(options.Applications))
	passwords, names := make(map[[32]byte]bool), make(map[string]bool)
	for _, user := range options.Users {
		digest := sha256.Sum256([]byte(user.Password))
		passwords[digest] = true
		entries = append(entries, credential{username: user.Username, digest: digest})
	}
	for _, app := range options.Applications {
		digest := sha256.Sum256([]byte(app.Password))
		if app.Name == "" || names[app.Name] || !config.ValidApplicationPassword(app.Password) || passwords[digest] {
			return nil, errors.New("invalid or ambiguous application credentials")
		}
		passwords[digest], names[app.Name] = true, true
		entries = append(entries, credential{application: app.Name, digest: digest})
	}
	h := newInbound(ctx, router, logger, tag, SharedType, options.HTTPMixedInboundOptions)
	h.shared, h.credentials = true, entries
	return h, nil
}

func newInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag, kind string, options option.HTTPMixedInboundOptions) *Inbound {
	h := &Inbound{Adapter: inbound.NewAdapter(kind, tag), router: router}
	h.httpListener = &httpListener{connections: make(chan *httpConn), done: make(chan struct{})}
	h.server = &http.Server{
		Handler: http.HandlerFunc(h.serveHTTP), ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10,
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			return context.WithValue(ctx, metadataKey{}, conn.(*httpConn).metadata)
		},
	}
	h.listener = listener.New(listener.Options{Context: ctx, Logger: logger, Network: []string{N.NetworkTCP}, Listen: options.ListenOptions, ConnectionHandler: h})
	return h
}

func (h *Inbound) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	go func() { _ = h.server.Serve(h.httpListener) }()
	return h.listener.Start()
}

func (h *Inbound) Close() error {
	return errors.Join(h.listener.Close(), h.httpListener.Close(), h.server.Close())
}

// Return a server-created application routing tag, or an empty tag for a
// verified legacy credential. A wrong supplied credential never becomes anonymous.
func (h *Inbound) authenticate(username, password string) (string, bool) {
	digest := sha256.Sum256([]byte(password))
	if h.shared {
		for _, entry := range h.credentials {
			if subtle.ConstantTimeCompare(digest[:], entry.digest[:]) != 1 {
				continue
			}
			if entry.application != "" {
				return config.SharedJobInboundTag(entry.application), config.ValidProxySessionID(username)
			}
			if username == entry.username {
				return "", true
			}
		}
		return "", false
	}
	_, valid := config.ProxySessionID(h.username, username)
	return "", subtle.ConstantTimeCompare(digest[:], h.passwordHash[:]) == 1 && valid
}

func (h *Inbound) NewConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(conn)
	header, err := reader.Peek(1)
	if err == nil {
		metadata.Inbound, metadata.InboundType = h.Tag(), h.Type()
		if header[0] == socks5.Version {
			err = h.serveSOCKS(ctx, conn, reader, metadata, onClose)
		} else if header[0] == 4 {
			err = errors.New("session ingress requires SOCKS5 username/password authentication")
		} else {
			_ = conn.SetDeadline(time.Time{})
			c := &httpConn{bufferedConn: bufferedConn{Conn: conn, reader: reader}, metadata: metadata, onClose: onClose}
			select {
			case h.httpListener.connections <- c:
			case <-h.httpListener.done:
				err = net.ErrClosed
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
	}
	// Handshake errors deliberately contain no supplied credentials.
	N.CloseOnHandshakeFailure(conn, onClose, err)
}

func (h *Inbound) serveSOCKS(ctx context.Context, conn net.Conn, reader *bufio.Reader, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) error {
	greeting, err := socks5.ReadAuthRequest(reader)
	if err != nil {
		return err
	}
	username, routingTag := "", ""
	if slices.Contains(greeting.Methods, socks5.AuthTypeUsernamePassword) {
		if err = socks5.WriteAuthResponse(conn, socks5.AuthResponse{Method: socks5.AuthTypeUsernamePassword}); err != nil {
			return err
		}
		credentials, err := socks5.ReadUsernamePasswordAuthRequest(reader)
		if err != nil {
			return err
		}
		var valid bool
		routingTag, valid = h.authenticate(credentials.Username, credentials.Password)
		if !valid {
			_ = socks5.WriteUsernamePasswordAuthResponse(conn, socks5.UsernamePasswordAuthResponse{Status: socks5.UsernamePasswordStatusFailure})
			return errAuthentication
		}
		username = credentials.Username
		if err = socks5.WriteUsernamePasswordAuthResponse(conn, socks5.UsernamePasswordAuthResponse{Status: socks5.UsernamePasswordStatusSuccess}); err != nil {
			return err
		}
	} else {
		_ = socks5.WriteAuthResponse(conn, socks5.AuthResponse{Method: socks5.AuthTypeNoAcceptedMethods})
		return errAuthentication
	}
	request, err := socks5.ReadRequest(reader)
	if err != nil {
		return err
	}
	if request.Command != socks5.CommandConnect {
		_ = socks5.WriteResponse(conn, socks5.Response{ReplyCode: socks5.ReplyCodeUnsupported})
		return errors.New("session ingress supports SOCKS5 CONNECT only")
	}
	metadata.User, metadata.Destination = username, request.Destination
	if routingTag != "" {
		metadata.Inbound = routingTag
	}
	_ = conn.SetDeadline(time.Time{})
	h.router.RouteConnectionEx(ctx, socks.NewLazyConn(&bufferedConn{Conn: conn, reader: reader}, socks5.Version), metadata, onClose)
	return nil
}

func (h *Inbound) serveHTTP(w http.ResponseWriter, r *http.Request) {
	// BasicAuth parses the standard Base64 alphabet, including non-ASCII passwords.
	authRequest := &http.Request{Header: http.Header{"Authorization": r.Header.Values("Proxy-Authorization")}}
	username, password, ok := authRequest.BasicAuth()
	routingTag, valid := h.authenticate(username, password)
	if !ok || len(r.Header.Values("Proxy-Authorization")) > 1 || !valid {
		w.Header().Set("Proxy-Authenticate", `Basic realm="ProxyFleet" charset="UTF-8"`)
		http.Error(w, "proxy session authentication required", http.StatusProxyAuthRequired)
		return
	}
	metadata := r.Context().Value(metadataKey{}).(adapter.InboundContext)
	metadata.User = username
	if routingTag != "" {
		metadata.Inbound = routingTag
	}
	if r.Method == http.MethodConnect {
		destination := M.ParseSocksaddr(r.Host).Unwrap()
		if !destination.IsValid() || destination.Port == 0 {
			http.Error(w, "CONNECT requires host:port", http.StatusBadRequest)
			return
		}
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		metadata.Destination = destination
		// Routing owns the tunnel beyond the HTTP handler's lifetime.
		h.router.RouteConnectionEx(context.WithoutCancel(r.Context()), &connectConn{bufferedConn: bufferedConn{Conn: conn, reader: buffered.Reader}}, metadata, nil)
		return
	}
	if r.URL.Scheme != "http" || r.URL.Host == "" {
		http.Error(w, "an absolute HTTP URL is required", http.StatusBadRequest)
		return
	}
	// A transport belongs to one request, so its connection cache can never be
	// shared between session identities. HTTPS uses the CONNECT tunnel above.
	transport := &http.Transport{DisableKeepAlives: true, DisableCompression: true, ResponseHeaderTimeout: 2 * time.Minute,
		DialContext: func(_ context.Context, network, address string) (net.Conn, error) {
			client, upstream := net.Pipe()
			md := metadata
			md.Destination = M.ParseSocksaddr(address).Unwrap()
			// Transport detaches its dial context from request cancellation.
			// This transport is private to one request, so routing must retain
			// that request's lifetime, including client disconnects and timeouts.
			go h.router.RouteConnectionEx(r.Context(), upstream, md, func(error) { _ = upstream.Close() })
			return client, nil
		},
	}
	defer transport.CloseIdleConnections()
	proxy := httputil.ReverseProxy{Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Forward proxies must preserve opaque target queries, including
			// semicolons and signed parameter ordering. ReverseProxy sanitizes
			// these before Rewrite; authentication here never uses the query.
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.Out.Header.Del("Proxy-Authorization")
			pr.Out.Header.Del("Proxy-Connection")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "proxy session unavailable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// Delay CONNECT success until the router has opened the selected upstream.
type connectConn struct {
	bufferedConn
	once sync.Once
	err  error
}

func (c *connectConn) HandshakeSuccess() error {
	c.once.Do(func() { _, c.err = io.WriteString(c.Conn, "HTTP/1.1 200 Connection established\r\n\r\n") })
	return c.err
}
func (c *connectConn) HandshakeFailure(error) error {
	c.once.Do(func() {
		_, c.err = io.WriteString(c.Conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
	})
	return c.err
}
func (c *connectConn) Read(p []byte) (int, error) {
	if err := c.HandshakeSuccess(); err != nil {
		return 0, err
	}
	return c.bufferedConn.Read(p)
}
func (c *connectConn) Write(p []byte) (int, error) {
	if err := c.HandshakeSuccess(); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}

type httpConn struct {
	bufferedConn
	metadata adapter.InboundContext
	onClose  N.CloseHandlerFunc
	once     sync.Once
}

func (c *httpConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		if c.onClose != nil {
			c.onClose(err)
		}
	})
	return err
}

type httpListener struct {
	connections chan *httpConn
	done        chan struct{}
	once        sync.Once
}

func (l *httpListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.connections:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *httpListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *httpListener) Addr() net.Addr { return &net.TCPAddr{} }

package builder

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	ssout "github.com/sagernet/sing-box/protocol/shadowsocks"
	"github.com/sagernet/sing-shadowsocks/shadowaead"
	"github.com/sagernet/sing/common"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// Exercise the real sing-box SS outbound over loopback TCP. The peer unwraps
// simple-obfs framing and uses a Shadowsocks server to decrypt and echo data.
func TestShadowsocksObfsEncryptedRoundTrip(t *testing.T) {
	for _, mode := range []string{"http", "tls"} {
		for _, cipher := range []string{"aes-128-gcm", "chacha20-ietf-poly1305"} {
			t.Run(mode+"/"+cipher, func(t *testing.T) {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				payload := bytes.Repeat([]byte("obfs round trip\x00"), 4096)
				handler := &obfsEchoHandler{payload: payload}
				server, err := shadowaead.NewService(cipher, nil, "test-password", 60, handler)
				if err != nil {
					t.Fatal(err)
				}
				result := make(chan error, 1)
				go func() {
					raw, err := listener.Accept()
					if err != nil {
						result <- err
						return
					}
					defer raw.Close()
					_ = raw.SetDeadline(time.Now().Add(5 * time.Second))
					peer := &obfsPeer{Conn: raw, mode: mode, reader: bufio.NewReader(raw), firstRead: true, firstWrite: true}
					result <- server.NewConnection(ctx, peer, M.Metadata{})
				}()
				opts, err := buildShadowsocksOptions("ss://" + cipher + ":test-password@" + listener.Addr().String() + "/?plugin=" + url.QueryEscape("obfs-"+mode+";host=cdn.example"))
				if err != nil {
					t.Fatal(err)
				}
				out, err := ssout.NewOutbound(ctx, nil, log.NewNOPFactory().Logger(), "obfs", opts)
				if err != nil {
					t.Fatal(err)
				}
				defer common.Close(out)
				conn, err := out.DialContext(ctx, "tcp", M.ParseSocksaddr("target.example:443"))
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				// Multiple writes exercise both the initial disguise and later records.
				for _, part := range [][]byte{payload[:37], payload[37:]} {
					if _, err = conn.Write(part); err != nil {
						t.Fatal(err)
					}
				}
				got := make([]byte, len(payload))
				if _, err := io.ReadFull(conn, got); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, payload) {
					t.Fatal("response payload changed")
				}
				if err := <-result; err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

type obfsEchoHandler struct{ payload []byte }

func (h *obfsEchoHandler) NewConnection(_ context.Context, conn net.Conn, metadata M.Metadata) error {
	if metadata.Destination.String() != "target.example:443" {
		return fmt.Errorf("wrong destination: %s", metadata.Destination)
	}
	payload := make([]byte, len(h.payload))
	if _, err := io.ReadFull(conn, payload); err != nil {
		return err
	}
	if !bytes.Equal(payload, h.payload) {
		return fmt.Errorf("request payload changed")
	}
	for _, part := range [][]byte{payload[:31], payload[31:]} {
		if _, err := conn.Write(part); err != nil {
			return err
		}
	}
	return nil
}
func (*obfsEchoHandler) NewPacketConnection(context.Context, N.PacketConn, M.Metadata) error {
	return fmt.Errorf("unexpected UDP connection")
}
func (*obfsEchoHandler) NewError(context.Context, error) {}

// A small wire peer for the two simple-obfs formats, not a TLS server: obfs-tls
// carries SS ciphertext in a ClientHello session ticket and application records.
type obfsPeer struct {
	net.Conn
	mode                  string
	reader                *bufio.Reader
	payload               io.Reader
	firstRead, firstWrite bool
}

func (p *obfsPeer) Read(b []byte) (int, error) {
	if p.mode == "http" {
		if p.firstRead {
			p.firstRead = false
			request, err := http.ReadRequest(p.reader)
			if err != nil {
				return 0, err
			}
			_, port, _ := net.SplitHostPort(p.LocalAddr().String())
			if request.Method != "GET" || request.Host != "cdn.example:"+port || request.Header.Get("Upgrade") != "websocket" {
				return 0, fmt.Errorf("invalid HTTP disguise")
			}
			p.payload = io.MultiReader(request.Body, p.reader)
		}
		return p.payload.Read(b)
	}
	for {
		if p.payload != nil {
			n, err := p.payload.Read(b)
			if err != io.EOF {
				return n, err
			}
			p.payload = nil
			if n > 0 {
				return n, nil
			}
		}
		var header [5]byte
		if _, err := io.ReadFull(p.reader, header[:]); err != nil {
			return 0, err
		}
		record := make([]byte, binary.BigEndian.Uint16(header[3:]))
		if _, err := io.ReadFull(p.reader, record); err != nil {
			return 0, err
		}
		if p.firstRead {
			p.firstRead = false
			if header[0] != 22 || header[1] != 3 || header[2] != 1 {
				return 0, fmt.Errorf("missing TLS disguise")
			}
			ticket, err := obfsClientHelloTicket(record)
			if err != nil {
				return 0, err
			}
			p.payload = bytes.NewReader(ticket)
		} else {
			if header[0] != 23 || header[1] != 3 || header[2] != 3 {
				return 0, fmt.Errorf("invalid TLS data record")
			}
			p.payload = bytes.NewReader(record)
		}
	}
}

func obfsClientHelloTicket(record []byte) ([]byte, error) {
	r := bytes.NewReader(record)
	// Handshake header, version and random precede the variable length fields.
	if len(record) < 38 || record[0] != 1 {
		return nil, fmt.Errorf("invalid ClientHello")
	}
	_, _ = r.Seek(38, io.SeekStart)
	for field, wide := range []bool{false, true, false, true} {
		var size uint16
		if wide {
			if err := binary.Read(r, binary.BigEndian, &size); err != nil {
				return nil, err
			}
		} else {
			v, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			size = uint16(v)
		}
		if int(size) > r.Len() {
			return nil, io.ErrUnexpectedEOF
		}
		// The final vector is the extension list itself.
		if field == 3 {
			if int(size) != r.Len() {
				return nil, fmt.Errorf("invalid extension length")
			}
			break
		}
		_, _ = r.Seek(int64(size), io.SeekCurrent)
	}
	var ticket []byte
	hostOK := false
	for r.Len() > 0 {
		var kind, size uint16
		if err := binary.Read(r, binary.BigEndian, &kind); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.BigEndian, &size); err != nil {
			return nil, err
		}
		value := make([]byte, size)
		if _, err := io.ReadFull(r, value); err != nil {
			return nil, err
		}
		if kind == 35 {
			ticket = value
		}
		if kind == 0 {
			hostOK = len(value) >= 5 && string(value[5:]) == "cdn.example"
		}
	}
	if !hostOK || len(ticket) == 0 {
		return nil, fmt.Errorf("ClientHello missing SNI or encrypted payload")
	}
	return ticket, nil
}

func (p *obfsPeer) Write(b []byte) (int, error) {
	var wire bytes.Buffer
	if p.mode == "http" {
		if p.firstWrite {
			wire.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		}
		wire.Write(b)
	} else {
		if p.firstWrite {
			// simple-obfs server greeting: 91-byte ServerHello and ChangeCipherSpec.
			greeting := make([]byte, 96)
			copy(greeting, []byte{22, 3, 3, 0, 91, 2, 0, 0, 87, 3, 3})
			wire.Write(greeting)
			wire.Write([]byte{20, 3, 3, 0, 1, 1})
		}
		for rest := b; len(rest) > 0; {
			n := min(len(rest), 16384)
			wire.Write([]byte{23, 3, 3, byte(n >> 8), byte(n)})
			wire.Write(rest[:n])
			rest = rest[n:]
		}
	}
	p.firstWrite = false
	_, err := io.Copy(p.Conn, &wire)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

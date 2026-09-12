package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionRedirectHeaderIsolation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		urls    []string
		keep    []bool
		blocked bool
	}{
		{"same origin", []string{"https://8.8.8.8/sub", "https://8.8.8.8:443/next"}, []bool{true, true}, false},
		{"different host", []string{"https://8.8.8.8/sub", "https://1.1.1.1/sub"}, []bool{true, false}, false},
		{"different port", []string{"https://8.8.8.8/sub", "https://8.8.8.8:8443/sub"}, []bool{true, false}, false},
		{"return to origin", []string{"https://8.8.8.8/sub", "https://1.1.1.1/sub", "https://8.8.8.8/final"}, []bool{true, false, false}, false},
		{"upgrade", []string{"http://8.8.8.8/sub", "https://8.8.8.8/sub"}, []bool{true, false}, false},
		{"downgrade", []string{"https://8.8.8.8/sub", "http://8.8.8.8/sub"}, []bool{true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, redirects := 0, 0
			headers := map[string]string{"X-Api-Key": "synthetic-only", "Cookie": "token=synthetic", "User-Agent": "private-client"}
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { redirects++; return nil }}
			client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if calls >= len(tc.keep) {
					t.Fatalf("unexpected redirect reached transport: %s", r.URL)
				}
				for name, value := range headers {
					if got := r.Header.Get(name); (got == value) != tc.keep[calls] {
						t.Errorf("hop %d: header %s retained=%v, want %v", calls, name, got == value, tc.keep[calls])
					}
				}
				if !tc.keep[calls] && r.Header.Get("Referer") != "" {
					t.Errorf("hop %d leaked source URL through Referer", calls)
				}
				calls++
				response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("socks5://127.0.0.1:1080")), Request: r}
				if calls < len(tc.urls) {
					response.StatusCode = 302
					response.Header.Set("Location", tc.urls[calls])
				}
				return response, nil
			})
			_, err := fetchSubscriptionWithClientAndHeaders(context.Background(), client, tc.urls[0], time.Second, false, headers)
			if (err != nil) != tc.blocked {
				t.Fatalf("err=%v, blocked=%v", err, tc.blocked)
			}
			if calls != len(tc.keep) || redirects != len(tc.keep)-1 {
				t.Fatalf("calls=%d redirects=%d", calls, redirects)
			}
		})
	}
}

func TestSubscriptionFetchBoundsOrderedWindow(t *testing.T) {
	const concurrency = 2
	started := make(chan int, 8)
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		index, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		started <- index
		if index == 0 {
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(fmt.Sprintf("socks5://node-%d.test:1080", index))), Request: r}, nil
	})}
	urls := make([]string, 8)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://8.8.8.8/%d", i)
	}
	done := make(chan []SubscriptionSourceResult, 1)
	go func() {
		results, _ := FetchSubscriptionSources(ctx, urls, SubscriptionFetchOptions{Client: client, Concurrency: concurrency})
		done <- results
	}()
	for i := 0; i < concurrency; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("initial window did not start")
		}
	}
	select {
	case index := <-started:
		t.Errorf("source %d escaped the bounded window behind a blocked first source", index)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case results := <-done:
		for i, result := range results {
			if result.Err != nil || len(result.Nodes) != 1 || result.Nodes[0].URI != fmt.Sprintf("socks5://node-%d.test:1080", i) {
				t.Fatalf("result %d out of order or missing: %+v", i, result)
			}
		}
	case <-ctx.Done():
		t.Fatal("fetch did not finish")
	}
}

func TestSubscriptionAggregateBoundaries(t *testing.T) {
	nodes := make([]NodeConfig, MaxSubscriptionNodesTotal)
	if err := ValidateSubscriptionAggregate(nodes); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSubscriptionAggregate(nodes, []NodeConfig{{}}); !errors.Is(err, ErrSubscriptionAggregateLimit) {
		t.Fatalf("count limit: %v", err)
	}
	uri := strings.Repeat("u", MaxSubscriptionNodeURIBytes)
	nodes = make([]NodeConfig, MaxSubscriptionNodeBytesTotal/len(uri))
	for i := range nodes {
		nodes[i].URI = uri
	}
	if err := ValidateSubscriptionAggregate(nodes); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSubscriptionAggregate(nodes, []NodeConfig{{URI: "x"}}); !errors.Is(err, ErrSubscriptionAggregateLimit) {
		t.Fatalf("byte limit: %v", err)
	}
}

func TestStartupRejectsOversizedSubscriptionCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// Loopback is rejected without opening a socket, forcing startup fallback.
	if err := os.WriteFile(path, []byte("subscriptions: [http://127.0.0.1/sub]\nnodes_file: nodes.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cache := []byte(strings.Repeat("socks5://example.test:1080\n", MaxSubscriptionNodesTotal+1))
	nodesPath := filepath.Join(dir, "nodes.txt")
	if err := os.WriteFile(nodesPath, cache, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); !errors.Is(err, ErrSubscriptionAggregateLimit) {
		t.Fatalf("oversized startup cache accepted: %v", err)
	}
	if data, err := os.ReadFile(nodesPath); err != nil || string(data) != string(cache) {
		t.Fatal("rejected cache was modified")
	}
}

func TestSubscriptionCacheStreamsWithStrictLineLimits(t *testing.T) {
	prefix := "socks5://example.test:1080#"
	uri := prefix + strings.Repeat("x", MaxSubscriptionNodeURIBytes-len(prefix))
	for _, tc := range []struct {
		name, content string
		wantError     bool
	}{
		{"max URI without newline", uri, false},
		{"max URI with CRLF", "# comment\r\n\r\n" + uri + "\r\n", false},
		{"oversized URI", uri + "x", true},
		{"oversized comment", "#" + strings.Repeat("x", MaxSubscriptionNodeURIBytes+2), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "nodes.txt")
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			nodes, err := LoadSubscriptionCache(path)
			if errors.Is(err, ErrSubscriptionAggregateLimit) != tc.wantError {
				t.Fatalf("cache error=%v want limit=%v", err, tc.wantError)
			}
			if !tc.wantError && (err != nil || len(nodes) != 1 || nodes[0].URI != uri) {
				t.Fatalf("valid cache corrupted: nodes=%d err=%v", len(nodes), err)
			}
		})
	}
}

package cloudflare

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPFetcherFetchSuccess(t *testing.T) {
	t.Parallel()
	var ipv4Requests atomic.Int32
	var ipv6Requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("User-Agent") != defaultUserAgent {
			t.Errorf("User-Agent = %q, want %q", request.Header.Get("User-Agent"), defaultUserAgent)
		}
		switch request.URL.Path {
		case "/v4":
			ipv4Requests.Add(1)
			_, _ = writer.Write([]byte("192.0.2.0/24\n"))
		case "/v6":
			ipv6Requests.Add(1)
			_, _ = writer.Write([]byte("2001:db8::/32\n"))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	fetcher := newHTTPFetcher(server.Client(), server.URL+"/v4", server.URL+"/v6")
	raw, err := fetcher.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if raw.IPv4 != "192.0.2.0/24\n" || raw.IPv6 != "2001:db8::/32\n" {
		t.Fatalf("Fetch() = %#v", raw)
	}
	if ipv4Requests.Load() != 1 || ipv6Requests.Load() != 1 {
		t.Fatalf("请求数 v4=%d v6=%d", ipv4Requests.Load(), ipv6Requests.Load())
	}
}

func TestNewHTTPFetcherUsesFixedProductionConfiguration(t *testing.T) {
	t.Parallel()
	fetcher := NewHTTPFetcher()
	if fetcher.ipv4URL != cloudflareIPv4URL || fetcher.ipv6URL != cloudflareIPv6URL {
		t.Fatalf("生产端点错误: v4=%q v6=%q", fetcher.ipv4URL, fetcher.ipv6URL)
	}
	if fetcher.client.Timeout != defaultHTTPTimeout {
		t.Fatalf("客户端超时 = %v, want %v", fetcher.client.Timeout, defaultHTTPTimeout)
	}
}

func TestNewHTTPFetcherWithTimeout(t *testing.T) {
	t.Parallel()
	const timeout = 3 * time.Second
	fetcher, err := NewHTTPFetcherWithTimeout(timeout)
	if err != nil {
		t.Fatalf("NewHTTPFetcherWithTimeout() error = %v", err)
	}
	if fetcher.client.Timeout != timeout {
		t.Fatalf("客户端超时 = %v, want %v", fetcher.client.Timeout, timeout)
	}
	if fetcher.ipv4URL != cloudflareIPv4URL || fetcher.ipv6URL != cloudflareIPv6URL {
		t.Fatalf("生产端点错误: v4=%q v6=%q", fetcher.ipv4URL, fetcher.ipv6URL)
	}
}

func TestNewHTTPFetcherWithTimeoutRejectsInvalidValues(t *testing.T) {
	t.Parallel()
	for _, timeout := range []time.Duration{0, -time.Second} {
		fetcher, err := NewHTTPFetcherWithTimeout(timeout)
		if !errors.Is(err, ErrInvalidHTTPTimeout) {
			t.Errorf("NewHTTPFetcherWithTimeout(%v) error = %v, want %v", timeout, err, ErrInvalidHTTPTimeout)
		}
		if fetcher != nil {
			t.Errorf("NewHTTPFetcherWithTimeout(%v) = %v, want nil", timeout, fetcher)
		}
	}
}

func TestHTTPFetcherFamilyFailureReturnsNoPartialResult(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		failedPath string
	}{
		{name: "IPv4 失败", failedPath: "/v4"},
		{name: "IPv6 失败", failedPath: "/v6"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path == test.failedPath {
					http.Error(writer, "失败", http.StatusInternalServerError)
					return
				}
				if request.URL.Path == "/v4" {
					_, _ = writer.Write([]byte("192.0.2.0/24\n"))
					return
				}
				_, _ = writer.Write([]byte("2001:db8::/32\n"))
			}))
			defer server.Close()

			fetcher := newHTTPFetcher(server.Client(), server.URL+"/v4", server.URL+"/v6")
			raw, err := fetcher.Fetch(context.Background())
			if !errors.Is(err, ErrUnexpectedStatus) {
				t.Fatalf("Fetch() error = %v, want %v", err, ErrUnexpectedStatus)
			}
			if raw != (RawRanges{}) {
				t.Fatalf("失败后 Fetch() = %#v, want 零值", raw)
			}
		})
	}
}

func TestHTTPFetcherRejectsEmptyAndOversizedResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want error
	}{
		{name: "空响应", body: " \r\n\t", want: ErrEmptyResponse},
		{name: "超过 64KiB", body: strings.Repeat("x", int(maxResponseBytes)+1), want: ErrResponseTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()

			fetcher := newHTTPFetcher(server.Client(), server.URL, server.URL)
			raw, err := fetcher.Fetch(context.Background())
			if !errors.Is(err, test.want) {
				t.Fatalf("Fetch() error = %v, want %v", err, test.want)
			}
			if raw != (RawRanges{}) {
				t.Fatalf("失败后 Fetch() = %#v, want 零值", raw)
			}
		})
	}
}

func TestHTTPFetcherHonorsContextCancellation(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fetcher := newHTTPFetcher(server.Client(), server.URL, server.URL)
	raw, err := fetcher.Fetch(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Fetch() error = %v, want context.Canceled", err)
	}
	if raw != (RawRanges{}) {
		t.Fatalf("取消后 Fetch() = %#v, want 零值", raw)
	}
}

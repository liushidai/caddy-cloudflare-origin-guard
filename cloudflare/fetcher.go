package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	cloudflareIPv4URL  = "https://www.cloudflare.com/ips-v4"
	cloudflareIPv6URL  = "https://www.cloudflare.com/ips-v6"
	maxResponseBytes   = int64(64 << 10)
	defaultHTTPTimeout = 10 * time.Second
	defaultUserAgent   = "caddy-cloudflare-origin-guard/1"
)

var (
	ErrUnexpectedStatus   = errors.New("Cloudflare CIDR 端点返回非 2xx 状态")
	ErrResponseTooLarge   = errors.New("Cloudflare CIDR 响应超过 64KiB")
	ErrEmptyResponse      = errors.New("Cloudflare CIDR 响应为空")
	ErrInvalidHTTPTimeout = errors.New("HTTP 超时必须大于零")
)

// RawRanges 保存一次事务抓取获得的原始双族响应。
type RawRanges struct {
	IPv4 string
	IPv6 string
}

// Fetcher 获取完整的 IPv4 和 IPv6 原始 CIDR 响应。
type Fetcher interface {
	Fetch(ctx context.Context) (RawRanges, error)
}

// HTTPFetcher 从 Cloudflare 固定生产端点抓取 CIDR。
type HTTPFetcher struct {
	client  *http.Client
	ipv4URL string
	ipv6URL string
}

// NewHTTPFetcher 创建复用单个客户端的生产 Fetcher。
func NewHTTPFetcher() *HTTPFetcher {
	return newProductionHTTPFetcher(defaultHTTPTimeout)
}

// NewHTTPFetcherWithTimeout 使用指定的每请求超时创建生产 Fetcher。
func NewHTTPFetcherWithTimeout(timeout time.Duration) (*HTTPFetcher, error) {
	if timeout <= 0 {
		return nil, ErrInvalidHTTPTimeout
	}
	return newProductionHTTPFetcher(timeout), nil
}

func newProductionHTTPFetcher(timeout time.Duration) *HTTPFetcher {
	return &HTTPFetcher{
		client: &http.Client{
			Timeout: timeout,
		},
		ipv4URL: cloudflareIPv4URL,
		ipv6URL: cloudflareIPv6URL,
	}
}

// newHTTPFetcher 仅供包内测试注入隔离端点和客户端。
func newHTTPFetcher(client *http.Client, ipv4URL, ipv6URL string) *HTTPFetcher {
	return &HTTPFetcher{
		client:  client,
		ipv4URL: ipv4URL,
		ipv6URL: ipv6URL,
	}
}

// Fetch 以双族事务返回结果，任一端失败时返回零值结果。
func (f *HTTPFetcher) Fetch(ctx context.Context) (RawRanges, error) {
	ipv4, err := f.fetchEndpoint(ctx, f.ipv4URL)
	if err != nil {
		return RawRanges{}, fmt.Errorf("抓取 IPv4 CIDR: %w", err)
	}
	ipv6, err := f.fetchEndpoint(ctx, f.ipv6URL)
	if err != nil {
		return RawRanges{}, fmt.Errorf("抓取 IPv6 CIDR: %w", err)
	}
	return RawRanges{IPv4: ipv4, IPv6: ipv6}, nil
}

func (f *HTTPFetcher) fetchEndpoint(ctx context.Context, endpoint string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("创建请求: %w", err)
	}
	request.Header.Set("User-Agent", defaultUserAgent)

	response, err := f.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("发送请求: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("%w: %s", ErrUnexpectedStatus, response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("读取响应: %w", err)
	}
	if int64(len(body)) > maxResponseBytes {
		return "", ErrResponseTooLarge
	}
	if strings.TrimSpace(string(body)) == "" {
		return "", ErrEmptyResponse
	}
	return string(body), nil
}

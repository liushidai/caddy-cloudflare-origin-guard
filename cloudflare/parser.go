package cloudflare

import (
	"bufio"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

const DefaultMaxPrefixes = 1024

// EndpointFamily 表示 CIDR 端点提供的地址族。
type EndpointFamily uint8

const (
	EndpointFamilyIPv4 EndpointFamily = 4
	EndpointFamilyIPv6 EndpointFamily = 6
)

var (
	ErrInvalidEndpointFamily = errors.New("无效的端点地址族")
	ErrInvalidCIDR           = errors.New("非法 CIDR")
	ErrWrongAddressFamily    = errors.New("CIDR 地址族与端点不符")
	ErrTooManyPrefixes       = errors.New("CIDR 数量超过限制")
	ErrInvalidPrefixLimit    = errors.New("CIDR 数量限制必须大于零")
)

// ParseCIDRText 使用默认上限解析指定地址族的 CIDR 文本。
func ParseCIDRText(text string, family EndpointFamily, updatedAt time.Time) (*RangeSet, error) {
	return ParseCIDRTextWithLimit(text, family, updatedAt, DefaultMaxPrefixes)
}

// ParseCIDRTextWithLimit 解析 CIDR 文本并限制去重前的非空条目数量。
func ParseCIDRTextWithLimit(text string, family EndpointFamily, updatedAt time.Time, maxPrefixes int) (*RangeSet, error) {
	if family != EndpointFamilyIPv4 && family != EndpointFamilyIPv6 {
		return nil, ErrInvalidEndpointFamily
	}
	if maxPrefixes <= 0 {
		return nil, ErrInvalidPrefixLimit
	}

	prefixes := make([]netip.Prefix, 0)
	scanner := bufio.NewScanner(strings.NewReader(text))
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if len(prefixes) >= maxPrefixes {
			return nil, fmt.Errorf("%w: 上限为 %d", ErrTooManyPrefixes, maxPrefixes)
		}

		prefix, err := netip.ParsePrefix(line)
		if err != nil {
			return nil, fmt.Errorf("%w（第 %d 行）: %v", ErrInvalidCIDR, lineNumber, err)
		}
		if !familyMatches(prefix.Addr(), family) {
			return nil, fmt.Errorf("%w（第 %d 行）", ErrWrongAddressFamily, lineNumber)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取 CIDR 文本: %w", err)
	}
	if len(prefixes) == 0 {
		return nil, ErrEmptyRangeSet
	}

	return NewRangeSet(prefixes, updatedAt)
}

func familyMatches(addr netip.Addr, family EndpointFamily) bool {
	switch family {
	case EndpointFamilyIPv4:
		return addr.Is4()
	case EndpointFamilyIPv6:
		return addr.Is6() && !addr.Is4In6()
	default:
		return false
	}
}

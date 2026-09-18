package cloudflare

import (
	"fmt"
	"time"
)

// BuildRangeSet 校验完整双族原始响应并构建单个不可变快照。
func BuildRangeSet(raw RawRanges, updatedAt time.Time) (*RangeSet, error) {
	ipv4, err := ParseCIDRText(raw.IPv4, EndpointFamilyIPv4, updatedAt)
	if err != nil {
		return nil, fmt.Errorf("解析 IPv4 CIDR: %w", err)
	}
	ipv6, err := ParseCIDRText(raw.IPv6, EndpointFamilyIPv6, updatedAt)
	if err != nil {
		return nil, fmt.Errorf("解析 IPv6 CIDR: %w", err)
	}

	prefixes := ipv4.Prefixes()
	prefixes = append(prefixes, ipv6.Prefixes()...)
	rangeSet, err := NewRangeSet(prefixes, updatedAt)
	if err != nil {
		return nil, fmt.Errorf("构建完整 CIDR 集合: %w", err)
	}
	return rangeSet, nil
}

package cloudflare

import (
	"errors"
	"net/netip"
	"slices"
	"time"
)

var (
	ErrEmptyRangeSet    = errors.New("CIDR 集合为空")
	ErrInvalidPrefix    = errors.New("无效的 IP 前缀")
	ErrIPv4MappedPrefix = errors.New("不允许 IPv4-mapped IPv6 前缀")
)

// RangeSet 是规范化后不可变的 IP 前缀快照。
type RangeSet struct {
	prefixes  []netip.Prefix
	updatedAt time.Time
	ipv4Count int
	ipv6Count int
}

// NewRangeSet 创建一个规范化、去重并排序的不可变快照。
func NewRangeSet(prefixes []netip.Prefix, updatedAt time.Time) (*RangeSet, error) {
	if len(prefixes) == 0 {
		return nil, ErrEmptyRangeSet
	}

	normalized := make([]netip.Prefix, len(prefixes))
	for i, prefix := range prefixes {
		if !prefix.IsValid() {
			return nil, ErrInvalidPrefix
		}
		if prefix.Addr().Is4In6() {
			return nil, ErrIPv4MappedPrefix
		}
		normalized[i] = prefix.Masked()
	}

	slices.SortFunc(normalized, comparePrefixes)
	normalized = slices.Compact(normalized)

	rangeSet := &RangeSet{
		prefixes:  normalized,
		updatedAt: updatedAt,
	}
	for _, prefix := range normalized {
		if prefix.Addr().Is4() {
			rangeSet.ipv4Count++
		} else {
			rangeSet.ipv6Count++
		}
	}

	return rangeSet, nil
}

func comparePrefixes(left, right netip.Prefix) int {
	if compared := left.Addr().Compare(right.Addr()); compared != 0 {
		return compared
	}
	return left.Bits() - right.Bits()
}

// Contains 报告地址是否属于任一前缀。
func (r *RangeSet) Contains(addr netip.Addr) bool {
	if r == nil || !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range r.prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// Prefixes 返回前缀副本，调用方无法修改快照内部状态。
func (r *RangeSet) Prefixes() []netip.Prefix {
	if r == nil {
		return nil
	}
	return slices.Clone(r.prefixes)
}

// UpdatedAt 返回快照的更新时间。
func (r *RangeSet) UpdatedAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.updatedAt
}

// PrefixCount 返回快照中的前缀总数。
func (r *RangeSet) PrefixCount() int {
	if r == nil {
		return 0
	}
	return len(r.prefixes)
}

// IPv4Count 返回 IPv4 前缀数。
func (r *RangeSet) IPv4Count() int {
	if r == nil {
		return 0
	}
	return r.ipv4Count
}

// IPv6Count 返回 IPv6 前缀数。
func (r *RangeSet) IPv6Count() int {
	if r == nil {
		return 0
	}
	return r.ipv6Count
}

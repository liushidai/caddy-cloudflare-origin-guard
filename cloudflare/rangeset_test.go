package cloudflare

import (
	"net/netip"
	"slices"
	"testing"
	"time"
)

func TestRangeSetContainsAndCounts(t *testing.T) {
	t.Parallel()
	updatedAt := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	input := []netip.Prefix{
		netip.MustParsePrefix("2001:db8:1::1234/48"),
		netip.MustParsePrefix("203.0.113.99/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
	}

	rangeSet, err := NewRangeSet(input, updatedAt)
	if err != nil {
		t.Fatalf("NewRangeSet() error = %v", err)
	}
	if rangeSet.PrefixCount() != 2 || rangeSet.IPv4Count() != 1 || rangeSet.IPv6Count() != 1 {
		t.Fatalf("计数错误: total=%d ipv4=%d ipv6=%d", rangeSet.PrefixCount(), rangeSet.IPv4Count(), rangeSet.IPv6Count())
	}
	if !rangeSet.UpdatedAt().Equal(updatedAt) {
		t.Fatalf("UpdatedAt() = %v, want %v", rangeSet.UpdatedAt(), updatedAt)
	}

	for _, addr := range []string{"203.0.113.7", "::ffff:203.0.113.7", "2001:db8:1::abcd"} {
		if !rangeSet.Contains(netip.MustParseAddr(addr)) {
			t.Errorf("Contains(%s) = false", addr)
		}
	}
	for _, addr := range []string{"198.51.100.1", "2001:db8:2::1"} {
		if rangeSet.Contains(netip.MustParseAddr(addr)) {
			t.Errorf("Contains(%s) = true", addr)
		}
	}

	input[0] = netip.MustParsePrefix("::/0")
	got := rangeSet.Prefixes()
	got[0] = netip.MustParsePrefix("0.0.0.0/0")
	want := []netip.Prefix{
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("2001:db8:1::/48"),
	}
	if !slices.Equal(rangeSet.Prefixes(), want) {
		t.Fatalf("Prefixes() = %v, want %v", rangeSet.Prefixes(), want)
	}
}

func TestNewRangeSetRejectsEmptyAndInvalid(t *testing.T) {
	t.Parallel()
	if _, err := NewRangeSet(nil, time.Time{}); err != ErrEmptyRangeSet {
		t.Fatalf("空集合 error = %v, want %v", err, ErrEmptyRangeSet)
	}
	if _, err := NewRangeSet([]netip.Prefix{{}}, time.Time{}); err != ErrInvalidPrefix {
		t.Fatalf("非法前缀 error = %v, want %v", err, ErrInvalidPrefix)
	}
	if _, err := NewRangeSet([]netip.Prefix{netip.MustParsePrefix("::ffff:192.0.2.0/120")}, time.Time{}); err != ErrIPv4MappedPrefix {
		t.Fatalf("IPv4-mapped IPv6 前缀 error = %v, want %v", err, ErrIPv4MappedPrefix)
	}
}

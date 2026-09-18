package cloudflare

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

func TestBuildRangeSetSuccess(t *testing.T) {
	t.Parallel()
	updatedAt := time.Date(2026, time.September, 19, 14, 0, 0, 0, time.UTC)
	rangeSet, err := BuildRangeSet(RawRanges{
		IPv4: "203.0.113.99/24\n192.0.2.0/24\n",
		IPv6: "2001:db8:1::1234/48\n",
	}, updatedAt)
	if err != nil {
		t.Fatalf("BuildRangeSet() error = %v", err)
	}
	if rangeSet.IPv4Count() != 2 || rangeSet.IPv6Count() != 1 {
		t.Fatalf("计数错误: IPv4=%d IPv6=%d", rangeSet.IPv4Count(), rangeSet.IPv6Count())
	}
	if !rangeSet.Contains(netip.MustParseAddr("203.0.113.7")) || !rangeSet.Contains(netip.MustParseAddr("2001:db8:1::1")) {
		t.Fatal("合并后的 RangeSet 未包含预期地址")
	}
	if !rangeSet.UpdatedAt().Equal(updatedAt) {
		t.Fatalf("UpdatedAt() = %v, want %v", rangeSet.UpdatedAt(), updatedAt)
	}
}

func TestBuildRangeSetRejectsMalformedAndMixedFamilies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  RawRanges
		want error
	}{
		{
			name: "IPv4 畸形 CIDR",
			raw:  RawRanges{IPv4: "invalid", IPv6: "2001:db8::/32"},
			want: ErrInvalidCIDR,
		},
		{
			name: "IPv4 响应混入 IPv6",
			raw:  RawRanges{IPv4: "2001:db8::/32", IPv6: "2001:db8::/32"},
			want: ErrWrongAddressFamily,
		},
		{
			name: "IPv6 响应混入 IPv4",
			raw:  RawRanges{IPv4: "192.0.2.0/24", IPv6: "192.0.2.0/24"},
			want: ErrWrongAddressFamily,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			rangeSet, err := BuildRangeSet(test.raw, time.Time{})
			if !errors.Is(err, test.want) {
				t.Fatalf("BuildRangeSet() error = %v, want %v", err, test.want)
			}
			if rangeSet != nil {
				t.Fatalf("失败后 RangeSet = %v, want nil", rangeSet)
			}
		})
	}
}

func TestBuildFailureDoesNotReplaceLastKnownGoodSnapshot(t *testing.T) {
	t.Parallel()
	lastKnownGood := mustRangeSet(t, "192.0.2.0/24", time.Unix(1, 0))
	store := NewSnapshotStore(lastKnownGood)

	built, err := BuildRangeSet(RawRanges{
		IPv4: "invalid",
		IPv6: "2001:db8::/32",
	}, time.Unix(2, 0))
	if err == nil || built != nil {
		t.Fatalf("BuildRangeSet() = %v, %v; want nil, error", built, err)
	}
	if store.Publish(built) {
		t.Fatal("Publish(nil) = true, want false")
	}
	if got := store.Load(); got != lastKnownGood {
		t.Fatalf("构建失败后 Load() = %p, want %p", got, lastKnownGood)
	}
}

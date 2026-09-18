package cloudflare

import (
	"net/netip"
	"testing"
	"time"
)

var benchmarkSnapshotResult *RangeSet

func BenchmarkSnapshotLoad(b *testing.B) {
	snapshot, err := NewRangeSet([]netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}, time.Unix(1, 0).UTC())
	if err != nil {
		b.Fatal(err)
	}
	store := NewSnapshotStore(snapshot)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkSnapshotResult = store.Load()
	}
}

package cloudflareorigin

import (
	"net/http"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liushidai/caddy-cloudflare-origin-guard/cloudflare"
)

var benchmarkMatcherResult atomic.Bool

func BenchmarkMatcherIPv4(b *testing.B) {
	matcher, request := newBenchmarkMatcher(b, "192.0.2.10:443")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkMatcherResult.Store(matcher.Match(request))
	}
}

func BenchmarkMatcherIPv6(b *testing.B) {
	matcher, request := newBenchmarkMatcher(b, "[2001:db8::10]:443")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkMatcherResult.Store(matcher.Match(request))
	}
}

func BenchmarkMatcherParallel(b *testing.B) {
	matcher, request := newBenchmarkMatcher(b, "192.0.2.10:443")
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			benchmarkMatcherResult.Store(matcher.Match(request))
		}
	})
}

func newBenchmarkMatcher(b *testing.B, remoteAddr string) (*OriginMatcher, *http.Request) {
	b.Helper()
	snapshot, err := cloudflare.NewRangeSet([]netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}, time.Unix(1, 0).UTC())
	if err != nil {
		b.Fatal(err)
	}
	app := &App{store: cloudflare.NewSnapshotStore(snapshot)}
	return &OriginMatcher{app: app}, &http.Request{RemoteAddr: remoteAddr}
}

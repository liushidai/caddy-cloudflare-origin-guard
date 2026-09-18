package cloudflare

import (
	"testing"
	"time"
)

var fuzzUpdatedAt = time.Unix(1, 0).UTC()

func FuzzParseCIDRResponse(f *testing.F) {
	f.Add("192.0.2.0/24\n", "2001:db8::/32\n")
	f.Add("not-a-cidr", "also-not-a-cidr")
	f.Add("2001:db8::/32", "192.0.2.0/24")
	f.Add("::ffff:192.0.2.0/120", "::ffff:2001:db8::/120")
	f.Add("192.0.2.1/32\n198.51.100.0/24", "2001:db8::1/128\nfe80::/10")
	f.Fuzz(func(t *testing.T, ipv4Response, ipv6Response string) {
		_, _ = BuildRangeSet(RawRanges{IPv4: ipv4Response, IPv6: ipv6Response}, fuzzUpdatedAt)
	})
}

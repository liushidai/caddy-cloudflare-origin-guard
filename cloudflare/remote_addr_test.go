package cloudflare

import (
	"net/netip"
	"testing"
	"time"
)

func TestParseRemoteAddr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		remoteAddr string
		want       netip.Addr
	}{
		{remoteAddr: "203.0.113.7:443", want: netip.MustParseAddr("203.0.113.7")},
		{remoteAddr: "[2001:db8::1]:443", want: netip.MustParseAddr("2001:db8::1")},
		{remoteAddr: "[fe80::1%eth0]:443", want: netip.MustParseAddr("fe80::1")},
		{remoteAddr: "[::ffff:203.0.113.7]:443", want: netip.MustParseAddr("203.0.113.7")},
	}
	for _, test := range tests {
		got, err := ParseRemoteAddr(test.remoteAddr)
		if err != nil {
			t.Errorf("ParseRemoteAddr(%q) error = %v", test.remoteAddr, err)
			continue
		}
		if got != test.want {
			t.Errorf("ParseRemoteAddr(%q) = %v, want %v", test.remoteAddr, got, test.want)
		}
	}
}

func TestContainsRemoteAddrUsesOnlyRemoteAddr(t *testing.T) {
	t.Parallel()
	rangeSet, err := NewRangeSet([]netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	got, err := ContainsRemoteAddr(rangeSet, "203.0.113.7:12345")
	if err != nil || !got {
		t.Fatalf("ContainsRemoteAddr() = %v, %v; want true, nil", got, err)
	}
	got, err = ContainsRemoteAddr(rangeSet, "198.51.100.1:12345")
	if err != nil || got {
		t.Fatalf("ContainsRemoteAddr() = %v, %v; want false, nil", got, err)
	}
	got, err = ContainsRemoteAddr(rangeSet, "203.0.113.7")
	if err == nil || got {
		t.Fatalf("非法地址 ContainsRemoteAddr() = %v, %v; want false, error", got, err)
	}
}

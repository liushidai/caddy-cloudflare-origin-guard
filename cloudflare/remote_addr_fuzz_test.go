package cloudflare

import "testing"

func FuzzRemoteAddr(f *testing.F) {
	f.Add("203.0.113.7:443")
	f.Add("[2001:db8::1]:443")
	f.Add("[fe80::1%eth0]:443")
	f.Add("[::ffff:203.0.113.7]:443")
	f.Add("not-an-address")
	f.Add("")
	f.Fuzz(func(t *testing.T, remoteAddr string) {
		_, _ = ParseRemoteAddr(remoteAddr)
		_, _ = ContainsRemoteAddr(nil, remoteAddr)
	})
}

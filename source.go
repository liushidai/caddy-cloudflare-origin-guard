package cloudflareorigin

import (
	"errors"
	"net/http"
	"net/netip"
	"sync/atomic"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/liushidai/caddy-cloudflare-origin-guard/cloudflare"
)

// IPRangeSource exposes the app snapshot to trusted_proxies without owning it.
type IPRangeSource struct {
	app    *App
	cached atomic.Pointer[cachedRanges]
}

type cachedRanges struct {
	snapshot *cloudflareRangeSet
	prefixes []netip.Prefix
}

// cloudflareRangeSet is kept as an alias to make the cache record concise.
type cloudflareRangeSet = cloudflare.RangeSet

func init() {
	caddy.RegisterModule(IPRangeSource{})
}

// CaddyModule returns the IP source module information.
func (IPRangeSource) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.ip_sources.cloudflare",
		New: func() caddy.Module { return new(IPRangeSource) },
	}
}

// Provision obtains the already-provisioned app from Caddy.
func (s *IPRangeSource) Provision(ctx caddy.Context) error {
	value, err := ctx.App(appID)
	if err != nil {
		return err
	}
	app, ok := value.(*App)
	if !ok || app == nil || app.store == nil {
		return errors.New("cloudflare_origin app 类型无效或未初始化")
	}
	s.app = app
	return nil
}

// GetIPRanges returns the app snapshot prefixes without per-request cloning.
func (s *IPRangeSource) GetIPRanges(_ *http.Request) []netip.Prefix {
	if s == nil || s.app == nil || s.app.store == nil {
		return nil
	}
	snapshot := s.app.store.Load()
	for {
		old := s.cached.Load()
		if old != nil && old.snapshot == snapshot {
			return old.prefixes
		}
		var prefixes []netip.Prefix
		if snapshot != nil {
			prefixes = snapshot.Prefixes()
		}
		fresh := &cachedRanges{snapshot: snapshot, prefixes: prefixes}
		if s.cached.CompareAndSwap(old, fresh) {
			return prefixes
		}
	}
}

// UnmarshalCaddyfile accepts the bare trusted_proxies cloudflare form.
func (s *IPRangeSource) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	if !d.Next() {
		return nil
	}
	if d.NextArg() {
		return d.ArgErr()
	}
	if d.NextBlock(0) {
		return d.Err("cloudflare IP source 不支持块参数")
	}
	return nil
}

var (
	_ caddy.Module            = (*IPRangeSource)(nil)
	_ caddy.Provisioner       = (*IPRangeSource)(nil)
	_ caddyfile.Unmarshaler   = (*IPRangeSource)(nil)
	_ caddyhttp.IPRangeSource = (*IPRangeSource)(nil)
)

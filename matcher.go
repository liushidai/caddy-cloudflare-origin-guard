package cloudflareorigin

import (
	"errors"
	"net/http"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/liushidai/caddy-cloudflare-origin-guard/cloudflare"
)

// OriginMatcher matches the direct remote address against the app snapshot.
type OriginMatcher struct {
	app *App
}

func init() {
	caddy.RegisterModule(OriginMatcher{})
}

// CaddyModule returns the matcher module information.
func (OriginMatcher) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.matchers.cloudflare_origin",
		New: func() caddy.Module { return new(OriginMatcher) },
	}
}

// Provision obtains the already-provisioned app from Caddy.
func (m *OriginMatcher) Provision(ctx caddy.Context) error {
	value, err := ctx.App(appID)
	if err != nil {
		return err
	}
	app, ok := value.(*App)
	if !ok || app == nil || app.store == nil {
		return errors.New("cloudflare_origin app 类型无效或未初始化")
	}
	m.app = app
	return nil
}

// MatchWithError matches only RemoteAddr; malformed or unavailable addresses do not match.
func (m *OriginMatcher) MatchWithError(r *http.Request) (bool, error) {
	if m == nil || m.app == nil || m.app.store == nil || r == nil {
		return false, nil
	}
	matched, err := cloudflare.ContainsRemoteAddr(m.app.store.Load(), r.RemoteAddr)
	if err != nil {
		return false, nil
	}
	return matched, nil
}

// Match keeps compatibility with the legacy matcher interface.
func (m *OriginMatcher) Match(r *http.Request) bool {
	matched, _ := m.MatchWithError(r)
	return matched
}

// UnmarshalCaddyfile accepts the bare cloudflare_origin matcher form.
func (m *OriginMatcher) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	if !d.Next() {
		return nil
	}
	if d.NextArg() {
		return d.ArgErr()
	}
	if d.NextBlock(0) {
		return d.Err("cloudflare_origin matcher 不支持块参数")
	}
	return nil
}

var (
	_ caddy.Module                      = (*OriginMatcher)(nil)
	_ caddy.Provisioner                 = (*OriginMatcher)(nil)
	_ caddyfile.Unmarshaler             = (*OriginMatcher)(nil)
	_ caddyhttp.RequestMatcher          = (*OriginMatcher)(nil)
	_ caddyhttp.RequestMatcherWithError = (*OriginMatcher)(nil)
)

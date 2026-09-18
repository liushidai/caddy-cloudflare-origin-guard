package cloudflareorigin

import (
	"fmt"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
)

func init() {
	httpcaddyfile.RegisterGlobalOption(appID, parseApp)
}

func parseApp(d *caddyfile.Dispenser, existingVal any) (any, error) {
	if existingVal != nil {
		return nil, fmt.Errorf("全局选项 %s 不能重复", appID)
	}
	config := new(App)
	d.Next()
	if d.NextArg() {
		return nil, d.ArgErr()
	}
	seen := make(map[string]bool)
	for d.NextBlock(0) {
		name := d.Val()
		if seen[name] {
			return nil, d.Errf("全局选项 %s 重复", name)
		}
		seen[name] = true
		var target *caddy.Duration
		switch name {
		case "refresh_interval":
			target = &config.RefreshInterval
		case "timeout":
			target = &config.Timeout
		case "max_stale":
			target = &config.MaxStale
		default:
			return nil, d.Errf("未知 cloudflare_origin 子指令: %s", name)
		}
		if !d.NextArg() {
			return nil, d.ArgErr()
		}
		duration, err := caddy.ParseDuration(d.Val())
		if err != nil {
			return nil, err
		}
		*target = caddy.Duration(duration)
		if d.NextArg() {
			return nil, d.ArgErr()
		}
	}
	return httpcaddyfile.App{Name: appID, Value: caddyconfig.JSON(config, nil)}, nil
}

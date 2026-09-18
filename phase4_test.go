package cloudflareorigin

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/liushidai/caddy-cloudflare-origin-guard/cloudflare"
)

type fakeCache struct{}

func (fakeCache) Load(context.Context) (cloudflare.CachedRanges, error) {
	return cloudflare.CachedRanges{}, cloudflare.ErrCacheMiss
}

func (fakeCache) Save(context.Context, cloudflare.CachedRanges) error { return nil }

type fakeUpdater struct {
	store    *cloudflare.SnapshotStore
	starts   atomic.Int64
	initErr  error
	initDone chan struct{}
}

func (u *fakeUpdater) Initialize(context.Context) (cloudflare.UpdateSource, error) {
	if u.initErr != nil {
		return "", u.initErr
	}
	snapshot, err := cloudflare.NewRangeSet([]netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}, time.Unix(1, 0).UTC())
	if err != nil {
		return "", err
	}
	u.store.Publish(snapshot)
	return cloudflare.SourceNetwork, nil
}

func (u *fakeUpdater) Run(ctx context.Context) error {
	u.starts.Add(1)
	if u.initDone != nil {
		close(u.initDone)
	}
	<-ctx.Done()
	return nil
}

type fakeFetcher struct{}

func (fakeFetcher) Fetch(context.Context) (cloudflare.RawRanges, error) {
	return cloudflare.RawRanges{}, errors.New("测试 Fetcher 不应被调用")
}

func testApp(updater *fakeUpdater) *App {
	return &App{
		newFetcher: func(time.Duration) (cloudflare.Fetcher, error) { return fakeFetcher{}, nil },
		newCache:   func(string) (cloudflare.RangeCache, error) { return fakeCache{}, nil },
		newUpdater: func(_ cloudflare.Fetcher, _ cloudflare.RangeCache, store *cloudflare.SnapshotStore, _ cloudflare.UpdaterConfig, _ *slog.Logger) (updaterRunner, error) {
			updater.store = store
			return updater, nil
		},
		cachePath: func() string { return "/tmp/cloudflare-origin-test/lkg.json" },
	}
}

func TestModuleRegistrationAndInterfaceGuards(t *testing.T) {
	t.Parallel()
	for _, id := range []string{appID, "http.ip_sources.cloudflare", "http.matchers.cloudflare_origin"} {
		if _, err := caddy.GetModule(id); err != nil {
			t.Fatalf("模块 %s 未注册: %v", id, err)
		}
	}
	var _ caddy.Module = (*App)(nil)
	var _ caddy.App = (*App)(nil)
	var _ caddyhttp.IPRangeSource = (*IPRangeSource)(nil)
	var _ caddyhttp.RequestMatcher = (*OriginMatcher)(nil)
	var _ caddyhttp.RequestMatcherWithError = (*OriginMatcher)(nil)
}

func TestAppProvisionStartStopAndSharedStore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		updater := &fakeUpdater{initDone: make(chan struct{})}
		app := testApp(updater)
		if err := app.Provision(caddy.Context{}); err != nil {
			t.Fatal(err)
		}
		if app.store == nil || updater.store != app.store {
			t.Fatal("App 与 updater 未共享同一个 SnapshotStore")
		}
		source := &IPRangeSource{app: app}
		matcher := &OriginMatcher{app: app}
		if source.app != matcher.app || source.app.store != matcher.app.store {
			t.Fatal("source/matcher 未指向同一 App/store")
		}
		if err := app.Start(); err != nil {
			t.Fatal(err)
		}
		if err := app.Start(); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := updater.starts.Load(); got != 1 {
			t.Fatalf("重复 Start 启动 Run 次数 = %d, want 1", got)
		}
		if err := app.Stop(); err != nil {
			t.Fatal(err)
		}
		if err := app.Stop(); err != nil {
			t.Fatal(err)
		}
		if err := app.Cleanup(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAppFailedProvisionAndNilSafeCleanup(t *testing.T) {
	t.Parallel()
	var nilApp *App
	if err := nilApp.Cleanup(); err != nil {
		t.Fatal(err)
	}
	app := &App{
		newFetcher: func(time.Duration) (cloudflare.Fetcher, error) { return fakeFetcher{}, nil },
		newCache:   func(string) (cloudflare.RangeCache, error) { return fakeCache{}, nil },
		newUpdater: func(_ cloudflare.Fetcher, _ cloudflare.RangeCache, store *cloudflare.SnapshotStore, _ cloudflare.UpdaterConfig, _ *slog.Logger) (updaterRunner, error) {
			return &fakeUpdater{store: store, initErr: errors.New("无网络且无缓存")}, nil
		},
		cachePath: func() string { return "/tmp/cloudflare-origin-test/lkg.json" },
	}
	if err := app.Provision(caddy.Context{}); err == nil {
		t.Fatal("无网络且无有效缓存时 Provision 意外成功")
	}
	if err := app.Cleanup(); err != nil {
		t.Fatal(err)
	}
}

func testSnapshotStore(t *testing.T) *cloudflare.SnapshotStore {
	t.Helper()
	snapshot, err := cloudflare.NewRangeSet([]netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	return cloudflare.NewSnapshotStore(snapshot)
}

func TestOriginMatcherRemoteAddressOnly(t *testing.T) {
	t.Parallel()
	app := &App{store: testSnapshotStore(t)}
	matcher := &OriginMatcher{app: app}
	tests := []struct {
		name   string
		remote string
		want   bool
	}{
		{name: "IPv4", remote: "192.0.2.10:443", want: true},
		{name: "IPv6", remote: "[2001:db8::10]:443", want: true},
		{name: "IPv4 mapped", remote: "[::ffff:192.0.2.10]:443", want: true},
		{name: "malformed", remote: "192.0.2.10", want: false},
		{name: "untrusted spoof header", remote: "198.51.100.10:443", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
			req.RemoteAddr = test.remote
			req.Header.Set("CF-Connecting-IP", "192.0.2.10")
			req.Header.Set("X-Forwarded-For", "192.0.2.10")
			got, err := matcher.MatchWithError(req)
			if err != nil || got != test.want {
				t.Fatalf("MatchWithError() = %v, %v; want %v, nil", got, err, test.want)
			}
		})
	}
	if got, err := (&OriginMatcher{app: &App{store: cloudflare.NewSnapshotStore(nil)}}).MatchWithError(httptest.NewRequest(http.MethodGet, "/", nil)); err != nil || got {
		t.Fatalf("空 snapshot MatchWithError() = %v, %v", got, err)
	}
}

func TestOriginMatcherSecurityRegression(t *testing.T) {
	t.Parallel()
	matcher := &OriginMatcher{app: &App{store: testSnapshotStore(t)}}

	attacker := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	attacker.RemoteAddr = "198.51.100.10:443"
	attacker.Header.Set("CF-Connecting-IP", "192.0.2.10")
	attacker.Header.Set("X-Forwarded-For", "192.0.2.10")
	if got, err := matcher.MatchWithError(attacker); err != nil || got {
		t.Fatalf("攻击者伪造头 MatchWithError() = %v, %v; want false, nil", got, err)
	}

	cloudflarePeer := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	cloudflarePeer.RemoteAddr = "192.0.2.10:443"
	if got, err := matcher.MatchWithError(cloudflarePeer); err != nil || !got {
		t.Fatalf("可信 peer 无头 MatchWithError() = %v, %v; want true, nil", got, err)
	}
}

func TestIPRangeSourceSnapshotCache(t *testing.T) {
	app := &App{store: testSnapshotStore(t)}
	source := &IPRangeSource{app: app}
	first := source.GetIPRanges(nil)
	if len(first) != 2 {
		t.Fatalf("首个范围数 = %d", len(first))
	}
	if got := testing.AllocsPerRun(1000, func() { _ = source.GetIPRanges(nil) }); got != 0 {
		t.Fatalf("稳定 GetIPRanges 分配数 = %v, want 0", got)
	}
	updated, err := cloudflare.NewRangeSet([]netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, time.Unix(2, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	app.store.Publish(updated)
	second := source.GetIPRanges(nil)
	if len(second) != 1 || second[0].String() != "198.51.100.0/24" {
		t.Fatalf("快照更新后范围 = %v", second)
	}
}

func TestCaddyfileAdapterProducesAppTrustedProxyAndMatcher(t *testing.T) {
	t.Parallel()
	input := []byte(`{
	cloudflare_origin {
		refresh_interval 2h
		timeout 3s
		max_stale 4d
	}
	servers :443 {
		trusted_proxies cloudflare
	}
}

:443 {
	@cf cloudflare_origin
	respond @cf "protected"
	respond "direct"
}`)
	adapter := caddyfile.Adapter{ServerType: httpcaddyfile.ServerType{}}
	data, _, err := adapter.Adapt(input, nil)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	apps := document["apps"].(map[string]any)
	appConfig := apps[appID].(map[string]any)
	if appConfig["refresh_interval"] != float64(2*time.Hour) || appConfig["timeout"] != float64(3*time.Second) || appConfig["max_stale"] != float64(96*time.Hour) {
		t.Fatalf("app JSON = %#v", appConfig)
	}
	httpApp := apps["http"].(map[string]any)
	servers := httpApp["servers"].(map[string]any)
	server := servers["srv0"].(map[string]any)
	trusted := server["trusted_proxies"].(map[string]any)
	if trusted["source"] != "cloudflare" {
		t.Fatalf("trusted_proxies = %#v", trusted)
	}
	encoded, ok := server["routes"].([]any)
	if !ok || len(encoded) == 0 {
		t.Fatalf("路由未编译: %#v", server["routes"])
	}
}

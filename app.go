package cloudflareorigin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/liushidai/caddy-cloudflare-origin-guard/cloudflare"
	"go.uber.org/zap/exp/zapslog"
)

const (
	appID       = "cloudflare_origin"
	defaultPath = "lkg.json"
	stopTimeout = 5 * time.Second
)

type updaterRunner interface {
	Initialize(context.Context) (cloudflare.UpdateSource, error)
	Run(context.Context) error
}

// App 管理当前配置代的唯一快照存储和更新器。
type App struct {
	RefreshInterval caddy.Duration `json:"refresh_interval,omitempty"`
	Timeout         caddy.Duration `json:"timeout,omitempty"`
	MaxStale        caddy.Duration `json:"max_stale,omitempty"`

	store   *cloudflare.SnapshotStore
	updater updaterRunner
	logger  *slog.Logger

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	started bool

	newFetcher func(time.Duration) (cloudflare.Fetcher, error)
	newCache   func(string) (cloudflare.RangeCache, error)
	newUpdater func(cloudflare.Fetcher, cloudflare.RangeCache, *cloudflare.SnapshotStore, cloudflare.UpdaterConfig, *slog.Logger) (updaterRunner, error)
	cachePath  func() string
}

func init() {
	caddy.RegisterModule(App{})
}

// CaddyModule returns the app module information.
func (App) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  appID,
		New: func() caddy.Module { return new(App) },
	}
}

// Provision creates the per-configuration core objects and loads the LKG.
func (a *App) Provision(ctx caddy.Context) error {
	if a == nil {
		return errors.New("cloudflare_origin app 为空")
	}
	logger := slog.New(zapslog.NewHandler(ctx.Logger().Core(), zapslog.WithName(appID)))
	a.logger = logger
	a.store = cloudflare.NewSnapshotStore(nil)

	coreConfig := cloudflare.DefaultUpdaterConfig()
	if a.RefreshInterval != 0 {
		coreConfig.Refresh = time.Duration(a.RefreshInterval)
	}
	if a.Timeout != 0 {
		coreConfig.Timeout = time.Duration(a.Timeout)
	}
	if a.MaxStale != 0 {
		coreConfig.MaxStale = time.Duration(a.MaxStale)
	}

	newFetcher := a.newFetcher
	if newFetcher == nil {
		newFetcher = func(timeout time.Duration) (cloudflare.Fetcher, error) {
			return cloudflare.NewHTTPFetcherWithTimeout(timeout)
		}
	}
	newCache := a.newCache
	if newCache == nil {
		newCache = func(path string) (cloudflare.RangeCache, error) {
			return cloudflare.NewFileCache(path)
		}
	}
	newUpdater := a.newUpdater
	if newUpdater == nil {
		newUpdater = func(fetcher cloudflare.Fetcher, cache cloudflare.RangeCache, store *cloudflare.SnapshotStore, config cloudflare.UpdaterConfig, log *slog.Logger) (updaterRunner, error) {
			return cloudflare.NewUpdater(fetcher, cache, store, config, log)
		}
	}
	path := filepath.Join(caddy.AppDataDir(), appID, defaultPath)
	if a.cachePath != nil {
		path = a.cachePath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		logger.Warn("创建缓存目录失败，将继续使用网络快照", "path", filepath.Dir(path), "error", err)
	}
	fetcher, err := newFetcher(coreConfig.Timeout)
	if err != nil {
		return fmt.Errorf("创建 Cloudflare Fetcher: %w", err)
	}
	cache, err := newCache(path)
	if err != nil {
		return fmt.Errorf("创建 Cloudflare 缓存: %w", err)
	}
	updater, err := newUpdater(fetcher, cache, a.store, coreConfig, logger)
	if err != nil {
		return fmt.Errorf("创建 Cloudflare 更新器: %w", err)
	}
	a.updater = updater
	if _, err := updater.Initialize(ctx.Context); err != nil {
		return fmt.Errorf("初始化 Cloudflare 来源: %w", err)
	}
	return nil
}

// Start launches the refresh loop at most once.
func (a *App) Start() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started {
		return nil
	}
	if a.updater == nil {
		return errors.New("cloudflare_origin app 未成功 Provision")
	}
	a.started = true
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.done = make(chan struct{})
	updater := a.updater
	done := a.done
	go func() {
		if err := updater.Run(ctx); err != nil && a.logger != nil {
			a.logger.Warn("Cloudflare 周期刷新退出", "error", err)
		}
		close(done)
	}()
	return nil
}

// Stop cancels and waits for the refresh loop; it is safe to call repeatedly.
func (a *App) Stop() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	cancel := a.cancel
	done := a.done
	a.mu.Unlock()
	if cancel == nil || done == nil {
		return nil
	}
	cancel()
	timer := time.NewTimer(stopTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return errors.New("等待 Cloudflare 更新器退出超时")
	}
}

// Cleanup releases the refresh loop and is safe after failed Provision.
func (a *App) Cleanup() error {
	if a == nil {
		return nil
	}
	return a.Stop()
}

var (
	_ caddy.Module       = (*App)(nil)
	_ caddy.Provisioner  = (*App)(nil)
	_ caddy.App          = (*App)(nil)
	_ caddy.CleanerUpper = (*App)(nil)
)

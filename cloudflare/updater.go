package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"time"
)

var (
	ErrNilFetcher      = errors.New("Fetcher 不能为空")
	ErrNilCache        = errors.New("缓存不能为空")
	ErrNilStore        = errors.New("SnapshotStore 不能为空")
	ErrInvalidRefresh  = errors.New("Refresh 必须大于零")
	ErrInvalidTimeout  = errors.New("Timeout 必须大于零")
	ErrInvalidJitter   = errors.New("Jitter 必须大于等于零且小于 Refresh")
	ErrInvalidMaxStale = errors.New("MaxStale 必须大于零")
	ErrCacheExpired    = errors.New("缓存已过期")
	ErrNetworkFailure  = errors.New("网络更新失败")
)

// UpdaterConfig 控制更新和缓存回退策略。
type UpdaterConfig struct {
	Refresh  time.Duration
	Timeout  time.Duration
	Jitter   time.Duration
	MaxStale time.Duration
}

// DefaultUpdaterConfig 返回生产默认配置。
func DefaultUpdaterConfig() UpdaterConfig {
	return UpdaterConfig{Refresh: time.Hour, Timeout: 10 * time.Second, Jitter: 5 * time.Minute, MaxStale: 30 * 24 * time.Hour}
}

// UpdateSource 表示快照来源。
type UpdateSource string

const (
	SourceNetwork UpdateSource = "network"
	SourceCache   UpdateSource = "cache"
)

// Updater 协调网络更新、缓存回退和快照发布。
type Updater struct {
	fetcher Fetcher
	cache   RangeCache
	store   *SnapshotStore
	cfg     UpdaterConfig
	logger  *slog.Logger
	now     func() time.Time
	random  func() float64
}

// NewUpdater 创建更新器。
func NewUpdater(fetcher Fetcher, cache RangeCache, store *SnapshotStore, cfg UpdaterConfig, logger *slog.Logger) (*Updater, error) {
	if fetcher == nil {
		return nil, ErrNilFetcher
	}
	if cache == nil {
		return nil, ErrNilCache
	}
	if store == nil {
		return nil, ErrNilStore
	}
	if cfg.Refresh <= 0 {
		return nil, ErrInvalidRefresh
	}
	if cfg.Timeout <= 0 {
		return nil, ErrInvalidTimeout
	}
	if cfg.MaxStale <= 0 {
		return nil, ErrInvalidMaxStale
	}
	if cfg.Jitter < 0 || cfg.Jitter >= cfg.Refresh {
		return nil, ErrInvalidJitter
	}
	if logger == nil {
		return nil, errors.New("logger 不能为空")
	}
	return &Updater{fetcher: fetcher, cache: cache, store: store, cfg: cfg, logger: logger, now: time.Now, random: rand.Float64}, nil
}

// Initialize 先尝试网络，失败后再使用未过期缓存。
func (u *Updater) Initialize(ctx context.Context) (UpdateSource, error) {
	snapshot, err := u.fetchAndBuild(ctx)
	if err == nil {
		u.store.Publish(snapshot)
		if saveErr := u.saveSnapshot(ctx, snapshot); saveErr != nil {
			u.logger.Warn("保存网络快照缓存失败", "error", saveErr)
		}
		return SourceNetwork, nil
	}
	networkErr := fmt.Errorf("%w: %v", ErrNetworkFailure, err)
	source, cacheErr := u.loadCache(ctx)
	if cacheErr == nil {
		return source, nil
	}
	return "", errors.Join(networkErr, cacheErr)
}

// Refresh 仅执行网络更新，失败时保持当前快照不变。
func (u *Updater) Refresh(ctx context.Context) error {
	snapshot, err := u.fetchAndBuild(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNetworkFailure, err)
	}
	u.store.Publish(snapshot)
	if saveErr := u.saveSnapshot(ctx, snapshot); saveErr != nil {
		u.logger.Warn("保存网络快照缓存失败", "error", saveErr)
	}
	return nil
}

func (u *Updater) fetchAndBuild(parent context.Context) (*RangeSet, error) {
	ctx, cancel := context.WithTimeout(parent, u.cfg.Timeout)
	defer cancel()
	raw, err := u.fetcher.Fetch(ctx)
	if err != nil {
		return nil, err
	}
	return BuildRangeSet(raw, u.now())
}

func (u *Updater) saveSnapshot(ctx context.Context, snapshot *RangeSet) error {
	return u.cache.Save(ctx, CachedRangesFromRangeSet(snapshot))
}

func (u *Updater) loadCache(ctx context.Context) (UpdateSource, error) {
	cached, err := u.cache.Load(ctx)
	if err != nil {
		return "", err
	}
	raw, updatedAt, err := rawRangesFromCached(cached)
	if err != nil {
		return "", err
	}
	if age := u.now().Sub(updatedAt); age > u.cfg.MaxStale {
		return "", fmt.Errorf("%w: age=%s", ErrCacheExpired, age)
	}
	if updatedAt.After(u.now()) {
		u.logger.Warn("缓存时间晚于当前时间", "updated_at", updatedAt)
	}
	snapshot, err := BuildRangeSet(raw, updatedAt)
	if err != nil {
		return "", fmt.Errorf("%w: CIDR 无效: %v", ErrCacheCorrupt, err)
	}
	u.store.Publish(snapshot)
	return SourceCache, nil
}

// Run 按周期刷新；调用方负责在自己的 goroutine 中运行。
func (u *Updater) Run(ctx context.Context) error {
	for {
		delay := u.refreshDelay()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
			if ctx.Err() != nil {
				return nil
			}
			if err := u.Refresh(ctx); err != nil {
				u.logger.Warn("周期刷新失败", "error", err)
			}
		}
	}
}

func (u *Updater) refreshDelay() time.Duration {
	return u.cfg.Refresh + time.Duration((u.random()*2-1)*float64(u.cfg.Jitter))
}

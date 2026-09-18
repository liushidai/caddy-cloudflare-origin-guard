package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type stubFetcher struct {
	mu     sync.Mutex
	raw    RawRanges
	err    error
	calls  int
	called chan<- struct{}
}

func (f *stubFetcher) Fetch(ctx context.Context) (RawRanges, error) {
	f.mu.Lock()
	f.calls++
	raw, err := f.raw, f.err
	called := f.called
	f.mu.Unlock()
	if called != nil {
		select {
		case called <- struct{}{}:
		default:
		}
	}
	select {
	case <-ctx.Done():
		return RawRanges{}, ctx.Err()
	default:
	}
	return raw, err
}

func (f *stubFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type memoryCache struct {
	mu      sync.Mutex
	value   CachedRanges
	loadErr error
	saveErr error
	saves   int
	saved   chan<- struct{}
}

func (c *memoryCache) Load(_ context.Context) (CachedRanges, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value, c.loadErr
}

func (c *memoryCache) Save(_ context.Context, value CachedRanges) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.saves++
	if c.saveErr != nil {
		return c.saveErr
	}
	c.value = value
	if c.saved != nil {
		select {
		case c.saved <- struct{}{}:
		default:
		}
	}
	return nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

func validRaw() RawRanges {
	return RawRanges{IPv4: "192.0.2.0/24\n", IPv6: "2001:db8::/32\n"}
}

func validCached(now time.Time) CachedRanges {
	return CachedRanges{Version: 1, UpdatedAt: now.Format(time.RFC3339), IPv4: []string{"192.0.2.0/24"}, IPv6: []string{"2001:db8::/32"}}
}

func TestFileCacheRoundTripAndFormat(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ranges.json")
	cache, err := NewFileCache(path)
	if err != nil {
		t.Fatal(err)
	}
	want := validCached(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if err := cache.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := cache.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.UpdatedAt != want.UpdatedAt || strings.Join(got.IPv4, ",") != "192.0.2.0/24" {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if len(document) != 4 {
		t.Fatalf("缓存字段数 = %d, want 4", len(document))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("缓存权限 = %o, want 600", got)
	}
}

func TestFileCacheMissingCorruptVersionAndOversize(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	tests := []struct {
		name string
		data string
		want error
	}{
		{name: "缺失", want: ErrCacheMiss},
		{name: "空", data: "", want: ErrCacheCorrupt},
		{name: "损坏", data: "{", want: ErrCacheCorrupt},
		{name: "版本", data: `{"version":2,"updated_at":"2026-01-01T00:00:00Z","ipv4":["192.0.2.0/24"],"ipv6":["2001:db8::/32"]}`, want: ErrCacheCorrupt},
		{name: "超限", data: strings.Repeat("x", int(maxCacheBytes)+1), want: ErrCacheCorrupt},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(directory, test.name)
			if test.name != "缺失" {
				if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cache, err := NewFileCache(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = cache.Load(context.Background())
			if !errors.Is(err, test.want) {
				t.Fatalf("Load() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestFileCacheConcurrentSaveLoadIsWholeDocument(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ranges.json")
	cache, err := NewFileCache(path)
	if err != nil {
		t.Fatal(err)
	}
	first := validCached(time.Unix(1, 0).UTC())
	second := validCached(time.Unix(2, 0).UTC())
	if err := cache.Save(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for i := 0; i < 4; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			for j := 0; j < 100; j++ {
				value := first
				if (i+j)%2 == 0 {
					value = second
				}
				if err := cache.Save(context.Background(), value); err != nil {
					t.Errorf("Save() error = %v", err)
					return
				}
			}
		}(i)
	}
	for i := 0; i < 400; i++ {
		value, err := cache.Load(context.Background())
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if value.UpdatedAt != first.UpdatedAt && value.UpdatedAt != second.UpdatedAt {
			t.Fatalf("读取到不完整缓存: %#v", value)
		}
	}
	wait.Wait()
}

func newTestUpdater(fetcher Fetcher, cache RangeCache, store *SnapshotStore, now time.Time) *Updater {
	u, err := NewUpdater(fetcher, cache, store, UpdaterConfig{Refresh: time.Hour, Timeout: time.Second, Jitter: 5 * time.Minute, MaxStale: 24 * time.Hour}, testLogger())
	if err != nil {
		panic(err)
	}
	u.now = func() time.Time { return now }
	u.random = func() float64 { return 0.5 }
	return u
}

func TestUpdaterInitializeNetworkAndCacheFallback(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	fetcher := &stubFetcher{raw: validRaw()}
	cache := &memoryCache{}
	store := NewSnapshotStore(nil)
	u := newTestUpdater(fetcher, cache, store, now)
	source, err := u.Initialize(context.Background())
	if err != nil || source != SourceNetwork || store.Load() == nil || cache.saves != 1 {
		t.Fatalf("网络 Initialize() = source=%q err=%v saves=%d", source, err, cache.saves)
	}

	fetcher.err = errors.New("offline")
	store = NewSnapshotStore(nil)
	u = newTestUpdater(fetcher, cache, store, now)
	source, err = u.Initialize(context.Background())
	if err != nil || source != SourceCache || store.Load() == nil {
		t.Fatalf("缓存回退 Initialize() = source=%q err=%v", source, err)
	}
}

func TestUpdaterInitializeFailureKeepsStoreAndReportsReasons(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	lastKnownGood := mustRangeSet(t, "192.0.2.0/24", now)
	store := NewSnapshotStore(lastKnownGood)
	fetcher := &stubFetcher{err: errors.New("offline")}
	cache := &memoryCache{loadErr: ErrCacheCorrupt}
	u := newTestUpdater(fetcher, cache, store, now)
	_, err := u.Initialize(context.Background())
	if !errors.Is(err, ErrNetworkFailure) || !errors.Is(err, ErrCacheCorrupt) {
		t.Fatalf("Initialize() error = %v, want network+cache 原因", err)
	}
	if store.Load() != lastKnownGood {
		t.Fatal("失败初始化修改了 LKG")
	}
}

func TestUpdaterRefreshFailureAndSaveFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	lastKnownGood := mustRangeSet(t, "192.0.2.0/24", now)
	store := NewSnapshotStore(lastKnownGood)
	fetcher := &stubFetcher{raw: RawRanges{IPv4: "invalid", IPv6: validRaw().IPv6}}
	cache := &memoryCache{}
	u := newTestUpdater(fetcher, cache, store, now)
	if err := u.Refresh(context.Background()); err == nil || store.Load() != lastKnownGood || cache.saves != 0 {
		t.Fatalf("Build 失败 Refresh() = %v, store=%p saves=%d", err, store.Load(), cache.saves)
	}
	fetcher.raw = validRaw()
	cache.saveErr = errors.New("disk full")
	if err := u.Refresh(context.Background()); err != nil || store.Load() == lastKnownGood {
		t.Fatalf("Save 失败 Refresh() = %v, store=%p", err, store.Load())
	}
}

func TestUpdaterCacheExpiredInvalidAndJitter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	fetcher := &stubFetcher{err: errors.New("offline")}
	cache := &memoryCache{value: validCached(now.Add(-25 * time.Hour))}
	store := NewSnapshotStore(nil)
	u := newTestUpdater(fetcher, cache, store, now)
	_, err := u.Initialize(context.Background())
	if !errors.Is(err, ErrCacheExpired) || store.Load() != nil {
		t.Fatalf("过期缓存 Initialize() = %v, store=%p", err, store.Load())
	}

	cfg := UpdaterConfig{Refresh: time.Hour, Timeout: time.Second, Jitter: 10 * time.Minute, MaxStale: time.Hour}
	u, err = NewUpdater(&stubFetcher{raw: validRaw()}, &memoryCache{}, NewSnapshotStore(nil), cfg, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	for _, random := range []float64{0, 1} {
		u.random = func() float64 { return random }
		u.now = func() time.Time { return now }
		delay := u.refreshDelay()
		want := u.cfg.Refresh - u.cfg.Jitter
		if random == 1 {
			want = u.cfg.Refresh + u.cfg.Jitter
		}
		if delay != want {
			t.Fatalf("random=%v delay=%v want=%v", random, delay, want)
		}
	}
}

func TestCachedRangesConversion(t *testing.T) {
	t.Parallel()
	snapshot, err := NewRangeSet([]netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("2001:db8::/32")}, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	cached := CachedRangesFromRangeSet(snapshot)
	if cached.Version != 1 || len(cached.IPv4) != 1 || len(cached.IPv6) != 1 {
		t.Fatalf("CachedRangesFromRangeSet() = %#v", cached)
	}
}

func TestUpdaterInitializeInvalidCachedCIDRDoesNotPublish(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	fetcher := &stubFetcher{err: errors.New("offline")}
	cache := &memoryCache{value: CachedRanges{Version: 1, UpdatedAt: now.Format(time.RFC3339), IPv4: []string{"not-a-cidr"}, IPv6: []string{"2001:db8::/32"}}}
	store := NewSnapshotStore(nil)
	u := newTestUpdater(fetcher, cache, store, now)
	_, err := u.Initialize(context.Background())
	if !errors.Is(err, ErrCacheCorrupt) || store.Load() != nil {
		t.Fatalf("非法缓存 Initialize() = err=%v store=%p", err, store.Load())
	}
}

func TestUpdaterCacheAtMaxStaleIsUsableWithoutSave(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	fetcher := &stubFetcher{err: errors.New("offline")}
	cache := &memoryCache{value: validCached(now.Add(-24 * time.Hour))}
	store := NewSnapshotStore(nil)
	u := newTestUpdater(fetcher, cache, store, now)
	source, err := u.Initialize(context.Background())
	if err != nil || source != SourceCache || store.Load() == nil || cache.saves != 0 {
		t.Fatalf("等号 MaxStale Initialize() = source=%q err=%v saves=%d", source, err, cache.saves)
	}
}

func TestUpdaterRunStopsAfterCancelAndPublishesMultipleUpdates(t *testing.T) {
	saves := make(chan struct{}, 8)
	fetcher := &stubFetcher{raw: validRaw()}
	cache := &memoryCache{saved: saves}
	store := NewSnapshotStore(nil)
	u, err := NewUpdater(fetcher, cache, store, UpdaterConfig{Refresh: 5 * time.Millisecond, Timeout: time.Second, MaxStale: time.Hour}, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	u.random = func() float64 { return 0.5 }
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- u.Run(ctx) }()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for i := 0; i < 2; i++ {
		select {
		case <-saves:
		case <-deadline.C:
			t.Fatal("Run 未在期限内完成两次快照更新")
		}
	}
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-deadline.C:
		t.Fatal("Run 未在取消后退出")
	}
	if store.Load() == nil {
		t.Fatal("Run 未发布快照")
	}
	callsAfterCancel := fetcher.callCount()
	timer := time.NewTimer(20 * time.Millisecond)
	<-timer.C
	if got := fetcher.callCount(); got != callsAfterCancel {
		t.Fatalf("取消后抓取次数从 %d 增长到 %d", callsAfterCancel, got)
	}
}

func TestUpdaterRunConcurrentStoreLoads(t *testing.T) {
	fetcher := &stubFetcher{raw: validRaw()}
	store := NewSnapshotStore(nil)
	u, err := NewUpdater(fetcher, &memoryCache{}, store, UpdaterConfig{Refresh: time.Millisecond, Timeout: time.Second, MaxStale: time.Hour}, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	u.random = func() float64 { return 0.5 }
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- u.Run(ctx) }()
	var loads sync.WaitGroup
	for i := 0; i < 8; i++ {
		loads.Add(1)
		go func() {
			defer loads.Done()
			deadline := time.Now().Add(100 * time.Millisecond)
			for time.Now().Before(deadline) {
				_ = store.Load()
			}
		}()
	}
	loads.Wait()
	cancel()
	if err := <-runDone; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

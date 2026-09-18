package cloudflare

import (
	"net/netip"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestSnapshotStoreEmptyAndPublish(t *testing.T) {
	t.Parallel()
	store := NewSnapshotStore(nil)
	if got := store.Load(); got != nil {
		t.Fatalf("空存储 Load() = %v, want nil", got)
	}

	snapshot := mustRangeSet(t, "192.0.2.0/24", time.Unix(1, 0))
	if published := store.Publish(snapshot); !published {
		t.Fatal("Publish() = false, want true")
	}
	if got := store.Load(); got != snapshot {
		t.Fatalf("Load() = %p, want %p", got, snapshot)
	}
	if published := store.Publish(nil); published {
		t.Fatal("Publish(nil) = true, want false")
	}
	if got := store.Load(); got != snapshot {
		t.Fatalf("拒绝 nil 后 Load() = %p, want %p", got, snapshot)
	}
}

func TestSnapshotStoreConcurrentAtomicVisibility(t *testing.T) {
	first := mustRangeSet(t, "192.0.2.0/24", time.Unix(1, 0))
	second := mustRangeSet(t, "2001:db8::/32", time.Unix(2, 0))
	store := NewSnapshotStore(first)

	const readers = 16
	const writes = 10_000
	start := make(chan struct{})
	done := make(chan struct{})
	errors := make(chan string, readers)
	var waitGroup sync.WaitGroup

	for range readers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			for {
				select {
				case <-done:
					return
				default:
				}
				snapshot := store.Load()
				if snapshot != first && snapshot != second {
					errors <- "读取到非完整快照"
					return
				}
				if snapshot == first && (snapshot.IPv4Count() != 1 || snapshot.IPv6Count() != 0) {
					errors <- "IPv4 快照状态不一致"
					return
				}
				if snapshot == second && (snapshot.IPv4Count() != 0 || snapshot.IPv6Count() != 1) {
					errors <- "IPv6 快照状态不一致"
					return
				}
				runtime.Gosched()
			}
		}()
	}

	close(start)
	for i := range writes {
		if i%2 == 0 {
			store.Publish(second)
		} else {
			store.Publish(first)
		}
	}
	close(done)
	waitGroup.Wait()
	close(errors)
	for message := range errors {
		t.Error(message)
	}
}

func mustRangeSet(t *testing.T, prefix string, updatedAt time.Time) *RangeSet {
	t.Helper()
	rangeSet, err := NewRangeSet([]netip.Prefix{netip.MustParsePrefix(prefix)}, updatedAt)
	if err != nil {
		t.Fatal(err)
	}
	return rangeSet
}

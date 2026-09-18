package cloudflare

import "sync/atomic"

// SnapshotStore 保存当前不可变前缀快照。
type SnapshotStore struct {
	current atomic.Pointer[RangeSet]
}

// NewSnapshotStore 创建存储，并可选发布初始快照。
func NewSnapshotStore(initial *RangeSet) *SnapshotStore {
	store := &SnapshotStore{}
	store.Publish(initial)
	return store
}

// Publish 原子替换整个快照；nil 快照会被拒绝并保留当前值。
func (s *SnapshotStore) Publish(snapshot *RangeSet) bool {
	if snapshot == nil {
		return false
	}
	s.current.Store(snapshot)
	return true
}

// Load 无锁读取当前快照；尚未发布时返回 nil。
func (s *SnapshotStore) Load() *RangeSet {
	return s.current.Load()
}

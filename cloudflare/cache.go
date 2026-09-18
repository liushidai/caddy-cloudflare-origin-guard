package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	cacheVersion  = 1
	maxCacheBytes = int64(64 << 10)
	cacheFilePerm = 0o600
)

var (
	ErrCacheMiss    = errors.New("缓存不存在")
	ErrCacheCorrupt = errors.New("缓存损坏")
)

// CachedRanges 是缓存文件的可信结构前的 JSON 数据。
type CachedRanges struct {
	Version   int      `json:"version"`
	UpdatedAt string   `json:"updated_at"`
	IPv4      []string `json:"ipv4"`
	IPv6      []string `json:"ipv6"`
}

// RangeCache 提供缓存读写能力。
type RangeCache interface {
	Load(context.Context) (CachedRanges, error)
	Save(context.Context, CachedRanges) error
}

// FileCache 使用单个文件保存规范化范围。
type FileCache struct {
	path string
}

// NewFileCache 创建文件缓存。
func NewFileCache(path string) (*FileCache, error) {
	if path == "" {
		return nil, ErrInvalidCachePath
	}
	return &FileCache{path: path}, nil
}

// Load 读取并初步校验不可信缓存内容。
func (c *FileCache) Load(ctx context.Context) (CachedRanges, error) {
	if err := ctx.Err(); err != nil {
		return CachedRanges{}, err
	}
	file, err := os.Open(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return CachedRanges{}, ErrCacheMiss
	}
	if err != nil {
		return CachedRanges{}, fmt.Errorf("打开缓存: %w", err)
	}
	defer file.Close()

	if err := ctx.Err(); err != nil {
		return CachedRanges{}, err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCacheBytes+1))
	if err != nil {
		return CachedRanges{}, fmt.Errorf("读取缓存: %w", err)
	}
	if int64(len(data)) > maxCacheBytes {
		return CachedRanges{}, fmt.Errorf("%w: 超过 64KiB", ErrCacheCorrupt)
	}
	var cached CachedRanges
	if err := json.Unmarshal(data, &cached); err != nil {
		return CachedRanges{}, fmt.Errorf("%w: JSON 无效: %v", ErrCacheCorrupt, err)
	}
	if cached.Version != cacheVersion || cached.UpdatedAt == "" || len(cached.IPv4) == 0 || len(cached.IPv6) == 0 {
		return CachedRanges{}, fmt.Errorf("%w: 字段无效", ErrCacheCorrupt)
	}
	updatedAt, err := time.Parse(time.RFC3339, cached.UpdatedAt)
	if err != nil || updatedAt.IsZero() {
		return CachedRanges{}, fmt.Errorf("%w: updated_at 无效", ErrCacheCorrupt)
	}
	return cached, nil
}

// Save 原子写入缓存文件。
func (c *FileCache) Save(ctx context.Context, cached CachedRanges) error {
	data, err := json.Marshal(cached)
	if err != nil {
		return fmt.Errorf("编码缓存: %w", err)
	}
	directory := filepath.Dir(c.path)
	if err := ctx.Err(); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".cloudflare-cache-*")
	if err != nil {
		return fmt.Errorf("创建缓存临时文件: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(cacheFilePerm); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("设置缓存权限: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("写入缓存: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭缓存临时文件: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, c.path); err != nil {
		return fmt.Errorf("替换缓存文件: %w", err)
	}
	return nil
}

// CachedRangesFromRangeSet 将验证过的快照转换为缓存结构。
func CachedRangesFromRangeSet(snapshot *RangeSet) CachedRanges {
	var cached CachedRanges
	if snapshot == nil {
		return cached
	}
	cached.Version = cacheVersion
	cached.UpdatedAt = snapshot.UpdatedAt().Format(time.RFC3339)
	for _, prefix := range snapshot.Prefixes() {
		if prefix.Addr().Is4() {
			cached.IPv4 = append(cached.IPv4, prefix.String())
		} else {
			cached.IPv6 = append(cached.IPv6, prefix.String())
		}
	}
	return cached
}

func rawRangesFromCached(cached CachedRanges) (RawRanges, time.Time, error) {
	updatedAt, err := time.Parse(time.RFC3339, cached.UpdatedAt)
	if err != nil || updatedAt.IsZero() {
		return RawRanges{}, time.Time{}, fmt.Errorf("%w: updated_at 无效", ErrCacheCorrupt)
	}
	return RawRanges{
		IPv4: joinCIDRs(cached.IPv4),
		IPv6: joinCIDRs(cached.IPv6),
	}, updatedAt, nil
}

func joinCIDRs(cidrs []string) string {
	return strings.Join(cidrs, "\n")
}

var ErrInvalidCachePath = errors.New("缓存路径不能为空")

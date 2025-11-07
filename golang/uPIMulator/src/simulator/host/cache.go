package host

import (
	"container/list"
	"strings"
	"sync"
)

// KVCacheOp 描述 KV cache 访问类型。
type KVCacheOp int

const (
	KVCacheOpUnknown KVCacheOp = iota
	KVCacheOpLoad
	KVCacheOpStore
)

// KVAccessInfo 携带一次访问所需的标识信息。
type KVAccessInfo struct {
	Layer    int
	Head     int
	Sequence int
	Token    int
	Batch    int
	Key      string
}

// KVAccessResult 记录一次访问的结果，用于统计。
type KVAccessResult struct {
	Op           KVCacheOp
	Layer        int
	Bytes        int64
	Hit          bool
	EvictedBytes int64
	Resident     int64
}

// kvEntry 是 KVCache 内部条目。
type kvEntry struct {
	key     string
	bytes   int64
	element *list.Element
}

// KVCounters 汇总统计。
type KVCounters struct {
	Loads      int64
	Stores     int64
	HitCount   int64
	MissCount  int64
	LoadBytes  int64
	StoreBytes int64
	HitBytes   int64
	MissBytes  int64
	Evicted    int64
	PeakBytes  int64
}

// KVCache 管理 Host 侧的 KV 缓存，使用 LRU。
type KVCache struct {
	capacity int64
	used     int64

	entries map[string]*kvEntry
	lru     *list.List

	stats KVCounters
	mu    sync.Mutex
}

// NewKVCache 创建 KV cache；若 capacity <= 0，则返回 nil。
func NewKVCache(capacity int64) *KVCache {
	if capacity <= 0 {
		return nil
	}
	return &KVCache{
		capacity: capacity,
		entries:  make(map[string]*kvEntry),
		lru:      list.New(),
	}
}

func (c *KVCache) Enabled() bool {
	return c != nil && c.capacity > 0
}

// CapacityBytes 返回总容量。
func (c *KVCache) CapacityBytes() int64 {
	if c == nil {
		return 0
	}
	return c.capacity
}

// ResidentBytes 当前占用。
func (c *KVCache) ResidentBytes() int64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

// Stats 返回累计统计快照。
func (c *KVCache) Stats() KVCounters {
	if c == nil {
		return KVCounters{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// Access 执行一次缓存访问，bytes 表示需要占用/读取的字节数。
func (c *KVCache) Access(op KVCacheOp, info KVAccessInfo, bytes int64) KVAccessResult {
	if c == nil || op == KVCacheOpUnknown || bytes <= 0 {
		return KVAccessResult{Op: op, Layer: info.Layer, Bytes: bytes}
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	key := buildKVKey(info)
	entry, exists := c.entries[key]

	result := KVAccessResult{
		Op:    op,
		Layer: info.Layer,
		Bytes: bytes,
		Hit:   exists,
	}

	if op == KVCacheOpLoad {
		c.stats.Loads++
		c.stats.LoadBytes += bytes
	} else if op == KVCacheOpStore {
		c.stats.Stores++
		c.stats.StoreBytes += bytes
	}

	if exists {
		c.moveToFront(entry)
		result.Resident = c.used
		c.stats.HitCount++
		c.stats.HitBytes += bytes
		if op == KVCacheOpStore && entry.bytes != bytes {
			// Store 更新条目大小。
			c.used -= entry.bytes
			entry.bytes = bytes
			c.used += entry.bytes
		}
		return result
	}

	// Miss：需要插入新条目。
	c.stats.MissCount++
	c.stats.MissBytes += bytes

	// 若条目过大，直接按 miss 处理，不缓存。
	if bytes > c.capacity {
		result.Hit = false
		result.Resident = c.used
		return result
	}

	result.Hit = false

	// 淘汰直至空间足够。
	for c.used+bytes > c.capacity && c.lru.Len() > 0 {
		back := c.lru.Back()
		if back == nil {
			break
		}
		evictEntry := back.Value.(*kvEntry)
		c.lru.Remove(back)
		delete(c.entries, evictEntry.key)
		c.used -= evictEntry.bytes
		result.EvictedBytes += evictEntry.bytes
		c.stats.Evicted += evictEntry.bytes
	}

	newElem := c.lru.PushFront(nil) // 占位
	newEntry := &kvEntry{
		key:     key,
		bytes:   bytes,
		element: newElem,
	}
	newElem.Value = newEntry
	c.entries[key] = newEntry
	c.used += bytes
	if c.used > c.stats.PeakBytes {
		c.stats.PeakBytes = c.used
	}
	result.Resident = c.used
	return result
}

func (c *KVCache) moveToFront(entry *kvEntry) {
	if entry == nil || entry.element == nil || c.lru == nil {
		return
	}
	c.lru.MoveToFront(entry.element)
}

func buildKVKey(info KVAccessInfo) string {
	if info.Key != "" {
		return info.Key
	}
	builder := strings.Builder{}
	builder.Grow(32)
	builder.WriteString("L")
	builder.WriteString(intToString(info.Layer))
	builder.WriteString(":H")
	builder.WriteString(intToString(info.Head))
	builder.WriteString(":S")
	builder.WriteString(intToString(info.Sequence))
	builder.WriteString(":T")
	builder.WriteString(intToString(info.Token))
	builder.WriteString(":B")
	builder.WriteString(intToString(info.Batch))
	return builder.String()
}

func intToString(v int) string {
	return strconvItoa(v)
}

// strconvItoa 封装 strconv.Itoa，避免跨文件重复导入。
func strconvItoa(v int) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	buf := make([]byte, 0, 12)
	for v > 0 {
		buf = append(buf, byte('0'+v%10))
		v /= 10
	}
	if negative {
		buf = append(buf, '-')
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

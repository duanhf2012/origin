// Package bufferpool 提供 Origin 框架内部使用的分档字节缓冲区池。
//
// 该包只负责 Buffer 的复用和所有权诊断，不负责网络背压、最大包长、
// 引用计数或业务对象池化。
package bufferpool

import (
	"math/bits"
	"sync"
	"sync/atomic"
)

const (
	// 最小和最大档位分别是 2^4=16 B 与 2^16=64 KiB。
	minBucketShift = 4
	maxBucketShift = 16
	bucketCount    = maxBucketShift - minBucketShift + 1

	// 非池化 Buffer 使用独立标记，避免与固定档位索引混淆。
	oversizeBucket = ^uint8(0)
	zeroSizeBucket = oversizeBucket - 1
)

// Options 配置一个 Buffer Pool。
//
// 零值配置关闭使用量统计，适合默认低延迟路径。
type Options struct {
	// TrackUsage 控制是否统计当前已经取得但尚未释放的 Buffer。
	// 该选项只在创建 Pool 时读取，运行期间不能动态切换。
	TrackUsage bool
}

// BucketStats 是一个固定容量档位的未归还统计快照。
type BucketStats struct {
	// Capacity 是该档位的 Buffer 容量，单位为字节。
	Capacity int
	// InUseBuffers 是取得后尚未释放的 Buffer 数量。
	InUseBuffers int64
}

// Stats 是 Pool 在某一时刻的未归还统计快照。
//
// 并发使用 Pool 时，各档位读数可能来自相邻时刻；停止取得和释放后，
// 该快照可以用于判断 Buffer 生命周期是否已经配平。
type Stats struct {
	// Enabled 表示创建 Pool 时是否开启了使用量统计。
	Enabled bool
	// InUseBuffers 是全部尚未释放的 Buffer 数量。
	InUseBuffers int64
	// InUseCapacityBytes 是全部尚未释放 Buffer 的容量之和。
	InUseCapacityBytes int64
	// ZeroSizeInUse 是尚未释放的零长度 Buffer 数量。
	ZeroSizeInUse int64
	// OversizeInUse 是容量超过 64 KiB 且尚未释放的 Buffer 数量。
	OversizeInUse int64
	// OversizeBytes 是尚未释放的超大 Buffer 容量之和。
	OversizeBytes int64
	// Buckets 保存全部固定容量档位的快照。
	Buckets []BucketStats
}

// Pool 管理多个固定容量档位的 Buffer。
//
// Pool 可以由多个 goroutine 并发使用，但开始使用后不能复制。
type Pool struct {
	buckets [bucketCount]bucket

	// trackUsage 只在构造时写入，热路径读取时不需要同步。
	trackUsage bool

	zeroSizeInUse atomic.Int64
	oversizeInUse atomic.Int64
	oversizeBytes atomic.Int64
}

// bucket 使用独立的 sync.Pool 复用同一容量的 Buffer。
type bucket struct {
	pool  sync.Pool
	inUse atomic.Int64
}

// NewPool 创建一个相互独立的 Buffer Pool。
func NewPool(options Options) *Pool {
	// Pool 没有后台资源；构造阶段只固化是否启用热路径统计。
	return &Pool{trackUsage: options.TrackUsage}
}

// Acquire 取得一个有效长度为 size 的 Buffer。
//
// size 为负数表示框架内部违反不变量，因此直接 panic。大于 64 KiB 的
// Buffer 按实际大小分配，并在释放时交还给 Go 垃圾回收器。
func (p *Pool) Acquire(size int) *Buffer {
	// 普通取得没有前置空间，复用统一实现避免两条档位和统计路径逐渐分叉。
	return p.AcquireWithHeadroom(size, 0)
}

// AcquireWithHeadroom 取得一个有效长度为 size、前方至少保留 headroom 字节的 Buffer。
//
// headroom 不属于初始 Bytes 视图，只能通过 Buffer.Prepend 显式启用。两者之和超过 64 KiB
// 时按真实总长度分配，释放时不进入固定档位池。
func (p *Pool) AcquireWithHeadroom(size, headroom int) *Buffer {
	// 首先拒绝违反 API 不变量的 Pool 和长度，避免整数溢出后产生错误切片。
	if p == nil {
		panic("bufferpool: 从 nil Pool 取得 Buffer")
	}
	if size < 0 || headroom < 0 {
		panic("bufferpool: Buffer 长度和 headroom 不能为负数")
	}
	if headroom > int(^uint(0)>>1)-size {
		panic("bufferpool: Buffer 长度和 headroom 之和溢出")
	}
	total := size + headroom

	// 只有完全没有可见数据和前置空间的对象才使用零长度特殊路径。
	if total == 0 {
		buf := &Buffer{
			owner:  p,
			bucket: zeroSizeBucket,
			active: true,
		}
		if p.trackUsage {
			// 统计开启时记录唯一所有权已经交给调用方。
			p.zeroSizeInUse.Add(1)
		}
		return buf
	}
	// 超出最大档位的请求按实际总长度分配，避免池长期滞留大对象。
	if total > maxPooledCapacity() {
		buf := &Buffer{
			data:   make([]byte, total),
			owner:  p,
			start:  headroom,
			size:   size,
			bucket: oversizeBucket,
			active: true,
		}
		if p.trackUsage {
			// 同时记录数量和真实容量，便于排查超大 Buffer 未归还。
			p.oversizeInUse.Add(1)
			p.oversizeBytes.Add(int64(cap(buf.data)))
		}
		return buf
	}

	// 池化请求按“headroom + 有效数据”选择能够容纳完整视图的最小 2 次幂档位。
	index := bucketIndex(total)
	// 优先复用该档位对象；sync.Pool 为空时才分配新的底层数组。
	item := p.buckets[index].pool.Get()
	var buf *Buffer
	if item == nil {
		buf = &Buffer{data: make([]byte, bucketCapacity(index))}
	} else {
		buf = item.(*Buffer)
	}

	// 每个 sync.Pool 只允许保存其固定档位的对象。该检查失败说明内部
	// 释放路径污染了池，继续运行可能把错误容量交给网络层。
	if cap(buf.data) != bucketCapacity(index) {
		panic("bufferpool: Buffer 容量档位被污染")
	}

	// 每次取得都重新建立所有权、有效长度和档位，覆盖上一任使用者状态。
	buf.owner = p
	buf.start = headroom
	buf.size = size
	buf.bucket = uint8(index)
	buf.active = true
	if p.trackUsage {
		// 最后增加使用量，保证统计对应已经完成初始化并返回的对象。
		p.buckets[index].inUse.Add(1)
	}
	return buf
}

// Stats 返回当前未归还 Buffer 的统计快照。
//
// 未开启 TrackUsage 时返回 Enabled=false 的零值快照，不推算任何数据。
func (p *Pool) Stats() Stats {
	// nil Pool 或关闭统计时不做原子读取和切片分配。
	if p == nil || !p.trackUsage {
		return Stats{}
	}

	// 先建立固定档位快照，再逐档汇总数量与容量。
	stats := Stats{
		Enabled: true,
		Buckets: make([]BucketStats, bucketCount),
	}
	for index := range p.buckets {
		count := p.buckets[index].inUse.Load()
		capacity := bucketCapacity(index)
		stats.Buckets[index] = BucketStats{
			Capacity:     capacity,
			InUseBuffers: count,
		}
		stats.InUseBuffers += count
		stats.InUseCapacityBytes += count * int64(capacity)
	}

	// 零长度和超大对象不属于 buckets，需要单独读取并加入总数。
	stats.ZeroSizeInUse = p.zeroSizeInUse.Load()
	stats.OversizeInUse = p.oversizeInUse.Load()
	stats.OversizeBytes = p.oversizeBytes.Load()
	stats.InUseBuffers += stats.ZeroSizeInUse + stats.OversizeInUse
	stats.InUseCapacityBytes += stats.OversizeBytes
	return stats
}

// releaseUsage 在首次有效释放时扣减对应档位的未归还统计。
func (p *Pool) releaseUsage(bucketID uint8, capacity int) {
	// 默认关闭统计时 Release 不执行任何原子操作。
	if !p.trackUsage {
		return
	}

	// 依据取得时固化的档位标记，精确扣减对应计数器。
	switch bucketID {
	case zeroSizeBucket:
		p.zeroSizeInUse.Add(-1)
	case oversizeBucket:
		p.oversizeInUse.Add(-1)
		p.oversizeBytes.Add(-int64(capacity))
	default:
		p.buckets[int(bucketID)].inUse.Add(-1)
	}
}

// bucketIndex 返回能够容纳 size 的最小固定档位索引。
func bucketIndex(size int) int {
	// 16 B 以内全部使用最小档位。
	if size <= 1<<minBucketShift {
		return 0
	}
	// size-1 的位宽可把恰好为 2 次幂的长度留在当前档位。
	return bits.Len(uint(size-1)) - minBucketShift
}

// bucketCapacity 返回固定档位对应的字节容量。
func bucketCapacity(index int) int {
	// 档位从 2^minBucketShift 开始连续递增。
	return 1 << (minBucketShift + index)
}

// maxPooledCapacity 返回最大的可池化 Buffer 容量。
func maxPooledCapacity() int {
	// 最大档位保持为单一来源，避免 Acquire 边界与档位定义分离。
	return 1 << maxBucketShift
}

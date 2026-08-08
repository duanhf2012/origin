package timerwheel

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// engineState 是 Engine 的一次性生命周期状态。
type engineState uint8

const (
	engineCreated engineState = iota
	engineRunning
	engineClosed
)

// Stats 是 Engine 某一时刻的只读统计快照。
//
// 并发调用时全部字段来自同一 Engine 锁内时刻，因此字段之间可以直接比较。
type Stats struct {
	// Running 表示工作 goroutine 当前仍接受和处理 Deadline。
	Running bool
	// Closed 表示 Engine 已经进入不可逆的关闭状态。
	Closed bool

	// Scheduled 是当前仍在时间轮中的 Deadline 数。
	Scheduled uint64
	// Expired 是已经到期但尚未被 Queue 消费的 ID 数。
	Expired uint64
	// Queues 是当前尚未关闭的 DeadlineQueue 数。
	Queues uint64
	// ScheduledPeak 和 ExpiredPeak 是当前 Engine 生命周期内的历史峰值。
	ScheduledPeak uint64
	ExpiredPeak   uint64

	// ScheduledTotal、CanceledTotal、ExpiredTotal 和 CleanedTotal 是累计生命周期计数。
	ScheduledTotal uint64
	CanceledTotal  uint64
	ExpiredTotal   uint64
	CleanedTotal   uint64

	// WakeupsTotal 统计 Timer 和结构变更实际唤醒工作 goroutine 的总次数。
	WakeupsTotal uint64
	// TimerWakeups 统计底层 Timer 到期唤醒次数。
	TimerWakeups uint64
	// ChangeWakeups 统计登记、取消或关闭导致的结构变更唤醒次数。
	ChangeWakeups uint64
	// EmptyWakeups 统计 Timer 唤醒后没有到期或级联工作的次数。
	EmptyWakeups uint64

	// Cascades 和 CascadedEntries 分别统计非空桶级联次数与迁移条目数。
	Cascades        uint64
	CascadedEntries uint64
	// LevelEntries 保存每层当前条目数。
	LevelEntries [LevelCount]uint64

	// LastExpiryDelay 和 MaxExpiryDelay 记录最近一次与历史最大到期延迟。
	LastExpiryDelay time.Duration
	MaxExpiryDelay  time.Duration
	// StopDuration 记录首次 Close 等待工作 goroutine 完成的耗时。
	StopDuration time.Duration

	// EntryAllocations、EntryReuses 和 EntryReleases 只在 TrackEntryPool=true 时统计。
	EntryAllocations uint64
	EntryReuses      uint64
	EntryReleases    uint64
}

// engineStats 是只在 Engine 锁内读写的统计状态。
type engineStats struct {
	scheduled uint64
	expired   uint64
	queues    uint64

	scheduledPeak uint64
	expiredPeak   uint64

	scheduledTotal uint64
	canceledTotal  uint64
	expiredTotal   uint64
	cleanedTotal   uint64

	wakeupsTotal  uint64
	timerWakeups  uint64
	changeWakeups uint64
	emptyWakeups  uint64

	cascades        uint64
	cascadedEntries uint64
	levelEntries    [LevelCount]uint64

	lastExpiryDelay time.Duration
	maxExpiryDelay  time.Duration
	stopDuration    time.Duration

	entryAllocations uint64
	entryReuses      uint64
	entryReleases    uint64
}

// Engine 管理一个 Node 独占的五层 Deadline 时间轮。
//
// Engine 是一次性对象。Start 只能成功一次；Close 幂等并等待唯一工作 goroutine 退出。
type Engine struct {
	mu sync.Mutex

	state          engineState
	clock          Clock
	wakeSource     WakeSource
	trackEntryPool bool
	startTime      time.Time
	currentTick    uint64
	nextID         DeadlineID

	wheel   timingWheel
	entries map[DeadlineID]*timerEntry
	queues  map[*DeadlineQueue]struct{}

	entryPool sync.Pool
	stats     engineStats

	changeSignal chan struct{}
	stopSignal   chan struct{}
	done         chan struct{}
}

// New 校验依赖并创建尚未启动的独立 Engine。
func New(options Options) (*Engine, error) {
	// Clock 和 WakeSource 都必须显式归属于当前 Engine，禁止隐藏的包级默认实例。
	if options.Clock == nil {
		return nil, invalidArgument("timerwheel Clock 不能为空")
	}
	if options.WakeSource == nil {
		return nil, invalidArgument("timerwheel WakeSource 不能为空")
	}

	// 固定结构和索引在冷路径一次建立；条目对象仍按真实负载渐进分配。
	engine := &Engine{
		state:          engineCreated,
		clock:          options.Clock,
		wakeSource:     options.WakeSource,
		trackEntryPool: options.TrackEntryPool,
		nextID:         1,
		entries:        make(map[DeadlineID]*timerEntry),
		queues:         make(map[*DeadlineQueue]struct{}),
		changeSignal:   make(chan struct{}, 1),
		stopSignal:     make(chan struct{}),
		done:           make(chan struct{}),
	}
	return engine, nil
}

// Now 返回当前 Engine 所使用 Clock 的时间。
//
// 上层组件必须通过该方法读取时间轮的统一时间源，不能混用 time.Now；否则测试时钟、
// 到期时间和延迟统计会处于不同时间轴。Clock 的实现必须遵守 Options 中的并发安全契约。
func (engine *Engine) Now() time.Time {
	if engine == nil || engine.clock == nil {
		return time.Time{}
	}
	return engine.clock.Now()
}

// Start 记录单调基准并启动唯一工作 goroutine。
func (engine *Engine) Start() error {
	if engine == nil {
		return invalidArgument("timerwheel Engine 不能为空")
	}

	// 状态检查和基准时间发布必须先于 goroutine 创建，避免工作循环看到半初始化字段。
	engine.mu.Lock()
	if engine.state != engineCreated {
		state := engine.state
		engine.mu.Unlock()
		return invalidArgument(fmt.Sprintf("timerwheel Engine 不能从状态 %d 启动", state))
	}
	engine.startTime = engine.clock.Now()
	engine.currentTick = 0
	engine.state = engineRunning
	engine.mu.Unlock()

	go engine.run()
	return nil
}

// NewDeadlineQueue 创建由当前运行 Engine 独占管理的到期队列。
func (engine *Engine) NewDeadlineQueue() (*DeadlineQueue, error) {
	if engine == nil {
		return nil, invalidArgument("timerwheel Engine 不能为空")
	}

	// Queue 必须在 Engine 运行后创建，保证登记后一定存在消费其 Deadline 的工作循环。
	engine.mu.Lock()
	defer engine.mu.Unlock()
	switch engine.state {
	case engineClosed:
		return nil, ErrEngineClosed
	case engineRunning:
		// 继续创建。
	default:
		return nil, invalidArgument("timerwheel Engine 尚未启动")
	}
	queue := &DeadlineQueue{
		engine: engine,
		signal: make(chan struct{}, 1),
	}
	engine.queues[queue] = struct{}{}
	engine.stats.queues++
	return queue, nil
}

// Stats 返回同一锁内时刻的完整统计快照。
func (engine *Engine) Stats() Stats {
	if engine == nil {
		return Stats{Closed: true}
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	return Stats{
		Running: engine.state == engineRunning,
		Closed:  engine.state == engineClosed,

		Scheduled:     engine.stats.scheduled,
		Expired:       engine.stats.expired,
		Queues:        engine.stats.queues,
		ScheduledPeak: engine.stats.scheduledPeak,
		ExpiredPeak:   engine.stats.expiredPeak,

		ScheduledTotal: engine.stats.scheduledTotal,
		CanceledTotal:  engine.stats.canceledTotal,
		ExpiredTotal:   engine.stats.expiredTotal,
		CleanedTotal:   engine.stats.cleanedTotal,

		WakeupsTotal:  engine.stats.wakeupsTotal,
		TimerWakeups:  engine.stats.timerWakeups,
		ChangeWakeups: engine.stats.changeWakeups,
		EmptyWakeups:  engine.stats.emptyWakeups,

		Cascades:        engine.stats.cascades,
		CascadedEntries: engine.stats.cascadedEntries,
		LevelEntries:    engine.stats.levelEntries,

		LastExpiryDelay: engine.stats.lastExpiryDelay,
		MaxExpiryDelay:  engine.stats.maxExpiryDelay,
		StopDuration:    engine.stats.stopDuration,

		EntryAllocations: engine.stats.entryAllocations,
		EntryReuses:      engine.stats.entryReuses,
		EntryReleases:    engine.stats.entryReleases,
	}
}

// Close 幂等关闭 Engine、清理全部 Queue 并等待工作 goroutine 退出。
func (engine *Engine) Close() error {
	if engine == nil {
		return nil
	}
	startedAt := time.Now()

	// 第一位关闭者取得状态转换权；后续调用只等待同一个 done。
	engine.mu.Lock()
	if engine.state == engineClosed {
		done := engine.done
		engine.mu.Unlock()
		<-done
		return nil
	}
	wasRunning := engine.state == engineRunning
	engine.state = engineClosed

	// 在同一锁内关闭全部 Queue，确保到期、取消和关闭不可能重复清理条目。
	for queue := range engine.queues {
		engine.closeQueueLocked(queue)
	}
	if wasRunning {
		close(engine.stopSignal)
	} else {
		// 尚未 Start 的 Engine 没有工作 goroutine，当前路径负责停止唤醒源并关闭 done。
		engine.wakeSource.Stop()
		close(engine.done)
	}
	done := engine.done
	engine.mu.Unlock()

	// 等待工作 goroutine 完全退出后再发布停止耗时，保证资源所有权已经回收。
	<-done
	engine.mu.Lock()
	if engine.stats.stopDuration == 0 {
		engine.stats.stopDuration = time.Since(startedAt)
	}
	// Engine 是一次性 Node 资源。关闭后主动断开高水位索引和池引用，避免百万级
	// Deadline 曾经增长出的 Map 桶及池对象跟随已停止 Node 长期保留。
	engine.entries = nil
	engine.queues = nil
	engine.entryPool = sync.Pool{}
	engine.mu.Unlock()
	return nil
}

// scheduleAfter 完成 Queue 所有权校验、Tick 计算和 O(1) 登记。
func (engine *Engine) scheduleAfter(
	queue *DeadlineQueue,
	delay time.Duration,
) (DeadlineID, error) {
	// 负延迟没有明确语义，必须在进入锁前直接拒绝。
	if delay < 0 {
		return InvalidDeadlineID, invalidArgument("Deadline 延迟不能为负数")
	}

	engine.mu.Lock()
	if engine.state == engineClosed {
		engine.mu.Unlock()
		return InvalidDeadlineID, ErrEngineClosed
	}
	if engine.state != engineRunning {
		engine.mu.Unlock()
		return InvalidDeadlineID, invalidArgument("timerwheel Engine 尚未启动")
	}
	if queue.closed {
		engine.mu.Unlock()
		return InvalidDeadlineID, ErrDeadlineQueueClosed
	}
	if _, exists := engine.queues[queue]; !exists {
		engine.mu.Unlock()
		return InvalidDeadlineID, invalidArgument("DeadlineQueue 不属于当前 Engine")
	}

	deadlineTick, err := engine.deadlineTickAfterLocked(delay)
	if err != nil {
		engine.mu.Unlock()
		return InvalidDeadlineID, err
	}

	// ID 为零表示已经耗尽 uint64 空间；旧 ID 永远不能绕回并指向新条目。
	id := engine.nextID
	if id == InvalidDeadlineID {
		engine.mu.Unlock()
		return InvalidDeadlineID, internalError("DeadlineID 已耗尽")
	}
	engine.nextID++

	// 从私有对象池取得条目并完整初始化，再同时加入 Queue 链表、时间轮和 ID 索引。
	entry := engine.acquireEntryLocked()
	entry.id = id
	entry.deadlineTick = deadlineTick
	entry.queue = queue
	entry.state = entryScheduled
	queue.addScheduledLocked(entry)
	level := engine.wheel.insertLocked(entry, engine.currentTick)
	engine.stats.levelEntries[level]++
	engine.entries[id] = entry

	// 最后更新计数，确保统计只观察已经完整可取消的 Scheduled 条目。
	engine.stats.scheduled++
	engine.stats.scheduledTotal++
	if engine.stats.scheduled > engine.stats.scheduledPeak {
		engine.stats.scheduledPeak = engine.stats.scheduled
	}
	engine.mu.Unlock()

	// 新 Deadline 可能早于当前睡眠目标，使用容量 1 Channel 合并结构变更。
	engine.notifyChange()
	return id, nil
}

// rescheduleAfter 在 Engine 单锁内原地移动已登记条目，不换 ID、不更换 Map Key、不经过对象池。
func (engine *Engine) rescheduleAfter(
	queue *DeadlineQueue,
	id DeadlineID,
	delay time.Duration,
) (bool, error) {
	if id == InvalidDeadlineID {
		return false, invalidArgument("DeadlineID 不能为零")
	}
	if delay < 0 {
		return false, invalidArgument("Deadline 延迟不能为负数")
	}

	engine.mu.Lock()
	if engine.state == engineClosed {
		engine.mu.Unlock()
		return false, ErrEngineClosed
	}
	if engine.state != engineRunning {
		engine.mu.Unlock()
		return false, invalidArgument("timerwheel Engine 尚未启动")
	}
	if queue.closed {
		engine.mu.Unlock()
		return false, ErrDeadlineQueueClosed
	}
	if _, exists := engine.queues[queue]; !exists {
		engine.mu.Unlock()
		return false, invalidArgument("DeadlineQueue 不属于当前 Engine")
	}
	entry, exists := engine.entries[id]
	if !exists || entry.queue != queue || entry.state != entryScheduled {
		engine.mu.Unlock()
		return false, nil
	}
	deadlineTick, err := engine.deadlineTickAfterLocked(delay)
	if err != nil {
		engine.mu.Unlock()
		return false, err
	}

	oldLevel := engine.wheel.removeLocked(entry)
	engine.stats.levelEntries[oldLevel]--
	entry.deadlineTick = deadlineTick
	newLevel := engine.wheel.insertLocked(entry, engine.currentTick)
	engine.stats.levelEntries[newLevel]++
	engine.mu.Unlock()

	// 新位置可能早于当前底层休眠目标，也可能移走原最早点；统一要求工作协程重算。
	engine.notifyChange()
	return true, nil
}

// deadlineTickAfterLocked 使用 Engine 基准的单调相对时间计算目标 Tick。
// 调用方必须持有 engine.mu，并先验证 delay 非负。
func (engine *Engine) deadlineTickAfterLocked(delay time.Duration) (uint64, error) {
	elapsed := engine.clock.Now().Sub(engine.startTime)
	if elapsed < 0 {
		elapsed = 0
	}
	if delay > 0 && elapsed > time.Duration(math.MaxInt64)-delay {
		return 0, internalError("Deadline 相对时间溢出")
	}
	deadlineElapsed := elapsed + delay
	deadlineTick := ceilTick(deadlineElapsed)
	if delay == 0 {
		// 零延迟只能在后续工作轮次到期，不能由登记调用栈同步交付。
		deadlineTick = floorTick(elapsed) + 1
	}
	if deadlineTick <= engine.currentTick {
		if engine.currentTick == math.MaxUint64 {
			return 0, internalError("Deadline Tick 已耗尽")
		}
		deadlineTick = engine.currentTick + 1
	}
	return deadlineTick, nil
}

// cancel 在 Engine 单锁内裁决取消与到期竞争。
func (engine *Engine) cancel(queue *DeadlineQueue, id DeadlineID) bool {
	engine.mu.Lock()
	if engine.state != engineRunning || queue.closed {
		engine.mu.Unlock()
		return false
	}
	entry, exists := engine.entries[id]
	if !exists || entry.queue != queue || entry.state != entryScheduled {
		engine.mu.Unlock()
		return false
	}

	// 条目先从全部可达索引移除，再清零回池，旧 ID 不再能访问复用对象。
	engine.removeScheduledEntryLocked(entry)
	engine.stats.canceledTotal++
	engine.releaseEntryLocked(entry)
	engine.mu.Unlock()

	// 被取消条目可能是最早唤醒点，通知工作 goroutine 重新计算。
	engine.notifyChange()
	return true
}

// closeQueueLocked 清理一个 Queue；调用方必须持有 Engine 锁。
func (engine *Engine) closeQueueLocked(queue *DeadlineQueue) bool {
	if queue == nil || queue.closed {
		return false
	}
	queue.closed = true

	// 通过 Queue 私有 Scheduled 链表清理，不扫描其他 Queue 或全局 ID Map。
	cleanedScheduled := uint64(0)
	for queue.scheduledHead != nil {
		entry := queue.scheduledHead
		engine.removeScheduledEntryLocked(entry)
		engine.releaseEntryLocked(entry)
		cleanedScheduled++
	}

	// 已到期 ID 不再引用 timerEntry，只需清空紧凑环形队列并扣减当前 Expired。
	cleanedExpired := uint64(queue.expired.Clear())
	engine.stats.expired -= cleanedExpired
	engine.stats.cleanedTotal += cleanedScheduled + cleanedExpired
	delete(engine.queues, queue)
	engine.stats.queues--

	// Engine 锁保证不会再向该 Channel 发送。先清掉可能存在的合并信号，
	// 再关闭 Channel，使等待方观察到关闭后不会误把旧通知当成新到期事件。
	select {
	case <-queue.signal:
	default:
	}
	close(queue.signal)
	return cleanedScheduled > 0
}

// removeScheduledEntryLocked 从时间轮、Queue 链表和 ID 索引移除条目。
func (engine *Engine) removeScheduledEntryLocked(entry *timerEntry) {
	level := engine.wheel.removeLocked(entry)
	engine.stats.levelEntries[level]--
	entry.queue.removeScheduledLocked(entry)
	delete(engine.entries, entry.id)
	engine.stats.scheduled--
}

// acquireEntryLocked 从 Engine 私有 sync.Pool 取得并验证一个已清零条目。
func (engine *Engine) acquireEntryLocked() *timerEntry {
	item := engine.entryPool.Get()
	if item == nil {
		if engine.trackEntryPool {
			engine.stats.entryAllocations++
		}
		return &timerEntry{}
	}
	entry := item.(*timerEntry)
	if entry.state != entryFree ||
		entry.queue != nil ||
		entry.wheelPrev != nil ||
		entry.wheelNext != nil ||
		entry.queuePrev != nil ||
		entry.queueNext != nil {
		panic("timerwheel: timerEntry 对象池包含未清零条目")
	}
	if engine.trackEntryPool {
		engine.stats.entryReuses++
	}
	return entry
}

// releaseEntryLocked 完整清零并把不再可达的条目归还私有 sync.Pool。
func (engine *Engine) releaseEntryLocked(entry *timerEntry) {
	if entry == nil || entry.state != entryScheduled {
		panic("timerwheel: timerEntry 重复或非法回收")
	}
	*entry = timerEntry{}
	if engine.trackEntryPool {
		engine.stats.entryReleases++
	}
	engine.entryPool.Put(entry)
}

// notifyChange 非阻塞合并时间轮结构变更。
func (engine *Engine) notifyChange() {
	select {
	case engine.changeSignal <- struct{}{}:
	default:
	}
}

// run 驱动事件到期、级联和下一唤醒点计算。
func (engine *Engine) run() {
	defer close(engine.done)
	defer engine.wakeSource.Stop()

	timerWake := false
	for {
		// 每轮先按当前单调时间大步推进，再决定是否需要真实等待。
		engine.mu.Lock()
		if engine.state != engineRunning {
			engine.mu.Unlock()
			return
		}
		now := engine.clock.Now()
		elapsed := now.Sub(engine.startTime)
		if elapsed < 0 {
			elapsed = 0
		}
		work := engine.advanceLocked(floorTick(elapsed), elapsed)
		if timerWake && work == 0 {
			engine.stats.emptyWakeups++
		}
		nextTick, hasNext := engine.wheel.nextEventTickLocked(engine.currentTick)
		engine.mu.Unlock()
		timerWake = false

		if !hasNext {
			// 空时间轮停止底层 Timer，只等待结构变更或关闭。
			engine.wakeSource.Stop()
			switch engine.waitForWake() {
			case wakeStop:
				return
			case wakeTimer:
				timerWake = true
			}
			continue
		}

		// 如果工作循环已经落后于最近事件，立即再推进一轮，不经过底层 Timer。
		delay := durationUntilTick(engine.startTime, now, nextTick)
		if delay <= 0 {
			continue
		}
		engine.wakeSource.Reset(delay)
		switch engine.waitForWake() {
		case wakeStop:
			return
		case wakeTimer:
			timerWake = true
		}
	}
}

// wakeKind 标识工作 goroutine 被哪一类信号唤醒。
type wakeKind uint8

const (
	wakeChange wakeKind = iota
	wakeTimer
	wakeStop
)

// waitForWake 等待唯一 Timer、合并结构变更或停止信号，并记录低成本统计。
func (engine *Engine) waitForWake() wakeKind {
	select {
	case <-engine.stopSignal:
		return wakeStop
	case <-engine.changeSignal:
		engine.mu.Lock()
		engine.stats.wakeupsTotal++
		engine.stats.changeWakeups++
		engine.mu.Unlock()
		return wakeChange
	case <-engine.wakeSource.C():
		engine.mu.Lock()
		engine.stats.wakeupsTotal++
		engine.stats.timerWakeups++
		engine.mu.Unlock()
		return wakeTimer
	}
}

// advanceLocked 只处理非空事件点，并把逻辑 currentTick 推进到 targetTick。
func (engine *Engine) advanceLocked(targetTick uint64, elapsed time.Duration) uint64 {
	if targetTick <= engine.currentTick {
		return 0
	}

	work := uint64(0)
	for {
		nextTick, exists := engine.wheel.nextEventTickLocked(engine.currentTick)
		if !exists || nextTick > targetTick {
			engine.currentTick = targetTick
			break
		}
		engine.currentTick = nextTick
		work += engine.processTickLocked(nextTick, elapsed)
	}
	return work
}

// processTickLocked 先从高到低级联边界桶，再交付当前 L0 槽的到期 ID。
func (engine *Engine) processTickLocked(tick uint64, elapsed time.Duration) uint64 {
	work := uint64(0)

	// 同时落在多层边界时必须先移动高层，保证本 Tick 到期条目最终可以进入 L0。
	for level := LevelCount - 1; level >= 1; level-- {
		shift := uint(level * slotBits)
		lowerMask := (uint64(1) << shift) - 1
		if tick&lowerMask != 0 {
			continue
		}
		slot := int((tick >> shift) & (SlotsPerLevel - 1))
		bucket := &engine.wheel.buckets[level][slot]
		if bucket.count == 0 {
			continue
		}

		engine.stats.cascades++
		for bucket.head != nil {
			entry := bucket.head
			oldLevel := engine.wheel.removeLocked(entry)
			engine.stats.levelEntries[oldLevel]--
			newLevel := engine.wheel.insertLocked(entry, tick)
			engine.stats.levelEntries[newLevel]++
			engine.stats.cascadedEntries++
			work++
		}
	}

	// 当前 L0 槽中的条目按桶内稳定顺序到期。
	slot := int(tick & (SlotsPerLevel - 1))
	bucket := &engine.wheel.buckets[0][slot]
	for bucket.head != nil {
		entry := bucket.head
		if entry.deadlineTick > tick {
			// L0 当前槽只允许保存本 Tick 到期的条目。若该不变量被破坏，
			// 继续重插会反复落回同一槽并形成死循环，因此立即暴露内部错误。
			panic("timerwheel: L0 当前槽包含未来 Deadline")
		}
		engine.expireEntryLocked(entry, elapsed)
		work++
	}
	return work
}

// expireEntryLocked 把到期 ID 交给 Queue，然后立即回收 timerEntry。
func (engine *Engine) expireEntryLocked(entry *timerEntry, elapsed time.Duration) {
	queue := entry.queue
	deadlineElapsed := durationAtTick(entry.deadlineTick)
	delay := elapsed - deadlineElapsed
	if delay < 0 {
		delay = 0
	}

	// 先复制稳定 ID 到 Queue，再移除全部 entry 引用并归还对象池。
	id := entry.id
	engine.removeScheduledEntryLocked(entry)
	wasEmpty := queue.expired.Len() == 0
	queue.expired.Push(id)
	if wasEmpty {
		// 合并信号只描述“Queue 中至少有一个到期 ID”。
		// 仅在空到非空的状态变化时发送，避免同批到期产生陈旧重复信号。
		queue.notifyLocked()
	}
	engine.stats.expired++
	engine.stats.expiredTotal++
	if engine.stats.expired > engine.stats.expiredPeak {
		engine.stats.expiredPeak = engine.stats.expired
	}
	engine.stats.lastExpiryDelay = delay
	if delay > engine.stats.maxExpiryDelay {
		engine.stats.maxExpiryDelay = delay
	}
	engine.releaseEntryLocked(entry)
}

// floorTick 把非负相对时长向下换算为当前 Tick。
func floorTick(elapsed time.Duration) uint64 {
	if elapsed <= 0 {
		return 0
	}
	return uint64(elapsed / TickDuration)
}

// ceilTick 把非负相对时长向上换算为 Deadline Tick。
func ceilTick(elapsed time.Duration) uint64 {
	if elapsed <= 0 {
		return 0
	}
	tick := uint64(elapsed / TickDuration)
	if elapsed%TickDuration != 0 {
		tick++
	}
	return tick
}

// durationUntilTick 返回从 now 到指定绝对 Tick 边界的剩余时长。
func durationUntilTick(start, now time.Time, tick uint64) time.Duration {
	elapsed := now.Sub(start)
	if elapsed < 0 {
		elapsed = 0
	}
	target := durationAtTick(tick)
	if target <= elapsed {
		return 0
	}
	return target - elapsed
}

// durationAtTick 把 Tick 边界转换为 time.Duration，并在最后一个不完整 Tick 饱和。
//
// time.Duration 最大正值不是 10ms 的整数倍；向上取整后的最后一个 Tick 会略超 int64。
// 饱和到 MaxInt64 仍不会早于调用方原始可表示 Deadline，同时避免乘法环绕成负数。
func durationAtTick(tick uint64) time.Duration {
	maxWholeTick := uint64(math.MaxInt64 / int64(TickDuration))
	if tick > maxWholeTick {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(tick) * TickDuration
}

package log

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// eventKind 区分单日志协程串行处理的三类队列事件。
type eventKind uint8

const (
	// writeEvent 携带一条普通或可靠日志。
	writeEvent eventKind = iota
	// flushEvent 是位于队列中的顺序屏障。
	flushEvent
	// closeEvent 是最后一个事件，处理后日志协程退出。
	closeEvent
)

// logEvent 是调用协程向唯一日志协程转移所有权的队列单元。
type logEvent struct {
	// kind 决定 record、fields 和 done 的解释方式。
	kind eventKind
	// record 和 fields 只在 writeEvent 中有效，入队后由日志协程独占。
	record Record
	fields []Field
	// done 非 nil 表示调用方需要等待处理结果；容量 1 防止超时后阻塞日志协程。
	done chan error
}

// Stats 是日志 Runtime 的不可变计数快照。
type Stats struct {
	// Dropped* 分级记录异步队列满时主动丢弃的普通日志。
	DroppedDebug          uint64
	DroppedInfo           uint64
	DroppedWarn           uint64
	DroppedError          uint64
	WriteFailures         uint64
	ReliableWriteTimeouts uint64
}

// counters 保存 Runtime 内部可并发更新的统计值。
type counters struct {
	droppedDebug          atomic.Uint64
	droppedInfo           atomic.Uint64
	droppedWarn           atomic.Uint64
	droppedError          atomic.Uint64
	writeFailures         atomic.Uint64
	reliableWriteTimeouts atomic.Uint64
}

// Runtime 拥有日志队列、日志协程和 Handler 生命周期。
type Runtime struct {
	// config 在构造后只读；handler 只由日志协程执行 Write/Sync/Close。
	config  Config
	handler Handler
	// queue 保存事件指针，slots 单独控制队列准入并为关闭事件预留顺序。
	queue chan *logEvent
	slots chan struct{}

	// state 为 Enabled 提供无锁生命周期快照，精确提交计数由 submitMu 保护。
	state atomic.Uint32
	stats counters

	// submitMu 保护准入开关和正在提交但尚未完成入队的调用数量。
	submitMu    sync.Mutex
	submitCond  *sync.Cond
	accepting   bool
	submitCount int
	stopSubmit  chan struct{}

	// closeOnce 保证只启动一次关闭流程；closed 广播最终关闭结果已经就绪。
	closeOnce sync.Once
	closed    chan struct{}
	closeMu   sync.Mutex
	closeErr  error
}

const (
	// Runtime 从 running 单向进入 closing，最终进入 closed。
	runtimeRunning uint32 = iota
	runtimeClosing
	runtimeClosed
)

// NewRuntime 使用指定 Handler 创建独立日志 Runtime。
func NewRuntime(config Config, handler Handler) (*Runtime, error) {
	// 先校验 Runtime 自身依赖的模式，失败时不能接管 Handler 生命周期。
	if err := config.validateRuntime(); err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, invalidConfig("log handler is nil")
	}

	// 一次性创建有界队列、容量令牌和关闭通知，所有权归新 Runtime。
	instance := &Runtime{
		config:     config,
		handler:    handler,
		queue:      make(chan *logEvent, eventQueueSize),
		slots:      make(chan struct{}, eventQueueSize),
		accepting:  true,
		stopSubmit: make(chan struct{}),
		closed:     make(chan struct{}),
	}
	// 条件变量与 submitMu 配套，用于关闭阶段等待提交临界区排空。
	instance.submitCond = sync.NewCond(&instance.submitMu)
	// Runtime 成功返回前启动唯一日志协程；Close 负责等待它退出。
	go instance.run()
	return instance, nil
}

// Logger 返回共享该 Runtime 的根 Logger。
func (runtime *Runtime) Logger() Logger {
	// nil Runtime 返回可安全传播的 Nop Logger。
	if runtime == nil {
		return NewNop()
	}
	// 根 Logger 不预绑定字段，也不额外跳过业务栈帧。
	return Logger{runtime: runtime}
}

// Stats 返回当前计数快照。
func (runtime *Runtime) Stats() Stats {
	// nil Runtime 没有统计，返回不可误解的零值快照。
	if runtime == nil {
		return Stats{}
	}
	// 每个计数独立原子读取；快照允许来自相邻时刻。
	return Stats{
		DroppedDebug:          runtime.stats.droppedDebug.Load(),
		DroppedInfo:           runtime.stats.droppedInfo.Load(),
		DroppedWarn:           runtime.stats.droppedWarn.Load(),
		DroppedError:          runtime.stats.droppedError.Load(),
		WriteFailures:         runtime.stats.writeFailures.Load(),
		ReliableWriteTimeouts: runtime.stats.reliableWriteTimeouts.Load(),
	}
}

// Flush 等待此前已进入队列的日志完成并刷新 Handler。
func (runtime *Runtime) Flush(ctx context.Context) error {
	// Flush 需要活动 Runtime；关闭后不能再建立新的顺序屏障。
	if runtime == nil {
		return errs.ErrLogClosed
	}
	// nil Context 视为无外部取消，保持 API 易用。
	if ctx == nil {
		ctx = context.Background()
	}
	// 进入提交临界区，确保 Close 会等待本次 Flush 完成入队。
	if !runtime.beginSubmit() {
		return errs.ErrLogClosed
	}
	defer runtime.endSubmit()

	// 先取得容量令牌；Context 取消或关闭开始时及时退出。
	if err := runtime.reserve(ctx); err != nil {
		return err
	}
	// Flush 事件排在此前所有已入队写事件之后，形成严格顺序屏障。
	event := &logEvent{kind: flushEvent, done: make(chan error, 1)}
	runtime.queue <- event

	// 等待 Handler.Sync 结果，但调用方 Context 可以先结束等待。
	select {
	case err := <-event.done:
		return err
	case <-ctx.Done():
		return contextError(ctx.Err())
	}
}

// Close 停止准入、排空队列并关闭 Handler。重复调用安全。
func (runtime *Runtime) Close(ctx context.Context) error {
	// nil Runtime 没有资源，关闭保持幂等成功。
	if runtime == nil {
		return nil
	}
	// nil Context 使用后台 Context，表示一直等待完整排空。
	if ctx == nil {
		ctx = context.Background()
	}

	// 首次调用触发关闭；后续调用只共享同一个完成信号和结果。
	runtime.closeOnce.Do(runtime.startClose)
	// 调用方超时不会中断真实关闭流程，资源仍由后台 finishClose 回收。
	select {
	case <-runtime.closed:
		// closeErr 在 closed 关闭前写入；互斥锁保持竞态检测下语义明确。
		runtime.closeMu.Lock()
		err := runtime.closeErr
		runtime.closeMu.Unlock()
		return err
	case <-ctx.Done():
		return contextError(ctx.Err())
	}
}

// enabled 在生命周期准入保护内询问 Handler 的级别策略。
func (runtime *Runtime) enabled(level Level) bool {
	// 无效级别、非运行状态或关闭后的新提交都直接返回 false。
	if !level.valid() || runtime.state.Load() != runtimeRunning || !runtime.beginSubmit() {
		return false
	}
	defer runtime.endSubmit()
	// Handler 构造后只读其级别配置，允许和日志协程并发调用 Enabled。
	return runtime.handler.Enabled(level)
}

// write 根据日志重要性和 Runtime 模式把一条记录投递给日志协程。
func (runtime *Runtime) write(logger Logger, level Level, message string, withStack bool, fields []Field) {
	// beginSubmit 把“检查准入到完成入队”纳入 Close 的等待范围。
	if !level.valid() || !runtime.beginSubmit() {
		return
	}
	defer runtime.endSubmit()
	// 在采集调用栈和复制字段前做级别过滤，降低关闭日志的开销。
	if !runtime.handler.Enabled(level) {
		return
	}

	// ErrorStack 使用有界等待的可靠路径，不参与普通异步丢弃策略。
	if withStack {
		runtime.writeReliable(logger, level, message, fields)
		return
	}

	// 异步模式只尝试取得令牌，队列满时按级别计数后立即返回。
	if runtime.config.Mode == AsyncMode {
		select {
		case runtime.slots <- struct{}{}:
		default:
			runtime.recordDrop(level)
			return
		}
	} else {
		// 同步模式允许阻塞等待容量，保证开发期每条日志都被处理。
		runtime.slots <- struct{}{}
	}

	// 在调用协程捕获调用者并复制字段，随后把事件所有权交给队列。
	event := runtime.makeWriteEvent(
		logger,
		newRecord(level, message, logger.callerSkip, false),
		fields,
		runtime.config.Mode == SyncMode,
	)
	runtime.queue <- event
	// 同步模式带 done 通知；异步模式 done 为 nil 并立即返回。
	if event.done != nil {
		<-event.done
	}
}

// writeReliable 在最多一秒内争取写出带堆栈的关键错误。
func (runtime *Runtime) writeReliable(logger Logger, level Level, message string, fields []Field) {
	// 堆栈必须在原调用协程采集；额外跳过本可靠写入函数本身。
	record := newRecord(level, message, logger.callerSkip+1, true)
	// 同一个 Timer 覆盖等待容量和等待写完的总时间预算。
	timer := time.NewTimer(reliableWriteTimeout)
	defer timer.Stop()

	select {
	case runtime.slots <- struct{}{}:
	case <-timer.C:
		// 无法进入队列时直接走 stderr 兜底，关键错误不会静默丢失。
		runtime.recordReliableTimeout(record)
		return
	}

	// 取得令牌后创建带完成通知的事件并移交日志协程。
	event := runtime.makeWriteEvent(logger, record, fields, true)
	runtime.queue <- event

	select {
	case <-event.done:
	case <-timer.C:
		// Handler 卡住时调用方按预算返回，同时记录超时并兜底输出。
		runtime.recordReliableTimeout(record)
	}
}

// makeWriteEvent 复制字段并按等待策略创建一次性队列事件。
func (runtime *Runtime) makeWriteEvent(
	logger Logger,
	record Record,
	fields []Field,
	wait bool,
) *logEvent {
	// 事件必须独占字段切片，调用方返回后修改原切片不能影响日志内容。
	ownedFields := make([]Field, 0, len(logger.fields)+len(fields))
	ownedFields = append(ownedFields, logger.fields...)
	ownedFields = appendValidFields(ownedFields, fields)

	event := &logEvent{
		kind:   writeEvent,
		record: record,
		fields: ownedFields,
	}
	// 仅同步或可靠调用创建完成通道，避免普通异步日志额外分配。
	if wait {
		event.done = make(chan error, 1)
	}
	return event
}

// run 是唯一执行 Handler 写入、刷新和关闭的日志协程。
func (runtime *Runtime) run() {
	// queue 不主动关闭；closeEvent 是有序且唯一的退出标记。
	for event := range runtime.queue {
		// 每个入队事件都对应一个容量令牌，取出后立即释放队列槽位。
		<-runtime.slots

		// Handler 的所有有副作用方法严格在此处串行执行。
		switch event.kind {
		case writeEvent:
			err := runtime.handler.Write(event.record, event.fields)
			if err != nil {
				// 输出失败不能递归写日志，改用原子统计和 stderr 兜底。
				runtime.stats.writeFailures.Add(1)
				fallback(event.record, err)
			}
			// done 为空时 notify 是无操作，统一同步与异步处理路径。
			notify(event.done, err)
		case flushEvent:
			// 事件顺序保证 Sync 发生在此前写入完成之后。
			notify(event.done, outputError(runtime.handler.Sync()))
		case closeEvent:
			// 关闭前最后刷新一次；无论结果如何都尝试 Close Handler。
			err := errors.Join(runtime.handler.Sync(), runtime.handler.Close())
			notify(event.done, outputError(err))
			return
		}
	}
}

// beginSubmit 原子地检查准入并登记一个正在提交的调用。
func (runtime *Runtime) beginSubmit() bool {
	// 检查 accepting 与递增 submitCount 必须处于同一临界区。
	runtime.submitMu.Lock()
	defer runtime.submitMu.Unlock()

	if !runtime.accepting {
		return false
	}
	// 计数覆盖调用方从当前时刻直到事件完成入队或提前返回。
	runtime.submitCount++
	return true
}

// endSubmit 注销提交调用，并在最后一个调用退出时唤醒关闭流程。
func (runtime *Runtime) endSubmit() {
	runtime.submitMu.Lock()
	runtime.submitCount--
	// 只在归零时广播，避免 finishClose 在仍有提交时反复唤醒。
	if runtime.submitCount == 0 {
		runtime.submitCond.Broadcast()
	}
	runtime.submitMu.Unlock()
}

// startClose 同步关闭新准入，然后异步执行可能阻塞的排空。
func (runtime *Runtime) startClose() {
	// 先切换状态和准入开关，保证此后没有新调用进入提交临界区。
	runtime.submitMu.Lock()
	runtime.state.Store(runtimeClosing)
	runtime.accepting = false
	close(runtime.stopSubmit)
	runtime.submitMu.Unlock()

	// finishClose 可能等待正在提交的同步日志，不能阻塞第一个 Close 调用本身。
	go runtime.finishClose()
}

// finishClose 等待提交者入队，随后用最后一个事件关闭 Handler。
func (runtime *Runtime) finishClose() {
	// 等待所有已获准调用完成入队或提前退出，确保 closeEvent 排在它们之后。
	runtime.submitMu.Lock()
	for runtime.submitCount != 0 {
		runtime.submitCond.Wait()
	}
	runtime.submitMu.Unlock()

	// 为 closeEvent 取得令牌并入队；此时没有新提交者会与它竞争。
	runtime.slots <- struct{}{}
	event := &logEvent{kind: closeEvent, done: make(chan error, 1)}
	runtime.queue <- event
	err := <-event.done

	// 先发布最终错误和 closed 状态，再关闭完成通道唤醒全部等待者。
	runtime.closeMu.Lock()
	runtime.closeErr = err
	runtime.closeMu.Unlock()
	runtime.state.Store(runtimeClosed)
	close(runtime.closed)
}

// reserve 在 Flush 等控制事件入队前等待一个可取消的容量令牌。
func (runtime *Runtime) reserve(ctx context.Context) error {
	// 同时监听关闭和调用方 Context，避免无限等待满队列。
	select {
	case runtime.slots <- struct{}{}:
		return nil
	case <-runtime.stopSubmit:
		return errs.ErrLogClosed
	case <-ctx.Done():
		return contextError(ctx.Err())
	}
}

// recordDrop 按日志级别累计异步队列满造成的丢弃数量。
func (runtime *Runtime) recordDrop(level Level) {
	// 只接受已经通过 valid 校验的四个公开级别。
	switch level {
	case DebugLevel:
		runtime.stats.droppedDebug.Add(1)
	case InfoLevel:
		runtime.stats.droppedInfo.Add(1)
	case WarnLevel:
		runtime.stats.droppedWarn.Add(1)
	case ErrorLevel:
		runtime.stats.droppedError.Add(1)
	}
}

// recordReliableTimeout 记录可靠写入超时并直接输出最小诊断信息。
func (runtime *Runtime) recordReliableTimeout(record Record) {
	// 先记统计，再走不依赖 Runtime 队列的 stderr 兜底。
	runtime.stats.reliableWriteTimeouts.Add(1)
	fallback(record, context.DeadlineExceeded)
}

// notify 向需要等待的调用方发送一次处理结果。
func notify(target chan error, err error) {
	// 异步事件不创建 done，统一调用时在此跳过。
	if target != nil {
		target <- err
	}
}

// outputError 把 Handler 本地错误映射为稳定日志输出错误码。
func outputError(err error) error {
	// nil 保持成功，不创建包装对象。
	if err == nil {
		return nil
	}
	// 保留底层原因，方便 errors.Is/As 和本地排障。
	return errs.Wrap(errs.CodeLogOutputFailed, err)
}

// contextError 把标准 Context 结束原因映射为 Origin 通用错误码。
func contextError(err error) error {
	// 只重分类标准的取消和超时，其余错误原样返回。
	switch err {
	case context.Canceled:
		return errs.Wrap(errs.CodeCanceled, err)
	case context.DeadlineExceeded:
		return errs.Wrap(errs.CodeDeadlineExceeded, err)
	default:
		return err
	}
}

// fallback 在日志 Handler 不可用时直接向标准错误输出最小诊断。
func fallback(record Record, err error) {
	// 不能再次调用 Logger，否则 Handler 故障会造成递归；写入错误也只能忽略。
	_, _ = fmt.Fprintf(
		os.Stderr,
		"origin log fallback: level=%s caller=%s:%d message=%q error=%q\n",
		record.Level,
		record.Caller.File,
		record.Caller.Line,
		record.Message,
		err.Error(),
	)
}

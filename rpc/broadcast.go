package rpc

import (
	"context"

	"github.com/duanhf2012/origin/v3/errs"
)

// broadcastPlan 保存一次多目标广播从 Prepare 到 Submit 必须复用的固定视图。
//
// 计划不复制发现候选、标签或成功目标；提交阶段按同一 candidateSet 再扫描一次，因此计划
// 大小不随目标数增长。lastSendableRaw 指定原始编码 Buffer 的唯一最终消费者。
type broadcastPlan struct {
	set             candidateSet
	methodID        MethodID
	intentCount     int
	sendableCount   int
	lastSendableRaw int
}

// prepareBroadcast 固定完整意图范围，并在编码前完成全局错误分类。
func (runtime *Runtime) prepareBroadcast(
	ctx context.Context,
	client Client,
	methodID MethodID,
) (preparedTarget, *broadcastPlan, error) {
	// 先检查调用和 Runtime 状态，保证任何全局失败都发生在 Sizer 与 Buffer 申请之前。
	if runtime == nil || ctx == nil || methodID == 0 {
		return preparedTarget{}, nil, errs.ErrInvalidArgument
	}
	if cause := context.Cause(ctx); cause != nil {
		return preparedTarget{}, nil, contextError(cause)
	}
	if !runtime.frozen.Load() {
		return preparedTarget{}, nil, errs.ErrServiceNotReady
	}
	if runtime.closed.Load() {
		return preparedTarget{}, nil, errs.ErrServiceStopped
	}

	// 一次构造同时捕获 Discovery、本地 endpoint、TCP 分片表和 NATS connection 视图。
	// Broadcast 明确忽略单目标 routeSpec，包括无效 Key 和自定义 Selector。
	set := runtime.buildCandidateSet(
		client.target,
		client.contractID,
		client.fingerprint,
		client.includeRetired,
	)
	intentCount := 0
	sendableCount := 0
	lastSendableRaw := -1
	for rawIndex := 0; rawIndex < set.rawCount(); rawIndex++ {
		candidate, exists := set.rawAt(rawIndex)
		if !exists {
			continue
		}
		set.scan.sameName = true
		if !set.contractMatches(candidate) {
			continue
		}
		set.scan.contract = true
		if !set.lifecycleMatches(candidate) {
			continue
		}
		set.scan.lifecycle = true

		// 契约与生命周期合法即属于广播意图。Transport 不兼容或当前断开只影响可发送数，
		// 不能把该实例从部分失败结果和总放大容量中静默删除。
		intentCount++
		if intentCount > maxRemoteTargets {
			return preparedTarget{}, nil, errs.ErrTransportOverloaded
		}
		candidate, compatible := set.transportCandidate(candidate)
		if !compatible {
			continue
		}
		set.scan.transport = true
		if !set.connected(candidate) {
			continue
		}
		set.scan.connected = true
		sendableCount++
		lastSendableRaw = rawIndex
	}

	// 零意图仍沿用 M19 对无服务、契约不匹配和生命周期不合法的稳定分类。
	if intentCount == 0 {
		return preparedTarget{}, nil, set.routeError()
	}
	if sendableCount == 0 {
		if intentCount == 1 {
			return preparedTarget{}, nil, errs.ErrTransportUnavailable
		}
		return preparedTarget{}, nil, newBroadcastError(
			intentCount,
			0,
			broadcastUnavailableFailures(&set, intentCount),
		)
	}

	// 唯一合法目标继续使用原有 prepared target 和 Notify 提交路径，避免新增 plan 分配。
	if intentCount == 1 {
		candidate, exists := broadcastSendableAt(&set, lastSendableRaw)
		if !exists {
			return preparedTarget{}, nil, errs.ErrTransportUnavailable
		}
		return preparedTargetFromCandidate(candidate, methodID, CallNotify), nil, nil
	}

	return preparedTarget{}, &broadcastPlan{
		set:             set,
		methodID:        methodID,
		intentCount:     intentCount,
		sendableCount:   sendableCount,
		lastSendableRaw: lastSendableRaw,
	}, nil
}

// broadcastSendableAt 从固定原始位置重新取得已捕获 Transport 句柄。
func broadcastSendableAt(
	set *candidateSet,
	rawIndex int,
) (routeCandidate, bool) {
	candidate, exists := set.rawAt(rawIndex)
	if !exists || !set.contractMatches(candidate) || !set.lifecycleMatches(candidate) {
		return routeCandidate{}, false
	}
	candidate, compatible := set.transportCandidate(candidate)
	return candidate, compatible && set.connected(candidate)
}

// preparedTargetFromCandidate 把一次固定候选转换为 M19 的精确提交值。
func preparedTargetFromCandidate(
	candidate routeCandidate,
	methodID MethodID,
	kind CallKind,
) preparedTarget {
	return preparedTarget{
		transport:   candidate.transport,
		nodeID:      candidate.nodeID,
		sessionID:   candidate.sessionID,
		serviceName: candidate.serviceName,
		methodID:    methodID,
		kind:        kind,
		endpoint:    candidate.endpoint,
		tcpSession:  candidate.tcpSession,
		natsView:    candidate.natsView,
	}
}

// broadcastUnavailableFailures 为 Prepare 阶段的多目标全部不可用结果建立稳定详情。
func broadcastUnavailableFailures(
	set *candidateSet,
	capacity int,
) []BroadcastFailure {
	// 只有失败返回路径分配详情；成功计划继续保持不按目标数分配 Go 对象。
	failures := make([]BroadcastFailure, 0, capacity)
	for rawIndex := 0; rawIndex < set.rawCount(); rawIndex++ {
		candidate, exists := set.rawAt(rawIndex)
		if !exists || !set.contractMatches(candidate) || !set.lifecycleMatches(candidate) {
			continue
		}
		failures = append(failures, BroadcastFailure{
			NodeID: candidate.nodeID,
			Err:    errs.ErrTransportUnavailable,
		})
	}
	return failures
}

// submitBroadcast 按冻结 NodeID 顺序完成有界、非阻塞的逐目标本地提交。
//
// request 进入本函数后始终由本函数消费：成功提交时转移给目标，失败或未尝试时由当前栈
// 释放。调用方无论收到 nil 还是 error 都不得再次访问该 Buffer。
func (runtime *Runtime) submitBroadcast(
	ctx context.Context,
	client Client,
	plan *broadcastPlan,
	request *Buffer,
) error {
	if runtime == nil || ctx == nil || plan == nil || request == nil ||
		plan.set.runtime != runtime || plan.methodID == 0 ||
		plan.intentCount <= 1 || plan.sendableCount <= 0 {
		releaseBuffer(request)
		return errs.ErrInvalidArgument
	}
	if !runtime.frozen.Load() {
		request.Release()
		return errs.ErrServiceNotReady
	}
	if runtime.closed.Load() {
		request.Release()
		return errs.ErrServiceStopped
	}

	payload := request.Bytes()
	payloadSize := len(payload)
	succeeded := 0
	originalConsumed := false
	var failures []BroadcastFailure
	for rawIndex := 0; rawIndex < plan.set.rawCount(); rawIndex++ {
		candidate, intent := broadcastIntentAt(&plan.set, rawIndex)
		if !intent {
			continue
		}

		// Context 中途结束后不再申请或提交 Buffer；当前及剩余意图都保留相同稳定原因。
		if cause := context.Cause(ctx); cause != nil {
			failures = appendBroadcastFailure(
				failures,
				plan.intentCount-succeeded,
				candidate.nodeID,
				contextError(cause),
			)
			continue
		}
		candidate, compatible := plan.set.transportCandidate(candidate)
		if !compatible || !plan.set.connected(candidate) {
			failures = appendBroadcastFailure(
				failures,
				plan.intentCount-succeeded,
				candidate.nodeID,
				errs.ErrTransportUnavailable,
			)
			continue
		}

		// 最后一个 Prepare 时可发送的目标唯一消费原始 Buffer；此前目标各自取得带精确
		// headroom 的池化副本，复制时原始 Buffer 尚未前置任何协议头。
		current := request
		if rawIndex != plan.lastSendableRaw {
			prepared := preparedTargetFromCandidate(candidate, plan.methodID, CallNotify)
			var err error
			current, err = runtime.AllocatePreparedRequest(
				prepared,
				payloadSize,
				CallNotify,
			)
			if err != nil {
				failures = appendBroadcastFailure(
					failures,
					plan.intentCount-succeeded,
					candidate.nodeID,
					err,
				)
				continue
			}
			copy(current.Bytes(), payload)
		} else {
			originalConsumed = true
		}

		err := runtime.submitFrozenBroadcastNotify(
			ctx,
			candidate,
			client.fingerprint,
			plan.methodID,
			current,
		)
		if err != nil {
			current.Release()
			failures = appendBroadcastFailure(
				failures,
				plan.intentCount-succeeded,
				candidate.nodeID,
				err,
			)
			continue
		}
		succeeded++
	}

	// 原始目标在抵达其稳定位置前可能因 Context 或内部防御性分支未被消费，必须统一归还。
	if !originalConsumed {
		request.Release()
	}
	if len(failures) == 0 {
		return nil
	}
	return newBroadcastError(plan.intentCount, succeeded, failures)
}

// broadcastIntentAt 判断固定原始位置是否属于本次契约和生命周期范围。
func broadcastIntentAt(
	set *candidateSet,
	rawIndex int,
) (routeCandidate, bool) {
	candidate, exists := set.rawAt(rawIndex)
	if !exists || !set.contractMatches(candidate) || !set.lifecycleMatches(candidate) {
		return routeCandidate{}, false
	}
	return candidate, true
}

// appendBroadcastFailure 在第一次失败时建立一次性详情数组，成功路径保持零详情分配。
func appendBroadcastFailure(
	failures []BroadcastFailure,
	capacity int,
	nodeID string,
	cause error,
) []BroadcastFailure {
	if failures == nil {
		failures = make([]BroadcastFailure, 0, capacity)
	}
	return append(failures, BroadcastFailure{NodeID: nodeID, Err: cause})
}

// submitFrozenBroadcastNotify 只复核 Prepare 捕获的连接身份，不读取新的 Discovery 快照。
func (runtime *Runtime) submitFrozenBroadcastNotify(
	ctx context.Context,
	candidate routeCandidate,
	fingerprint ContractFingerprint,
	methodID MethodID,
	request *Buffer,
) error {
	switch candidate.transport {
	case preparedLocal:
		_, err := runtime.submitLocal(
			ctx,
			candidate.endpoint,
			methodID,
			CallNotify,
			request,
			nil,
		)
		return err
	case preparedTCP:
		if runtime.remote == nil {
			return errs.ErrTransportUnavailable
		}
		current := runtime.remote.targetSession(candidate.nodeID, candidate.sessionID)
		if current == nil || current != candidate.tcpSession {
			return errs.ErrTransportUnavailable
		}
		return current.sendNotify(
			candidate.serviceName,
			fingerprint,
			methodID,
			request,
		)
	case preparedNATS:
		if runtime.nats == nil {
			return errs.ErrTransportUnavailable
		}
		conn, err := runtime.nats.preparedConn(candidate.natsView)
		if err != nil {
			return err
		}
		return runtime.nats.sendNotifyWithConn(
			conn,
			candidate.nodeID,
			candidate.sessionID,
			candidate.serviceName,
			methodID,
			request,
		)
	default:
		return errs.ErrTransportUnavailable
	}
}

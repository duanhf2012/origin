package rpc

import (
	"encoding/binary"
	"math"
	"time"
	"unicode/utf8"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

const (
	// tcpWireVersion 标识 Origin TCP RPC 当前不兼容线布局版本。
	tcpWireVersion byte = 1

	// wireEnvelopeSize 是 TCP 层在业务 payload 上限之外允许的固定协议包络余量。
	wireEnvelopeSize = 576

	// 主动方业务包必须区分 Request、Notify 和 Ping；被动方只返回 Response 或一字节 Pong。
	wireKindRequest byte = 1
	wireKindNotify  byte = 2
	wireKindPing    byte = 3
	wireKindPong    byte = 4

	// 固定头大小与当前 TCP Wire 的逐字段布局严格一致。
	wireRequestFixedSize  = 22
	wireNotifyFixedSize   = 10
	wireResponseFixedSize = 12
	wireHeartbeatSize     = 1
	wireHelloFixedSize    = 11
	wireHelloAckFixedSize = 6
	wireServiceFixedSize  = 33

	// NodeID 和 ServiceName 使用 uint8 长度，避免热路径多一个无效长度字节。
	wireMaxNameSize = math.MaxUint8
)

// wireServiceEntry 是 HelloAck 中一个公开 Service 的不可变契约目录项。
type wireServiceEntry struct {
	name        string
	fingerprint ContractFingerprint
}

// wireHello 是主动连接方第一帧声明的来源 Node 和精确目标会话。
type wireHello struct {
	sourceNodeID    string
	targetNodeID    string
	targetSessionID uint64
}

// wireHelloAck 是被连接方返回的握手结论和公开契约目录。
//
// 目标 NodeID 和 SessionID 已在 Hello 中校验，固定连接方向使 Ack 不必重复返回这些字段。
type wireHelloAck struct {
	statusCode errs.Code
	services   []wireServiceEntry
}

// wireRequestView 借用入站 Buffer 中的 Request 头字段和业务 payload。
//
// serviceName 和 payload 只在原 Buffer 释放前有效，不能交给其他长期对象保存。
type wireRequestView struct {
	requestID        uint64
	methodID         MethodID
	remainingTimeout time.Duration
	serviceName      []byte
	payloadOffset    int
}

// wireNotifyView 借用入站 Buffer 中的 Notify 头字段。
type wireNotifyView struct {
	methodID      MethodID
	serviceName   []byte
	payloadOffset int
}

// wireResponseView 借用入站 Buffer 中的 Response 头字段。
type wireResponseView struct {
	requestID     uint64
	errorCode     errs.Code
	payloadOffset int
}

// encodeHello 创建主动方第一帧。握手属于连接冷路径，可以按准确大小申请 Buffer。
func encodeHello(
	pool *bufferpool.Pool,
	sourceNodeID string,
	targetNodeID string,
	targetSessionID uint64,
) (*bufferpool.Buffer, error) {
	if pool == nil ||
		!validWireName(sourceNodeID) ||
		!validWireName(targetNodeID) ||
		targetSessionID == 0 {
		return nil, errs.ErrInvalidArgument
	}

	// 固定头直接保存版本、两个名称长度和紧凑 SessionID，不再携带 ASCII Magic。
	buffer := pool.Acquire(
		wireHelloFixedSize + len(sourceNodeID) + len(targetNodeID),
	)
	data := buffer.Bytes()
	data[0] = tcpWireVersion
	data[1] = byte(len(sourceNodeID))
	data[2] = byte(len(targetNodeID))
	binary.BigEndian.PutUint64(data[3:11], targetSessionID)
	offset := wireHelloFixedSize
	copy(data[offset:], sourceNodeID)
	offset += len(sourceNodeID)
	copy(data[offset:], targetNodeID)
	return buffer, nil
}

// parseHello 严格解析主动方第一帧，并拒绝未知版本、尾部数据和非法 UTF-8。
func parseHello(data []byte) (wireHello, error) {
	if len(data) < wireHelloFixedSize || data[0] != tcpWireVersion {
		return wireHello{}, errs.ErrTransportProtocol
	}
	sourceLength := int(data[1])
	targetLength := int(data[2])
	targetSessionID := binary.BigEndian.Uint64(data[3:11])
	total := wireHelloFixedSize + sourceLength + targetLength
	if sourceLength == 0 ||
		targetLength == 0 ||
		targetSessionID == 0 ||
		total != len(data) {
		return wireHello{}, errs.ErrTransportProtocol
	}
	offset := wireHelloFixedSize
	sourceBytes := data[offset : offset+sourceLength]
	offset += sourceLength
	targetBytes := data[offset : offset+targetLength]
	if !utf8.Valid(sourceBytes) || !utf8.Valid(targetBytes) {
		return wireHello{}, errs.ErrTransportProtocol
	}
	return wireHello{
		sourceNodeID:    string(sourceBytes),
		targetNodeID:    string(targetBytes),
		targetSessionID: targetSessionID,
	}, nil
}

// encodeHelloAck 创建被连接方握手响应；非成功响应不得携带 Service 目录。
func encodeHelloAck(
	pool *bufferpool.Pool,
	statusCode errs.Code,
	services []wireServiceEntry,
) (*bufferpool.Buffer, error) {
	if pool == nil ||
		len(services) > math.MaxUint16 ||
		(statusCode != errs.CodeOK && len(services) != 0) {
		return nil, errs.ErrInvalidArgument
	}
	size := wireHelloAckFixedSize
	for _, service := range services {
		if !validWireName(service.name) {
			return nil, errs.ErrInvalidArgument
		}
		size += wireServiceFixedSize + len(service.name)
		if size > DefaultMaxPayloadSize+wireEnvelopeSize {
			return nil, errs.ErrTransportMessageTooLarge
		}
	}

	// 按最终准确大小一次申请并顺序写入，握手目录不建立中间序列化对象。
	buffer := pool.Acquire(size)
	data := buffer.Bytes()
	binary.BigEndian.PutUint32(data[0:4], uint32(statusCode))
	binary.BigEndian.PutUint16(data[4:6], uint16(len(services)))
	offset := wireHelloAckFixedSize
	for _, service := range services {
		data[offset] = byte(len(service.name))
		offset++
		copy(data[offset:], service.name)
		offset += len(service.name)
		copy(data[offset:], service.fingerprint[:])
		offset += len(service.fingerprint)
	}
	return buffer, nil
}

// parseHelloAck 严格解析握手结果，并保证目录名称唯一、没有截断或尾部数据。
func parseHelloAck(data []byte) (wireHelloAck, error) {
	if len(data) < wireHelloAckFixedSize {
		return wireHelloAck{}, errs.ErrTransportProtocol
	}
	status := errs.Code(binary.BigEndian.Uint32(data[0:4]))
	serviceCount := int(binary.BigEndian.Uint16(data[4:6]))
	if status != errs.CodeOK && serviceCount != 0 {
		return wireHelloAck{}, errs.ErrTransportProtocol
	}

	// 目录只在握手冷路径分配一次，随后成为出站会话的只读兼容快照。
	offset := wireHelloAckFixedSize
	services := make([]wireServiceEntry, 0, serviceCount)
	seen := make(map[string]struct{}, serviceCount)
	for index := 0; index < serviceCount; index++ {
		if offset >= len(data) {
			return wireHelloAck{}, errs.ErrTransportProtocol
		}
		nameLength := int(data[offset])
		offset++
		if nameLength == 0 ||
			nameLength > len(data)-offset ||
			len(data)-offset-nameLength < len(ContractFingerprint{}) {
			return wireHelloAck{}, errs.ErrTransportProtocol
		}
		nameBytes := data[offset : offset+nameLength]
		if !utf8.Valid(nameBytes) {
			return wireHelloAck{}, errs.ErrTransportProtocol
		}
		name := string(nameBytes)
		if _, duplicate := seen[name]; duplicate {
			return wireHelloAck{}, errs.ErrTransportProtocol
		}
		seen[name] = struct{}{}
		offset += nameLength
		var fingerprint ContractFingerprint
		copy(fingerprint[:], data[offset:offset+len(fingerprint)])
		offset += len(fingerprint)
		services = append(services, wireServiceEntry{
			name:        name,
			fingerprint: fingerprint,
		})
	}
	if offset != len(data) {
		return wireHelloAck{}, errs.ErrTransportProtocol
	}
	return wireHelloAck{statusCode: status, services: services}, nil
}

// prependRequest 在已经编码的业务 payload 前原地写入最小 Request 头。
func prependRequest(
	buffer *bufferpool.Buffer,
	requestID uint64,
	methodID MethodID,
	remaining time.Duration,
	serviceName string,
) error {
	remainingMillis, ok := durationToWireMillis(remaining)
	if buffer == nil || requestID == 0 || methodID == 0 || !ok ||
		!validWireName(serviceName) {
		return errs.ErrInvalidArgument
	}
	headerSize := wireRequestFixedSize + len(serviceName)
	header, ok := buffer.Prepend(headerSize)
	if !ok {
		return errs.ErrRPCEncodeFailed
	}
	header[0] = wireKindRequest
	binary.BigEndian.PutUint64(header[1:9], requestID)
	binary.BigEndian.PutUint64(header[9:17], uint64(methodID))
	binary.BigEndian.PutUint32(header[17:21], remainingMillis)
	header[21] = byte(len(serviceName))
	copy(header[22:], serviceName)
	return nil
}

// parseRequest 返回借用视图；调用方校验成功后再 DiscardPrefix 转移业务 payload。
func parseRequest(data []byte) (wireRequestView, error) {
	if len(data) < wireRequestFixedSize || data[0] != wireKindRequest {
		return wireRequestView{}, errs.ErrTransportProtocol
	}
	requestID := binary.BigEndian.Uint64(data[1:9])
	methodID := MethodID(binary.BigEndian.Uint64(data[9:17]))
	remainingMillis := binary.BigEndian.Uint32(data[17:21])
	nameLength := int(data[21])
	headerSize := wireRequestFixedSize + nameLength
	if requestID == 0 || methodID == 0 || remainingMillis == 0 ||
		nameLength == 0 || headerSize > len(data) ||
		!utf8.Valid(data[22:headerSize]) {
		return wireRequestView{}, errs.ErrTransportProtocol
	}
	return wireRequestView{
		requestID:        requestID,
		methodID:         methodID,
		remainingTimeout: time.Duration(remainingMillis) * time.Millisecond,
		serviceName:      data[22:headerSize],
		payloadOffset:    headerSize,
	}, nil
}

// prependNotify 在业务 payload 前原地写入不含 RequestID 和 Deadline 的短头。
func prependNotify(
	buffer *bufferpool.Buffer,
	methodID MethodID,
	serviceName string,
) error {
	if buffer == nil || methodID == 0 || !validWireName(serviceName) {
		return errs.ErrInvalidArgument
	}
	headerSize := wireNotifyFixedSize + len(serviceName)
	header, ok := buffer.Prepend(headerSize)
	if !ok {
		return errs.ErrRPCEncodeFailed
	}
	header[0] = wireKindNotify
	binary.BigEndian.PutUint64(header[1:9], uint64(methodID))
	header[9] = byte(len(serviceName))
	copy(header[10:], serviceName)
	return nil
}

// parseNotify 返回借用视图，并允许业务 payload 为空。
func parseNotify(data []byte) (wireNotifyView, error) {
	if len(data) < wireNotifyFixedSize || data[0] != wireKindNotify {
		return wireNotifyView{}, errs.ErrTransportProtocol
	}
	methodID := MethodID(binary.BigEndian.Uint64(data[1:9]))
	nameLength := int(data[9])
	headerSize := wireNotifyFixedSize + nameLength
	if methodID == 0 || nameLength == 0 ||
		headerSize > len(data) || !utf8.Valid(data[10:headerSize]) {
		return wireNotifyView{}, errs.ErrTransportProtocol
	}
	return wireNotifyView{
		methodID:      methodID,
		serviceName:   data[10:headerSize],
		payloadOffset: headerSize,
	}, nil
}

// prependResponse 在成功业务 payload 或空错误响应前原地写入无 Kind 的 Response 头。
func prependResponse(
	buffer *bufferpool.Buffer,
	requestID uint64,
	errorCode errs.Code,
) error {
	if buffer == nil || requestID == 0 ||
		(errorCode != errs.CodeOK && len(buffer.Bytes()) != 0) {
		return errs.ErrInvalidArgument
	}
	header, ok := buffer.Prepend(wireResponseFixedSize)
	if !ok {
		return errs.ErrRPCEncodeFailed
	}
	binary.BigEndian.PutUint64(header[0:8], requestID)
	binary.BigEndian.PutUint32(header[8:12], uint32(errorCode))
	return nil
}

// parseResponse 返回响应关联字段和 payload 起点。
func parseResponse(data []byte) (wireResponseView, error) {
	if len(data) < wireResponseFixedSize {
		return wireResponseView{}, errs.ErrTransportProtocol
	}
	requestID := binary.BigEndian.Uint64(data[0:8])
	errorCode := errs.Code(binary.BigEndian.Uint32(data[8:12]))
	if requestID == 0 ||
		(errorCode != errs.CodeOK && len(data) != wireResponseFixedSize) {
		return wireResponseView{}, errs.ErrTransportProtocol
	}
	return wireResponseView{
		requestID:     requestID,
		errorCode:     errorCode,
		payloadOffset: wireResponseFixedSize,
	}, nil
}

// encodeHeartbeat 创建一个只有 Kind 的 Ping 或 Pong。
func encodeHeartbeat(pool *bufferpool.Pool, kind byte) (*bufferpool.Buffer, error) {
	if pool == nil || (kind != wireKindPing && kind != wireKindPong) {
		return nil, errs.ErrInvalidArgument
	}
	buffer := pool.Acquire(wireHeartbeatSize)
	buffer.Bytes()[0] = kind
	return buffer, nil
}

// durationToWireMillis 把剩余时长向上取整为协议 uint32 毫秒。
//
// 向上取整保证正的亚毫秒 Deadline 不会被编码成零；超过约 49.71 天的值不能由协议表达。
func durationToWireMillis(remaining time.Duration) (uint32, bool) {
	if remaining <= 0 {
		return 0, false
	}
	milliseconds := (uint64(remaining) + uint64(time.Millisecond) - 1) /
		uint64(time.Millisecond)
	if milliseconds == 0 || milliseconds > math.MaxUint32 {
		return 0, false
	}
	return uint32(milliseconds), true
}

// validWireName 检查一字节长度名称的共同约束。
func validWireName(value string) bool {
	return len(value) > 0 && len(value) <= wireMaxNameSize && utf8.ValidString(value)
}

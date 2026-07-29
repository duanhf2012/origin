package rpc

import (
	"encoding/binary"
	"math"
	"unicode/utf8"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

const (
	// wireMagic 同时标识 Origin RPC 和第一代不兼容 TCP 线协议。
	wireMagic = "ORP1"

	// wireEnvelopeSize 是 M5 在业务 payload 上限之外允许的固定协议包络余量。
	wireEnvelopeSize = 512

	// 业务包使用一字节 Kind；握手阶段由连接角色决定包类型，因此不携带 Kind。
	wireKindRequest  byte = 1
	wireKindNotify   byte = 2
	wireKindResponse byte = 3
	wireKindPing     byte = 4
	wireKindPong     byte = 5

	// 固定头大小必须与 M13 设计的逐字段布局严格一致。
	wireRequestFixedSize  = 26
	wireNotifyFixedSize   = 10
	wireResponseFixedSize = 13
	wireHeartbeatSize     = 1
	wireHelloFixedSize    = 8
	wireHelloAckFixedSize = 12
	wireServiceFixedSize  = 33

	// NodeID 和 ServiceName 使用 uint8 长度，避免热路径多一个无用字节。
	wireMaxNameSize = math.MaxUint8
)

// wireServiceEntry 是 HelloAck 中一个公开 Service 的不可变契约目录项。
type wireServiceEntry struct {
	name        string
	fingerprint ContractFingerprint
}

// wireHello 是主动连接方第一帧声明的固定 Node 身份。
type wireHello struct {
	sourceNodeID    string
	sourceSessionID string
	targetNodeID    string
	targetSessionID string
}

// wireHelloAck 是被连接方返回的握手结论和公开契约目录。
type wireHelloAck struct {
	statusCode errs.Code
	nodeID     string
	sessionID  string
	services   []wireServiceEntry
}

// wireRequestView 借用入站 Buffer 中的 Request 头字段和业务 payload。
//
// serviceName 和 payload 只在原 Buffer 释放前有效，不能交给其他长期对象保存。
type wireRequestView struct {
	requestID        uint64
	methodID         MethodID
	remainingTimeout timeDuration
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

// timeDuration 使用 uint64 保存线上纳秒值，解析完成后再安全转换为 time.Duration。
//
// 独立别名让 wire.go 不需要在多个字段上重复“线上无符号、进程内有符号”的说明。
type timeDuration uint64

// encodeHello 创建主动方第一帧。握手属于连接冷路径，可以直接按准确大小申请 Buffer。
func encodeHello(
	pool *bufferpool.Pool,
	sourceNodeID string,
	sourceSessionID string,
	targetNodeID string,
	targetSessionID string,
) (*bufferpool.Buffer, error) {
	// NodeID 和 SessionID 都必须能够由 uint8 长度确定性表达。
	if !validWireName(sourceNodeID) ||
		!validWireName(sourceSessionID) ||
		!validWireName(targetNodeID) ||
		!validWireName(targetSessionID) {
		return nil, errs.ErrInvalidArgument
	}
	buffer := pool.Acquire(
		wireHelloFixedSize +
			len(sourceNodeID) +
			len(sourceSessionID) +
			len(targetNodeID) +
			len(targetSessionID),
	)
	data := buffer.Bytes()
	copy(data[:4], wireMagic)
	data[4] = byte(len(sourceNodeID))
	data[5] = byte(len(sourceSessionID))
	data[6] = byte(len(targetNodeID))
	data[7] = byte(len(targetSessionID))
	offset := wireHelloFixedSize
	copy(data[offset:], sourceNodeID)
	offset += len(sourceNodeID)
	copy(data[offset:], sourceSessionID)
	offset += len(sourceSessionID)
	copy(data[offset:], targetNodeID)
	offset += len(targetNodeID)
	copy(data[offset:], targetSessionID)
	return buffer, nil
}

// parseHello 严格解析主动方第一帧，并拒绝尾部数据和非法 UTF-8 身份。
func parseHello(data []byte) (wireHello, error) {
	// 在读取长度字段前先验证最小尺寸和 Magic。
	if len(data) < wireHelloFixedSize || string(data[:4]) != wireMagic {
		return wireHello{}, errs.ErrTransportProtocol
	}
	sourceLength := int(data[4])
	sourceSessionLength := int(data[5])
	targetLength := int(data[6])
	targetSessionLength := int(data[7])
	total := wireHelloFixedSize +
		sourceLength +
		sourceSessionLength +
		targetLength +
		targetSessionLength
	if sourceLength == 0 ||
		sourceSessionLength == 0 ||
		targetLength == 0 ||
		targetSessionLength == 0 ||
		total != len(data) {
		return wireHello{}, errs.ErrTransportProtocol
	}
	offset := wireHelloFixedSize
	sourceBytes := data[offset : offset+sourceLength]
	offset += sourceLength
	sourceSessionBytes := data[offset : offset+sourceSessionLength]
	offset += sourceSessionLength
	targetBytes := data[offset : offset+targetLength]
	offset += targetLength
	targetSessionBytes := data[offset : offset+targetSessionLength]
	if !utf8.Valid(sourceBytes) ||
		!utf8.Valid(sourceSessionBytes) ||
		!utf8.Valid(targetBytes) ||
		!utf8.Valid(targetSessionBytes) {
		return wireHello{}, errs.ErrTransportProtocol
	}
	return wireHello{
		sourceNodeID:    string(sourceBytes),
		sourceSessionID: string(sourceSessionBytes),
		targetNodeID:    string(targetBytes),
		targetSessionID: string(targetSessionBytes),
	}, nil
}

// encodeHelloAck 创建被连接方握手响应；非成功响应不得携带 Service 目录。
func encodeHelloAck(
	pool *bufferpool.Pool,
	statusCode errs.Code,
	nodeID string,
	sessionID string,
	services []wireServiceEntry,
) (*bufferpool.Buffer, error) {
	// 先完成全部长度与目录约束，失败时不申请任何 Buffer。
	if !validWireName(nodeID) ||
		!validWireName(sessionID) ||
		len(services) > math.MaxUint16 ||
		(statusCode != errs.CodeOK && len(services) != 0) {
		return nil, errs.ErrInvalidArgument
	}
	size := wireHelloAckFixedSize + len(nodeID) + len(sessionID)
	for _, service := range services {
		if !validWireName(service.name) {
			return nil, errs.ErrInvalidArgument
		}
		size += wireServiceFixedSize + len(service.name)
		if size > DefaultMaxMessageSize+wireEnvelopeSize {
			return nil, errs.ErrTransportMessageTooLarge
		}
	}

	// 按最终准确大小一次申请并顺序写入，握手目录不建立中间序列化对象。
	buffer := pool.Acquire(size)
	data := buffer.Bytes()
	copy(data[:4], wireMagic)
	binary.BigEndian.PutUint32(data[4:8], uint32(statusCode))
	data[8] = byte(len(nodeID))
	data[9] = byte(len(sessionID))
	binary.BigEndian.PutUint16(data[10:12], uint16(len(services)))
	offset := wireHelloAckFixedSize
	copy(data[offset:], nodeID)
	offset += len(nodeID)
	copy(data[offset:], sessionID)
	offset += len(sessionID)
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
	if len(data) < wireHelloAckFixedSize || string(data[:4]) != wireMagic {
		return wireHelloAck{}, errs.ErrTransportProtocol
	}
	status := errs.Code(binary.BigEndian.Uint32(data[4:8]))
	nodeLength := int(data[8])
	sessionLength := int(data[9])
	serviceCount := int(binary.BigEndian.Uint16(data[10:12]))
	offset := wireHelloAckFixedSize
	if nodeLength == 0 ||
		sessionLength == 0 ||
		nodeLength > len(data)-offset ||
		sessionLength > len(data)-offset-nodeLength {
		return wireHelloAck{}, errs.ErrTransportProtocol
	}
	nodeBytes := data[offset : offset+nodeLength]
	offset += nodeLength
	sessionBytes := data[offset : offset+sessionLength]
	if !utf8.Valid(nodeBytes) || !utf8.Valid(sessionBytes) {
		return wireHelloAck{}, errs.ErrTransportProtocol
	}
	offset += sessionLength
	if status != errs.CodeOK && serviceCount != 0 {
		return wireHelloAck{}, errs.ErrTransportProtocol
	}

	// 目录只在握手冷路径分配一次，随后成为出站会话的只读兼容快照。
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
	return wireHelloAck{
		statusCode: status,
		nodeID:     string(nodeBytes),
		sessionID:  string(sessionBytes),
		services:   services,
	}, nil
}

// prependRequest 在已经编码的业务 payload 前原地写入最小 Request 头。
func prependRequest(
	buffer *bufferpool.Buffer,
	requestID uint64,
	methodID MethodID,
	remaining timeDuration,
	serviceName string,
) error {
	if buffer == nil || requestID == 0 || methodID == 0 || remaining == 0 ||
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
	binary.BigEndian.PutUint64(header[17:25], uint64(remaining))
	header[25] = byte(len(serviceName))
	copy(header[26:], serviceName)
	return nil
}

// parseRequest 返回借用视图；调用方校验成功后再 DiscardPrefix 转移业务 payload。
func parseRequest(data []byte) (wireRequestView, error) {
	if len(data) < wireRequestFixedSize || data[0] != wireKindRequest {
		return wireRequestView{}, errs.ErrTransportProtocol
	}
	requestID := binary.BigEndian.Uint64(data[1:9])
	methodID := MethodID(binary.BigEndian.Uint64(data[9:17]))
	remaining := binary.BigEndian.Uint64(data[17:25])
	nameLength := int(data[25])
	headerSize := wireRequestFixedSize + nameLength
	if requestID == 0 || methodID == 0 || remaining == 0 ||
		remaining > math.MaxInt64 || nameLength == 0 ||
		headerSize > len(data) || !utf8.Valid(data[26:headerSize]) {
		return wireRequestView{}, errs.ErrTransportProtocol
	}
	return wireRequestView{
		requestID:        requestID,
		methodID:         methodID,
		remainingTimeout: timeDuration(remaining),
		serviceName:      data[26:headerSize],
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

// prependResponse 在成功业务 payload 或空错误响应前原地写入 Response 头。
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
	header[0] = wireKindResponse
	binary.BigEndian.PutUint64(header[1:9], requestID)
	binary.BigEndian.PutUint32(header[9:13], uint32(errorCode))
	return nil
}

// parseResponse 返回响应关联字段和 payload 起点。
func parseResponse(data []byte) (wireResponseView, error) {
	if len(data) < wireResponseFixedSize || data[0] != wireKindResponse {
		return wireResponseView{}, errs.ErrTransportProtocol
	}
	requestID := binary.BigEndian.Uint64(data[1:9])
	errorCode := errs.Code(binary.BigEndian.Uint32(data[9:13]))
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

// validWireName 检查一字节长度名称的共同约束。
func validWireName(value string) bool {
	return len(value) > 0 && len(value) <= wireMaxNameSize && utf8.ValidString(value)
}

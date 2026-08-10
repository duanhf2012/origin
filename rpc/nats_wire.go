package rpc

import (
	"encoding/binary"
	"time"
	"unicode/utf8"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

const (
	// PacketType 同时编码 NATS RPC 协议代次和消息分类，不再携带额外 Magic 或 Kind。
	natsPacketRequest  byte = 0x11
	natsPacketNotify   byte = 0x12
	natsPacketResponse byte = 0x13

	// 固定头大小与当前 NATS Wire 布局严格一致。
	natsRequestFixedSize  = 39
	natsNotifyFixedSize   = 18
	natsResponseFixedSize = 29
)

// natsRequestView 借用 NATS Message.Data 中的请求字段和业务 payload。
type natsRequestView struct {
	requestID        uint64
	methodID         MethodID
	remainingTimeout time.Duration
	sourceSessionID  uint64
	targetSessionID  uint64
	sourceNodeID     []byte
	serviceName      []byte
	payloadOffset    int
}

// natsNotifyView 借用不需要响应的通知字段。
type natsNotifyView struct {
	methodID        MethodID
	targetSessionID uint64
	serviceName     []byte
	payloadOffset   int
}

// natsResponseView 借用响应关联、双向会话校验字段和业务 payload。
type natsResponseView struct {
	requestID       uint64
	errorCode       errs.Code
	sourceSessionID uint64
	targetSessionID uint64
	payloadOffset   int
}

// prependNATSRequest 在业务 payload 前原地写入 NATS Request 包头。
func prependNATSRequest(
	buffer *bufferpool.Buffer,
	requestID uint64,
	methodID MethodID,
	remaining time.Duration,
	sourceSessionID uint64,
	targetSessionID uint64,
	sourceNodeID string,
	serviceName string,
) error {
	remainingMillis, ok := durationToWireMillis(remaining)
	if buffer == nil ||
		requestID == 0 ||
		methodID == 0 ||
		!ok ||
		sourceSessionID == 0 ||
		targetSessionID == 0 ||
		!validWireName(sourceNodeID) ||
		!validWireName(serviceName) {
		return errs.ErrInvalidArgument
	}
	headerSize := natsRequestFixedSize + len(sourceNodeID) + len(serviceName)
	header, ok := buffer.Prepend(headerSize)
	if !ok {
		return errs.ErrRPCEncodeFailed
	}

	// 所有整数固定使用网络字节序；名称长度紧邻固定头，payload 不再携带额外长度。
	header[0] = natsPacketRequest
	binary.BigEndian.PutUint64(header[1:9], requestID)
	binary.BigEndian.PutUint64(header[9:17], uint64(methodID))
	binary.BigEndian.PutUint32(header[17:21], remainingMillis)
	binary.BigEndian.PutUint64(header[21:29], sourceSessionID)
	binary.BigEndian.PutUint64(header[29:37], targetSessionID)
	header[37] = byte(len(sourceNodeID))
	header[38] = byte(len(serviceName))
	offset := natsRequestFixedSize
	copy(header[offset:], sourceNodeID)
	offset += len(sourceNodeID)
	copy(header[offset:], serviceName)
	return nil
}

// parseNATSRequest 严格解析 Request，并返回只在原消息存活期间有效的借用视图。
func parseNATSRequest(data []byte) (natsRequestView, error) {
	if len(data) < natsRequestFixedSize || data[0] != natsPacketRequest {
		return natsRequestView{}, errs.ErrTransportProtocol
	}
	requestID := binary.BigEndian.Uint64(data[1:9])
	methodID := MethodID(binary.BigEndian.Uint64(data[9:17]))
	remainingMillis := binary.BigEndian.Uint32(data[17:21])
	sourceSessionID := binary.BigEndian.Uint64(data[21:29])
	targetSessionID := binary.BigEndian.Uint64(data[29:37])
	sourceLength := int(data[37])
	serviceLength := int(data[38])
	headerSize := natsRequestFixedSize + sourceLength + serviceLength
	if requestID == 0 ||
		methodID == 0 ||
		remainingMillis == 0 ||
		sourceSessionID == 0 ||
		targetSessionID == 0 ||
		sourceLength == 0 ||
		serviceLength == 0 ||
		headerSize > len(data) {
		return natsRequestView{}, errs.ErrTransportProtocol
	}
	offset := natsRequestFixedSize
	sourceNodeID := data[offset : offset+sourceLength]
	offset += sourceLength
	serviceName := data[offset:headerSize]
	if !utf8.Valid(sourceNodeID) || !utf8.Valid(serviceName) {
		return natsRequestView{}, errs.ErrTransportProtocol
	}
	return natsRequestView{
		requestID:        requestID,
		methodID:         methodID,
		remainingTimeout: time.Duration(remainingMillis) * time.Millisecond,
		sourceSessionID:  sourceSessionID,
		targetSessionID:  targetSessionID,
		sourceNodeID:     sourceNodeID,
		serviceName:      serviceName,
		payloadOffset:    headerSize,
	}, nil
}

// prependNATSNotify 写入不建立 pending、也不携带来源身份的短通知头。
func prependNATSNotify(
	buffer *bufferpool.Buffer,
	methodID MethodID,
	targetSessionID uint64,
	serviceName string,
) error {
	if buffer == nil ||
		methodID == 0 ||
		targetSessionID == 0 ||
		!validWireName(serviceName) {
		return errs.ErrInvalidArgument
	}
	headerSize := natsNotifyFixedSize + len(serviceName)
	header, ok := buffer.Prepend(headerSize)
	if !ok {
		return errs.ErrRPCEncodeFailed
	}
	header[0] = natsPacketNotify
	binary.BigEndian.PutUint64(header[1:9], uint64(methodID))
	binary.BigEndian.PutUint64(header[9:17], targetSessionID)
	header[17] = byte(len(serviceName))
	copy(header[18:], serviceName)
	return nil
}

// parseNATSNotify 返回只在原消息存活期间有效的通知视图。
func parseNATSNotify(data []byte) (natsNotifyView, error) {
	if len(data) < natsNotifyFixedSize || data[0] != natsPacketNotify {
		return natsNotifyView{}, errs.ErrTransportProtocol
	}
	methodID := MethodID(binary.BigEndian.Uint64(data[1:9]))
	targetSessionID := binary.BigEndian.Uint64(data[9:17])
	serviceLength := int(data[17])
	headerSize := natsNotifyFixedSize + serviceLength
	if methodID == 0 ||
		targetSessionID == 0 ||
		serviceLength == 0 ||
		headerSize > len(data) ||
		!utf8.Valid(data[18:headerSize]) {
		return natsNotifyView{}, errs.ErrTransportProtocol
	}
	return natsNotifyView{
		methodID:        methodID,
		targetSessionID: targetSessionID,
		serviceName:     data[18:headerSize],
		payloadOffset:   headerSize,
	}, nil
}

// prependNATSResponse 写入包含双向 SessionID 的响应头。
func prependNATSResponse(
	buffer *bufferpool.Buffer,
	requestID uint64,
	errorCode errs.Code,
	sourceSessionID uint64,
	targetSessionID uint64,
) error {
	if buffer == nil ||
		requestID == 0 ||
		sourceSessionID == 0 ||
		targetSessionID == 0 ||
		(errorCode != errs.CodeOK && len(buffer.Bytes()) != 0) {
		return errs.ErrInvalidArgument
	}
	header, ok := buffer.Prepend(natsResponseFixedSize)
	if !ok {
		return errs.ErrRPCEncodeFailed
	}
	header[0] = natsPacketResponse
	binary.BigEndian.PutUint64(header[1:9], requestID)
	binary.BigEndian.PutUint32(header[9:13], uint32(errorCode))
	binary.BigEndian.PutUint64(header[13:21], sourceSessionID)
	binary.BigEndian.PutUint64(header[21:29], targetSessionID)
	return nil
}

// parseNATSResponse 严格解析响应；框架错误不得携带业务 payload。
func parseNATSResponse(data []byte) (natsResponseView, error) {
	if len(data) < natsResponseFixedSize || data[0] != natsPacketResponse {
		return natsResponseView{}, errs.ErrTransportProtocol
	}
	requestID := binary.BigEndian.Uint64(data[1:9])
	errorCode := errs.Code(binary.BigEndian.Uint32(data[9:13]))
	sourceSessionID := binary.BigEndian.Uint64(data[13:21])
	targetSessionID := binary.BigEndian.Uint64(data[21:29])
	if requestID == 0 ||
		sourceSessionID == 0 ||
		targetSessionID == 0 ||
		(errorCode != errs.CodeOK && len(data) != natsResponseFixedSize) {
		return natsResponseView{}, errs.ErrTransportProtocol
	}
	return natsResponseView{
		requestID:       requestID,
		errorCode:       errorCode,
		sourceSessionID: sourceSessionID,
		targetSessionID: targetSessionID,
		payloadOffset:   natsResponseFixedSize,
	}, nil
}

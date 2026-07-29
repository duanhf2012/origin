package rpc

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

// TestNATSWireGoldenRequestLayout 锁定 Request 39 字节固定头和字段顺序。
func TestNATSWireGoldenRequestLayout(t *testing.T) {
	pool := bufferpool.NewPool(bufferpool.Options{})
	packet := pool.AcquireWithHeadroom(1, natsRequestFixedSize+2)
	packet.Bytes()[0] = 0xEE
	err := prependNATSRequest(
		packet,
		0x0102030405060708,
		0x1112131415161718,
		9*time.Millisecond,
		0x2122232425262728,
		0x3132333435363738,
		"N",
		"S",
	)
	if err != nil {
		t.Fatalf("prependNATSRequest() error = %v", err)
	}
	data := packet.Bytes()
	if len(data) != natsRequestFixedSize+3 ||
		data[0] != natsPacketRequest ||
		binary.BigEndian.Uint64(data[1:9]) != 0x0102030405060708 ||
		binary.BigEndian.Uint64(data[9:17]) != 0x1112131415161718 ||
		binary.BigEndian.Uint32(data[17:21]) != 9 ||
		binary.BigEndian.Uint64(data[21:29]) != 0x2122232425262728 ||
		binary.BigEndian.Uint64(data[29:37]) != 0x3132333435363738 ||
		data[37] != 1 || data[38] != 1 ||
		data[39] != 'N' || data[40] != 'S' || data[41] != 0xEE {
		t.Fatalf("NATS Request golden = %v", data)
	}
	packet.Release()
}

// TestNATSWireRoundTrip 验证 Request、Notify 和 Response 都允许空业务 payload。
func TestNATSWireRoundTrip(t *testing.T) {
	pool := bufferpool.NewPool(bufferpool.Options{})

	request := pool.AcquireWithHeadroom(0, natsRequestFixedSize+len("gateway-1")+len("PlayerService"))
	if err := prependNATSRequest(
		request,
		7,
		11,
		time.Nanosecond,
		21,
		31,
		"gateway-1",
		"PlayerService",
	); err != nil {
		t.Fatalf("prependNATSRequest() error = %v", err)
	}
	requestView, err := parseNATSRequest(request.Bytes())
	if err != nil {
		t.Fatalf("parseNATSRequest() error = %v", err)
	}
	if requestView.requestID != 7 ||
		requestView.methodID != 11 ||
		requestView.remainingTimeout != time.Millisecond ||
		requestView.sourceSessionID != 21 ||
		requestView.targetSessionID != 31 ||
		string(requestView.sourceNodeID) != "gateway-1" ||
		string(requestView.serviceName) != "PlayerService" ||
		requestView.payloadOffset != len(request.Bytes()) {
		t.Fatalf("Request view = %+v", requestView)
	}
	request.Release()

	notify := pool.AcquireWithHeadroom(0, natsNotifyFixedSize+len("PlayerService"))
	if err := prependNATSNotify(notify, 12, 31, "PlayerService"); err != nil {
		t.Fatalf("prependNATSNotify() error = %v", err)
	}
	notifyView, err := parseNATSNotify(notify.Bytes())
	if err != nil {
		t.Fatalf("parseNATSNotify() error = %v", err)
	}
	if notifyView.methodID != 12 ||
		notifyView.targetSessionID != 31 ||
		string(notifyView.serviceName) != "PlayerService" ||
		notifyView.payloadOffset != len(notify.Bytes()) {
		t.Fatalf("Notify view = %+v", notifyView)
	}
	notify.Release()

	response := pool.AcquireWithHeadroom(2, natsResponseFixedSize)
	copy(response.Bytes(), []byte{8, 9})
	if err := prependNATSResponse(response, 7, errs.CodeOK, 31, 21); err != nil {
		t.Fatalf("prependNATSResponse() error = %v", err)
	}
	responseView, err := parseNATSResponse(response.Bytes())
	if err != nil {
		t.Fatalf("parseNATSResponse() error = %v", err)
	}
	if responseView.requestID != 7 ||
		responseView.errorCode != errs.CodeOK ||
		responseView.sourceSessionID != 31 ||
		responseView.targetSessionID != 21 ||
		!bytes.Equal(response.Bytes()[responseView.payloadOffset:], []byte{8, 9}) {
		t.Fatalf("Response view = %+v", responseView)
	}
	response.Release()
}

// TestNATSWireRejectsMalformedPackets 覆盖零会话、零 Deadline、名称截断和错误响应 payload。
func TestNATSWireRejectsMalformedPackets(t *testing.T) {
	request := make([]byte, natsRequestFixedSize+2)
	request[0] = natsPacketRequest
	binary.BigEndian.PutUint64(request[1:9], 1)
	binary.BigEndian.PutUint64(request[9:17], 2)
	binary.BigEndian.PutUint32(request[17:21], 1)
	binary.BigEndian.PutUint64(request[21:29], 3)
	binary.BigEndian.PutUint64(request[29:37], 4)
	request[37], request[38] = 1, 1
	request[39], request[40] = 'N', 'S'

	cases := [][]byte{
		nil,
		request[:natsRequestFixedSize],
		func() []byte {
			value := append([]byte(nil), request...)
			binary.BigEndian.PutUint32(value[17:21], 0)
			return value
		}(),
		func() []byte {
			value := append([]byte(nil), request...)
			binary.BigEndian.PutUint64(value[21:29], 0)
			return value
		}(),
		func() []byte {
			value := append([]byte(nil), request...)
			binary.BigEndian.PutUint64(value[29:37], 0)
			return value
		}(),
	}
	for index, data := range cases {
		if _, err := parseNATSRequest(data); err == nil {
			t.Fatalf("case %d 非法 Request 被接受", index)
		}
	}

	errorResponse := make([]byte, natsResponseFixedSize+1)
	errorResponse[0] = natsPacketResponse
	binary.BigEndian.PutUint64(errorResponse[1:9], 1)
	binary.BigEndian.PutUint32(errorResponse[9:13], uint32(errs.CodeInternal))
	binary.BigEndian.PutUint64(errorResponse[13:21], 2)
	binary.BigEndian.PutUint64(errorResponse[21:29], 3)
	if _, err := parseNATSResponse(errorResponse); err == nil {
		t.Fatal("错误 Response 携带 payload 仍被接受")
	}
}

// FuzzNATSWireParsers 确保任意 Broker 输入不会触发 panic 或越界。
func FuzzNATSWireParsers(f *testing.F) {
	f.Add([]byte{natsPacketRequest})
	f.Add([]byte{natsPacketNotify})
	f.Add([]byte{natsPacketResponse})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = parseNATSRequest(data)
		_, _ = parseNATSNotify(data)
		_, _ = parseNATSResponse(data)
	})
}

// BenchmarkNATSWireRequestHeader 记录 ORN1 Request 原地编码与无分配借用解析成本。
func BenchmarkNATSWireRequestHeader(b *testing.B) {
	pool := bufferpool.NewPool(bufferpool.Options{})
	sourceNodeID := "gateway-1"
	serviceName := "PlayerService"
	b.ReportAllocs()
	for b.Loop() {
		buffer := pool.AcquireWithHeadroom(
			32,
			natsRequestFixedSize+len(sourceNodeID)+len(serviceName),
		)
		if err := prependNATSRequest(
			buffer,
			1,
			MethodID(2),
			time.Second,
			100,
			200,
			sourceNodeID,
			serviceName,
		); err != nil {
			b.Fatal(err)
		}
		if _, err := parseNATSRequest(buffer.Bytes()); err != nil {
			b.Fatal(err)
		}
		buffer.Release()
	}
}

package rpc

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

// TestWireGoldenBigEndian 锁定 ORP1 不依赖 Go 结构体内存布局的黄金字节。
func TestWireGoldenBigEndian(t *testing.T) {
	pool := bufferpool.NewPool(bufferpool.Options{})
	hello, err := encodeHello(pool, "A", "B")
	if err != nil {
		t.Fatal(err)
	}
	if expected := []byte{'O', 'R', 'P', '1', 1, 1, 'A', 'B'}; !bytes.Equal(
		hello.Bytes(),
		expected,
	) {
		t.Fatalf("Hello golden = %v", hello.Bytes())
	}
	hello.Release()

	request := pool.AcquireWithHeadroom(1, wireRequestFixedSize+1)
	request.Bytes()[0] = 0xEE
	if err := prependRequest(request, 0x0102030405060708, 0x1112131415161718, 9, "S"); err != nil {
		t.Fatal(err)
	}
	data := request.Bytes()
	if data[0] != wireKindRequest ||
		binary.BigEndian.Uint64(data[1:9]) != 0x0102030405060708 ||
		binary.BigEndian.Uint64(data[9:17]) != 0x1112131415161718 ||
		binary.BigEndian.Uint64(data[17:25]) != 9 ||
		data[25] != 1 || data[26] != 'S' || data[27] != 0xEE {
		t.Fatalf("Request golden = %v", data)
	}
	request.Release()
}

// TestWireHelloRoundTrip 锁定 Hello 和 HelloAck 的字段顺序、目录与错误响应。
func TestWireHelloRoundTrip(t *testing.T) {
	pool := bufferpool.NewPool(bufferpool.Options{})
	helloBuffer, err := encodeHello(pool, "gateway-1", "game-1")
	if err != nil {
		t.Fatalf("encodeHello: %v", err)
	}
	hello, err := parseHello(helloBuffer.Bytes())
	if err != nil {
		t.Fatalf("parseHello: %v", err)
	}
	if hello.sourceNodeID != "gateway-1" || hello.targetNodeID != "game-1" {
		t.Fatalf("Hello 身份错误: %+v", hello)
	}
	helloBuffer.Release()

	fingerprint := ContractFingerprint{1, 2, 3}
	ackBuffer, err := encodeHelloAck(
		pool,
		errs.CodeOK,
		"game-1",
		[]wireServiceEntry{{
			name:        "PlayerService",
			fingerprint: fingerprint,
		}},
	)
	if err != nil {
		t.Fatalf("encodeHelloAck: %v", err)
	}
	ack, err := parseHelloAck(ackBuffer.Bytes())
	if err != nil {
		t.Fatalf("parseHelloAck: %v", err)
	}
	if ack.statusCode != errs.CodeOK || ack.nodeID != "game-1" ||
		len(ack.services) != 1 || ack.services[0].name != "PlayerService" ||
		ack.services[0].fingerprint != fingerprint {
		t.Fatalf("HelloAck 内容错误: %+v", ack)
	}
	ackBuffer.Release()
}

// TestWireBusinessPacketsRoundTrip 验证三类业务包使用独立最小头且允许空 payload。
func TestWireBusinessPacketsRoundTrip(t *testing.T) {
	pool := bufferpool.NewPool(bufferpool.Options{})

	request := pool.AcquireWithHeadroom(3, wireRequestFixedSize+len("PlayerService"))
	copy(request.Bytes(), []byte{1, 2, 3})
	if err := prependRequest(
		request,
		9,
		MethodID(11),
		timeDuration((15 * time.Second).Nanoseconds()),
		"PlayerService",
	); err != nil {
		t.Fatalf("prependRequest: %v", err)
	}
	requestView, err := parseRequest(request.Bytes())
	if err != nil {
		t.Fatalf("parseRequest: %v", err)
	}
	if requestView.requestID != 9 || requestView.methodID != 11 ||
		requestView.remainingTimeout != timeDuration((15*time.Second).Nanoseconds()) ||
		string(requestView.serviceName) != "PlayerService" ||
		!bytes.Equal(request.Bytes()[requestView.payloadOffset:], []byte{1, 2, 3}) {
		t.Fatalf("Request 内容错误: %+v", requestView)
	}
	request.Release()

	notify := pool.AcquireWithHeadroom(0, wireNotifyFixedSize+len("PlayerService"))
	if err := prependNotify(notify, MethodID(12), "PlayerService"); err != nil {
		t.Fatalf("prependNotify: %v", err)
	}
	notifyView, err := parseNotify(notify.Bytes())
	if err != nil {
		t.Fatalf("parseNotify: %v", err)
	}
	if notifyView.methodID != 12 ||
		string(notifyView.serviceName) != "PlayerService" ||
		notifyView.payloadOffset != len(notify.Bytes()) {
		t.Fatalf("Notify 内容错误: %+v", notifyView)
	}
	notify.Release()

	response := pool.AcquireWithHeadroom(2, wireResponseFixedSize)
	copy(response.Bytes(), []byte{7, 8})
	if err := prependResponse(response, 9, errs.CodeOK); err != nil {
		t.Fatalf("prependResponse: %v", err)
	}
	responseView, err := parseResponse(response.Bytes())
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if responseView.requestID != 9 || responseView.errorCode != errs.CodeOK ||
		!bytes.Equal(response.Bytes()[responseView.payloadOffset:], []byte{7, 8}) {
		t.Fatalf("Response 内容错误: %+v", responseView)
	}
	response.Release()
}

// TestWireRejectsMalformedPackets 覆盖容易造成越界或含糊解释的协议错误。
func TestWireRejectsMalformedPackets(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte("ORP0"),
		append([]byte("ORP1"), 1),
		{wireKindRequest},
		make([]byte, wireRequestFixedSize),
		{wireKindNotify},
		make([]byte, wireNotifyFixedSize),
		{wireKindResponse},
		make([]byte, wireResponseFixedSize),
	}
	for index, data := range cases {
		switch {
		case len(data) >= 4 && string(data[:4]) == "ORP1":
			if _, err := parseHello(data); err == nil {
				t.Fatalf("case %d 非法 Hello 被接受", index)
			}
		case len(data) > 0 && data[0] == wireKindRequest:
			if _, err := parseRequest(data); err == nil {
				t.Fatalf("case %d 非法 Request 被接受", index)
			}
		case len(data) > 0 && data[0] == wireKindNotify:
			if _, err := parseNotify(data); err == nil {
				t.Fatalf("case %d 非法 Notify 被接受", index)
			}
		case len(data) > 0 && data[0] == wireKindResponse:
			if _, err := parseResponse(data); err == nil {
				t.Fatalf("case %d 非法 Response 被接受", index)
			}
		default:
			if _, err := parseHello(data); err == nil {
				t.Fatalf("case %d 非法握手被接受", index)
			}
		}
	}

	// 错误响应必须为空，重复目录名和尾随数据也不能被模糊接受。
	errorResponse := make([]byte, wireResponseFixedSize+1)
	errorResponse[0] = wireKindResponse
	binary.BigEndian.PutUint64(errorResponse[1:9], 1)
	binary.BigEndian.PutUint32(errorResponse[9:13], uint32(errs.CodeInternal))
	if _, err := parseResponse(errorResponse); err == nil {
		t.Fatal("携带 payload 的错误 Response 被接受")
	}

	pool := bufferpool.NewPool(bufferpool.Options{})
	duplicate, err := encodeHelloAck(
		pool,
		errs.CodeOK,
		"game-1",
		[]wireServiceEntry{
			{name: "S", fingerprint: ContractFingerprint{1}},
			{name: "S", fingerprint: ContractFingerprint{2}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseHelloAck(duplicate.Bytes()); err == nil {
		t.Fatal("重复 ServiceName 的 HelloAck 被接受")
	}
	duplicate.Release()
}

// FuzzWireParsers 确保任意远端输入不会触发 panic 或越界。
func FuzzWireParsers(f *testing.F) {
	f.Add([]byte("ORP1\x01\x01ab"))
	f.Add([]byte{wireKindRequest})
	f.Add([]byte{wireKindNotify})
	f.Add([]byte{wireKindResponse})
	f.Fuzz(func(t *testing.T, data []byte) {
		// 每个解析器都必须只返回成功或稳定错误，不能修改输入和触发 panic。
		_, _ = parseHello(data)
		_, _ = parseHelloAck(data)
		_, _ = parseRequest(data)
		_, _ = parseNotify(data)
		_, _ = parseResponse(data)
	})
}

// BenchmarkWireRequestHeader 记录低延迟热路径固定头的编码与解析分配。
func BenchmarkWireRequestHeader(b *testing.B) {
	pool := bufferpool.NewPool(bufferpool.Options{})
	serviceName := "PlayerService"
	b.ReportAllocs()
	for b.Loop() {
		buffer := pool.AcquireWithHeadroom(
			32,
			wireRequestFixedSize+len(serviceName),
		)
		if err := prependRequest(
			buffer,
			1,
			MethodID(2),
			timeDuration(time.Second),
			serviceName,
		); err != nil {
			b.Fatal(err)
		}
		if _, err := parseRequest(buffer.Bytes()); err != nil {
			b.Fatal(err)
		}
		buffer.Release()
	}
}

package rpc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

// TestTCPWireGoldenBigEndian 锁定 Wire v1 不依赖 Go 结构体内存布局的黄金字节。
func TestTCPWireGoldenBigEndian(t *testing.T) {
	pool := bufferpool.NewPool(bufferpool.Options{})
	hello, err := encodeHello(pool, "A", "B", 0x0102030405060708)
	if err != nil {
		t.Fatal(err)
	}
	expectedHello := []byte{
		tcpWireVersion, 1, 1,
		1, 2, 3, 4, 5, 6, 7, 8,
		'A', 'B',
	}
	if !bytes.Equal(hello.Bytes(), expectedHello) {
		t.Fatalf("Hello golden = %v", hello.Bytes())
	}
	hello.Release()

	request := pool.AcquireWithHeadroom(1, wireRequestFixedSize+1)
	request.Bytes()[0] = 0xEE
	if err := prependRequest(
		request,
		0x0102030405060708,
		0x1112131415161718,
		9*time.Millisecond,
		"S",
	); err != nil {
		t.Fatal(err)
	}
	data := request.Bytes()
	if data[0] != wireKindRequest ||
		binary.BigEndian.Uint64(data[1:9]) != 0x0102030405060708 ||
		binary.BigEndian.Uint64(data[9:17]) != 0x1112131415161718 ||
		binary.BigEndian.Uint32(data[17:21]) != 9 ||
		data[21] != 1 || data[22] != 'S' || data[23] != 0xEE {
		t.Fatalf("Request golden = %v", data)
	}
	request.Release()
}

// TestTCPWireHelloRoundTrip 锁定精简 Hello、HelloAck 及目录顺序。
func TestTCPWireHelloRoundTrip(t *testing.T) {
	pool := bufferpool.NewPool(bufferpool.Options{})
	helloBuffer, err := encodeHello(pool, "gateway-1", "game-1", 27)
	if err != nil {
		t.Fatalf("encodeHello: %v", err)
	}
	hello, err := parseHello(helloBuffer.Bytes())
	if err != nil {
		t.Fatalf("parseHello: %v", err)
	}
	if hello.sourceNodeID != "gateway-1" ||
		hello.targetNodeID != "game-1" ||
		hello.targetSessionID != 27 {
		t.Fatalf("Hello 身份错误: %+v", hello)
	}
	helloBuffer.Release()

	fingerprint := ContractFingerprint{1, 2, 3}
	ackBuffer, err := encodeHelloAck(
		pool,
		errs.CodeOK,
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
	if ack.statusCode != errs.CodeOK ||
		len(ack.services) != 1 || ack.services[0].name != "PlayerService" ||
		ack.services[0].fingerprint != fingerprint {
		t.Fatalf("HelloAck 内容错误: %+v", ack)
	}
	ackBuffer.Release()
}

// TestTCPWireBusinessPacketsRoundTrip 验证三类业务包使用最小头且允许空 payload。
func TestTCPWireBusinessPacketsRoundTrip(t *testing.T) {
	pool := bufferpool.NewPool(bufferpool.Options{})

	request := pool.AcquireWithHeadroom(3, wireRequestFixedSize+len("PlayerService"))
	copy(request.Bytes(), []byte{1, 2, 3})
	if err := prependRequest(
		request,
		9,
		MethodID(11),
		15*time.Second,
		"PlayerService",
	); err != nil {
		t.Fatalf("prependRequest: %v", err)
	}
	requestView, err := parseRequest(request.Bytes())
	if err != nil {
		t.Fatalf("parseRequest: %v", err)
	}
	if requestView.requestID != 9 || requestView.methodID != 11 ||
		requestView.remainingTimeout != 15*time.Second ||
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

// TestTCPWireDeadlineMillis 验证向上取整、零值拒绝和 uint32 毫秒边界。
func TestTCPWireDeadlineMillis(t *testing.T) {
	cases := []struct {
		remaining time.Duration
		want      uint32
		ok        bool
	}{
		{remaining: time.Nanosecond, want: 1, ok: true},
		{remaining: time.Millisecond, want: 1, ok: true},
		{remaining: time.Millisecond + 1, want: 2, ok: true},
		{remaining: time.Duration(math.MaxUint32) * time.Millisecond, want: math.MaxUint32, ok: true},
		{remaining: 0, ok: false},
		{remaining: -time.Nanosecond, ok: false},
		{remaining: time.Duration(math.MaxUint32)*time.Millisecond + 1, ok: false},
	}
	for _, test := range cases {
		got, ok := durationToWireMillis(test.remaining)
		if got != test.want || ok != test.ok {
			t.Fatalf(
				"durationToWireMillis(%s) = (%d, %v)，期望 (%d, %v)",
				test.remaining,
				got,
				ok,
				test.want,
				test.ok,
			)
		}
	}
}

// TestTCPWireHeartbeatEncoding 固定 Ping/Pong 单字节帧，并确保无效 Kind 不申请 Buffer。
func TestTCPWireHeartbeatEncoding(t *testing.T) {
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	for _, kind := range []byte{wireKindPing, wireKindPong} {
		packet, err := encodeHeartbeat(pool, kind)
		if err != nil {
			t.Fatalf("encodeHeartbeat(%d) error = %v", kind, err)
		}
		if data := packet.Bytes(); len(data) != wireHeartbeatSize || data[0] != kind {
			t.Fatalf("encodeHeartbeat(%d) = %v", kind, data)
		}
		packet.Release()
	}
	if packet, err := encodeHeartbeat(pool, 0xff); packet != nil ||
		!errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("encodeHeartbeat(invalid) = %v, %v", packet, err)
	}
	if stats := pool.Stats(); stats.InUseBuffers != 0 {
		t.Fatalf("heartbeat pool stats = %+v", stats)
	}
}

// TestTCPWireRejectsMalformedPackets 覆盖版本、零会话、截断、方向和尾部错误。
func TestTCPWireRejectsMalformedPackets(t *testing.T) {
	invalidHello := make([]byte, wireHelloFixedSize+2)
	invalidHello[0] = tcpWireVersion
	invalidHello[1] = 1
	invalidHello[2] = 1
	invalidHello[11] = 'A'
	invalidHello[12] = 'B'

	cases := [][]byte{
		nil,
		{tcpWireVersion + 1},
		invalidHello,
		{wireKindRequest},
		make([]byte, wireRequestFixedSize),
		{wireKindNotify},
		make([]byte, wireNotifyFixedSize),
		make([]byte, wireResponseFixedSize),
	}
	for index, data := range cases {
		_, helloErr := parseHello(data)
		_, requestErr := parseRequest(data)
		_, notifyErr := parseNotify(data)
		_, responseErr := parseResponse(data)
		if helloErr == nil || requestErr == nil || notifyErr == nil || responseErr == nil {
			t.Fatalf(
				"case %d 被某个错误方向解析器接受: hello=%v request=%v notify=%v response=%v",
				index,
				helloErr,
				requestErr,
				notifyErr,
				responseErr,
			)
		}
	}

	// 错误响应必须为空，重复目录名也不能被模糊接受。
	errorResponse := make([]byte, wireResponseFixedSize+1)
	binary.BigEndian.PutUint64(errorResponse[0:8], 1)
	binary.BigEndian.PutUint32(errorResponse[8:12], uint32(errs.CodeInternal))
	if _, err := parseResponse(errorResponse); err == nil {
		t.Fatal("携带 payload 的错误 Response 被接受")
	}

	pool := bufferpool.NewPool(bufferpool.Options{})
	duplicate, err := encodeHelloAck(
		pool,
		errs.CodeOK,
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

// FuzzTCPWireParsers 确保任意远端输入不会触发 panic 或越界。
func FuzzTCPWireParsers(f *testing.F) {
	f.Add([]byte{tcpWireVersion, 1, 1, 0, 0, 0, 0, 0, 0, 0, 1, 'A', 'B'})
	f.Add([]byte{wireKindRequest})
	f.Add([]byte{wireKindNotify})
	f.Add(make([]byte, wireResponseFixedSize))
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
			time.Second,
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

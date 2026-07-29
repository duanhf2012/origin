package rpc

import (
	"bytes"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/natsnet"
)

func TestNATSResponseCopiesOnlyMatchedSuccessPayload(t *testing.T) {
	t.Parallel()

	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	owner := &Runtime{
		sessionID: 201,
		pool:      pool,
	}
	runtime := &natsRuntime{
		owner:   owner,
		config:  Config{MaxPayloadSize: DefaultMaxPayloadSize},
		pending: newNATSPendingTable(8),
	}

	payload := []byte("business-result")
	completed := false
	if err := runtime.pending.reserve(1, 101, func(response *Buffer, err error) {
		completed = true
		var actual []byte
		if response != nil {
			actual = response.Bytes()
		}
		if err != nil || response == nil ||
			!bytes.Equal(actual, payload) {
			t.Errorf("success completion = %q, %v", actual, err)
			return
		}
		if got := pool.Stats().InUseBuffers; got != 1 {
			t.Errorf("matched success in-use buffers = %d, want 1", got)
		}
		response.Release()
	}); err != nil {
		t.Fatal(err)
	}
	runtime.handleResponse(natsnet.Message{
		Data: encodedNATSResponse(t, 1, errs.CodeOK, 101, 201, payload),
	})
	if !completed || pool.Stats().InUseBuffers != 0 {
		t.Fatalf("success completed=%v stats=%+v", completed, pool.Stats())
	}

	// 未知 RequestID 与框架错误都不得为业务 payload 取得响应 Buffer。
	runtime.handleResponse(natsnet.Message{
		Data: encodedNATSResponse(t, 999, errs.CodeOK, 101, 201, payload),
	})
	if got := pool.Stats().InUseBuffers; got != 0 {
		t.Fatalf("unknown response allocated %d buffers", got)
	}

	errorCompleted := false
	if err := runtime.pending.reserve(2, 101, func(response *Buffer, err error) {
		errorCompleted = true
		if response != nil || !errs.IsCode(err, errs.CodeInvalidArgument) {
			t.Errorf("error completion = %v, %v", response, err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	runtime.handleResponse(natsnet.Message{
		Data: encodedNATSResponse(
			t,
			2,
			errs.CodeInvalidArgument,
			101,
			201,
			nil,
		),
	})
	if !errorCompleted || pool.Stats().InUseBuffers != 0 {
		t.Fatalf("error completed=%v stats=%+v", errorCompleted, pool.Stats())
	}
}

// encodedNATSResponse 使用独立 Pool 构造线协议数据，避免污染被测 Runtime 的统计。
func encodedNATSResponse(
	t testing.TB,
	requestID uint64,
	code errs.Code,
	sourceSessionID uint64,
	targetSessionID uint64,
	payload []byte,
) []byte {
	t.Helper()
	encodingPool := bufferpool.NewPool(bufferpool.Options{})
	buffer := encodingPool.AcquireWithHeadroom(
		len(payload),
		natsResponseFixedSize,
	)
	copy(buffer.Bytes(), payload)
	if err := prependNATSResponse(
		buffer,
		requestID,
		code,
		sourceSessionID,
		targetSessionID,
	); err != nil {
		buffer.Release()
		t.Fatalf("prependNATSResponse() error = %v", err)
	}
	result := append([]byte(nil), buffer.Bytes()...)
	buffer.Release()
	return result
}

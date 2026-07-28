package rpcfixture

import "testing"

// FuzzGeneratedCustomCodecDecoders 把任意载荷交给真实生成的自定义请求和响应解码器。
//
// 目标是锁定伪造长度、容器数量、指针 presence 和自定义 time.Time payload 只能返回固定
// 解码错误，不能越界、超量分配或 panic。
func FuzzGeneratedCustomCodecDecoders(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Add(make([]byte, 22))
	f.Fuzz(func(t *testing.T, payload []byte) {
		_, _, _ = decodePlayerRPCRoundTripTimeRequest(payload)
		_, _ = decodePlayerRPCRoundTripTimeResponse(payload)
		_, _ = decodePlayerRPCRoundTripPackedIDRequest(payload)
		_, _ = decodePlayerRPCRoundTripPackedIDResponse(payload)
		_, _ = decodePlayerRPCRoundTripBlobRequest(payload)
		_, _ = decodePlayerRPCRoundTripBlobResponse(payload)
	})
}

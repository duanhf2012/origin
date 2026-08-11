package tcpnet

import "github.com/duanhf2012/origin/v3/internal/lengthframe"

// encodeFrameLength 把已经校验的 payload 长度写入栈上或队列项帧头。
func encodeFrameLength(header *[4]byte, payloadLength int, options FrameOptions) int {
	return lengthframe.Encode(header, payloadLength, lengthframe.Options{
		Size:      options.LengthFieldSize,
		ByteOrder: lengthframe.ByteOrder(options.ByteOrder),
	})
}

// decodeFrameLength 从网络字节序的完整长度字段中读取无符号 payload 长度。
func decodeFrameLength(header []byte, options FrameOptions) uint64 {
	return lengthframe.Decode(header, lengthframe.Options{
		Size:      options.LengthFieldSize,
		ByteOrder: lengthframe.ByteOrder(options.ByteOrder),
	})
}

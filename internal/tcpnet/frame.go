package tcpnet

import "encoding/binary"

// encodeFrameLength 把已经校验的 payload 长度写入栈上或队列项帧头。
func encodeFrameLength(header *[4]byte, payloadLength int, options FrameOptions) int {
	// 每种长度字段直接写入最终帧头，不创建临时切片或接口。
	switch options.LengthFieldSize {
	case 1:
		header[0] = byte(payloadLength)
	case 2:
		binary.BigEndian.PutUint16(header[:2], uint16(payloadLength))
	case 4:
		binary.BigEndian.PutUint32(header[:4], uint32(payloadLength))
	default:
		// ConnectionOptions 已经拒绝其他宽度，到达这里表示框架内部不变量被破坏。
		panic("tcpnet: 未校验的长度字段宽度")
	}
	return options.LengthFieldSize
}

// decodeFrameLength 从网络字节序的完整长度字段中读取无符号 payload 长度。
func decodeFrameLength(header []byte, options FrameOptions) uint64 {
	// ReadLoop 保证 header 已经完整读取，分支只处理确定宽度。
	switch options.LengthFieldSize {
	case 1:
		return uint64(header[0])
	case 2:
		return uint64(binary.BigEndian.Uint16(header[:2]))
	case 4:
		return uint64(binary.BigEndian.Uint32(header[:4]))
	default:
		// 只有内部错误才能绕过 Options 校验。
		panic("tcpnet: 未校验的长度字段宽度")
	}
}

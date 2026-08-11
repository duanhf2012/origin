// Package lengthframe 提供 TCP、KCP 等字节流传输共用的无符号长度字段算法。
//
// 该包只处理已经由调用方校验的 1、2、4 字节长度字段，不分配内存，也不负责
// 消息大小、I/O 或协议错误分类。
package lengthframe

import "encoding/binary"

// ByteOrder 是长度字段使用的固定端序。
type ByteOrder uint8

const (
	// BigEndian 使用网络字节序。
	BigEndian ByteOrder = iota + 1
	// LittleEndian 支持使用小端长度字段的客户端协议。
	LittleEndian
)

// Options 配置长度字段宽度和端序。
type Options struct {
	// Size 只允许一、二或四字节；调用方必须在进入热路径前完成校验。
	Size int
	// ByteOrder 对二、四字节生效；一字节没有端序差异。
	ByteOrder ByteOrder
}

// Encode 把已经校验的 Payload 长度写入固定帧头，并返回有效头长度。
func Encode(header *[4]byte, payloadLength int, options Options) int {
	switch options.Size {
	case 1:
		header[0] = byte(payloadLength)
	case 2:
		if options.ByteOrder == LittleEndian {
			binary.LittleEndian.PutUint16(header[:2], uint16(payloadLength))
		} else {
			binary.BigEndian.PutUint16(header[:2], uint16(payloadLength))
		}
	case 4:
		if options.ByteOrder == LittleEndian {
			binary.LittleEndian.PutUint32(header[:4], uint32(payloadLength))
		} else {
			binary.BigEndian.PutUint32(header[:4], uint32(payloadLength))
		}
	default:
		panic("lengthframe: 未校验的长度字段宽度")
	}
	return options.Size
}

// Decode 从完整长度字段读取无符号 Payload 长度。
func Decode(header []byte, options Options) uint64 {
	switch options.Size {
	case 1:
		return uint64(header[0])
	case 2:
		if options.ByteOrder == LittleEndian {
			return uint64(binary.LittleEndian.Uint16(header[:2]))
		}
		return uint64(binary.BigEndian.Uint16(header[:2]))
	case 4:
		if options.ByteOrder == LittleEndian {
			return uint64(binary.LittleEndian.Uint32(header[:4]))
		}
		return uint64(binary.BigEndian.Uint32(header[:4]))
	default:
		panic("lengthframe: 未校验的长度字段宽度")
	}
}

package network

import (
	"encoding/binary"
	"errors"
	"io"
)

// --------------
// | len | data |
// --------------
type MsgParser struct {
	LenMsgLen    int
	MinMsgLen    uint32
	MaxMsgLen    uint32
	LittleEndian bool

	INetMempool
}


func (p *MsgParser) init(){
	p.INetMempool = NewMemAreaPool()

	for i:=1;i<=4;i*=2 {
		max := uint32(1<<(i*8)-1)
		if p.MaxMsgLen <= max  {
			p.LenMsgLen = i
			return
		}
	}

	panic("MaxMsgLen value must be less than 4294967295")
}

// goroutine safe
func (p *MsgParser) Read(conn *TCPConn) ([]byte, error) {
	var b [4]byte
	bufMsgLen := b[:p.LenMsgLen]

	// read len
	if _, err := io.ReadFull(conn, bufMsgLen); err != nil {
		return nil, err
	}

	// parse len
	var msgLen uint32
	switch p.LenMsgLen {
	case 1:
		msgLen = uint32(bufMsgLen[0])
	case 2:
		if p.LittleEndian {
			msgLen = uint32(binary.LittleEndian.Uint16(bufMsgLen))
		} else {
			msgLen = uint32(binary.BigEndian.Uint16(bufMsgLen))
		}
	case 4:
		if p.LittleEndian {
			msgLen = binary.LittleEndian.Uint32(bufMsgLen)
		} else {
			msgLen = binary.BigEndian.Uint32(bufMsgLen)
		}
	}

	// check len
	if msgLen > p.MaxMsgLen {
		return nil, errors.New("message too long")
	} else if msgLen < p.MinMsgLen {
		return nil, errors.New("message too short")
	}
	
	// data
	msgData := p.MakeByteSlice(int(msgLen))
	if _, err := io.ReadFull(conn, msgData[:msgLen]); err != nil {
		p.ReleaseByteSlice(msgData)
		return nil, err
	}

	return msgData[:msgLen], nil
}

// goroutine safe
func (p *MsgParser) Write(conn *TCPConn, args ...[]byte) error {
	// get len
	var msgLen uint32
	for i := 0; i < len(args); i++ {
		msgLen += uint32(len(args[i]))
	}

	// check len
	if msgLen > p.MaxMsgLen {
		return errors.New("message too long")
	} else if msgLen < p.MinMsgLen {
		return errors.New("message too short")
	}

	//msg := make([]byte, uint32(p.lenMsgLen)+msgLen)
	msg := p.MakeByteSlice(p.LenMsgLen+int(msgLen))
	// write len
	switch p.LenMsgLen {
	case 1:
		msg[0] = byte(msgLen)
	case 2:
		if p.LittleEndian {
			binary.LittleEndian.PutUint16(msg, uint16(msgLen))
		} else {
			binary.BigEndian.PutUint16(msg, uint16(msgLen))
		}
	case 4:
		if p.LittleEndian {
			binary.LittleEndian.PutUint32(msg, msgLen)
		} else {
			binary.BigEndian.PutUint32(msg, msgLen)
		}
	}

	// write data
	l := p.LenMsgLen
	for i := 0; i < len(args); i++ {
		copy(msg[l:], args[i])
		l += len(args[i])
	}

	conn.Write(msg)

	return nil
}

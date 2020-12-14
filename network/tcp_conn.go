package network

import (
	"fmt"
	"github.com/duanhf2012/origin/log"
	"net"
	"sync"
	"time"
)

type ConnSet map[net.Conn]struct{}

type TCPConn struct {
	sync.Mutex
	conn      net.Conn
	writeChan chan []byte
	closeFlag bool
	msgParser *MsgParser
}

func freeChannel(conn *TCPConn){
	for;len(conn.writeChan)>0;{
		byteBuff := <- conn.writeChan
		if byteBuff != nil {
			conn.ReleaseReadMsg(byteBuff)
		}
	}
}

func newTCPConn(conn net.Conn, pendingWriteNum int, msgParser *MsgParser) *TCPConn {
	tcpConn := new(TCPConn)
	tcpConn.conn = conn
	tcpConn.writeChan = make(chan []byte, pendingWriteNum)
	tcpConn.msgParser = msgParser
	go func() {
		for b := range tcpConn.writeChan {
			if b == nil {
				break
			}
			_, err := conn.Write(b)
			tcpConn.msgParser.ReleaseByteSlice(b)

			if err != nil {
				break
			}
		}
		conn.Close()
		tcpConn.Lock()
		freeChannel(tcpConn)
		tcpConn.closeFlag = true
		tcpConn.Unlock()
	}()

	return tcpConn
}

func (tcpConn *TCPConn) doDestroy() {
	tcpConn.conn.(*net.TCPConn).SetLinger(0)
	tcpConn.conn.Close()

	if !tcpConn.closeFlag {
		close(tcpConn.writeChan)
		tcpConn.closeFlag = true
	}
}

func (tcpConn *TCPConn) Destroy() {
	tcpConn.Lock()
	defer tcpConn.Unlock()

	tcpConn.doDestroy()
}

func (tcpConn *TCPConn) Close() {
	tcpConn.Lock()
	defer tcpConn.Unlock()
	if tcpConn.closeFlag {
		return
	}

	tcpConn.doWrite(nil)
	tcpConn.closeFlag = true
}

func (tcpConn *TCPConn) GetRemoteIp() string {
	return tcpConn.conn.RemoteAddr().String()
}

func (tcpConn *TCPConn) doWrite(b []byte) {
	if len(tcpConn.writeChan) == cap(tcpConn.writeChan) {
		tcpConn.ReleaseReadMsg(b)
		log.Error("close conn: channel full")
		tcpConn.doDestroy()
		return
	}

	tcpConn.writeChan <- b
}

// b must not be modified by the others goroutines
func (tcpConn *TCPConn) Write(b []byte) {
	tcpConn.Lock()
	defer tcpConn.Unlock()
	if tcpConn.closeFlag || b == nil {
		tcpConn.ReleaseReadMsg(b)
		return
	}

	tcpConn.doWrite(b)
}

func (tcpConn *TCPConn) Read(b []byte) (int, error) {
	return tcpConn.conn.Read(b)
}

func (tcpConn *TCPConn) LocalAddr() net.Addr {
	return tcpConn.conn.LocalAddr()
}

func (tcpConn *TCPConn) RemoteAddr() net.Addr {
	return tcpConn.conn.RemoteAddr()
}

func (tcpConn *TCPConn) ReadMsg() ([]byte, error) {
	return tcpConn.msgParser.Read(tcpConn)
}

func (tcpConn *TCPConn) ReleaseReadMsg(byteBuff []byte){
	tcpConn.msgParser.ReleaseByteSlice(byteBuff)
}

func (tcpConn *TCPConn) WriteMsg(args ...[]byte) error {
	if tcpConn.closeFlag == true {
		return fmt.Errorf("conn is close")
	}
	return tcpConn.msgParser.Write(tcpConn, args...)
}

func (tcpConn *TCPConn) IsConnected() bool {
	return tcpConn.closeFlag == false
}

func (tcpConn *TCPConn) SetReadDeadline(d time.Duration)  {
	tcpConn.conn.SetReadDeadline(time.Now().Add(d))
}

func (tcpConn *TCPConn) SetWriteDeadline(d time.Duration)  {
	tcpConn.conn.SetWriteDeadline(time.Now().Add(d))
}

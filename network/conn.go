package network

import (
	"errors"
	"github.com/duanhf2012/origin/v2/log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Conn interface {
	ReadMsg() ([]byte, error)
	WriteMsg(args ...[]byte) error
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Close()
	Destroy()
	ReleaseReadMsg(byteBuff []byte)
}

type ConnSet map[net.Conn]struct{}

type NetConn struct {
	sync.Mutex
	conn      net.Conn
	writeChan chan []byte
	closeFlag int32
	msgParser *MsgParser
}

func freeChannel(conn *NetConn) {
	for len(conn.writeChan) > 0 {
		byteBuff := <-conn.writeChan
		if byteBuff != nil {
			conn.ReleaseReadMsg(byteBuff)
		}
	}
}

func newNetConn(conn net.Conn, pendingWriteNum int, msgParser *MsgParser, writeDeadline time.Duration) *NetConn {
	netConn := new(NetConn)
	netConn.conn = conn
	netConn.writeChan = make(chan []byte, pendingWriteNum)
	netConn.msgParser = msgParser
	go func() {
		for b := range netConn.writeChan {
			if b == nil {
				break
			}

			conn.SetWriteDeadline(time.Now().Add(writeDeadline))
			_, err := conn.Write(b)
			netConn.msgParser.ReleaseBytes(b)

			if err != nil {
				break
			}
		}
		conn.Close()
		netConn.Lock()
		freeChannel(netConn)
		atomic.StoreInt32(&netConn.closeFlag, 1)
		netConn.Unlock()
	}()

	return netConn
}

func (netConn *NetConn) doDestroy() {
	netConn.conn.Close()

	if atomic.LoadInt32(&netConn.closeFlag) == 0 {
		close(netConn.writeChan)
		atomic.StoreInt32(&netConn.closeFlag, 1)
	}
}

func (netConn *NetConn) Destroy() {
	netConn.Lock()
	defer netConn.Unlock()

	netConn.doDestroy()
}

func (netConn *NetConn) Close() {
	netConn.Lock()
	defer netConn.Unlock()
	if atomic.LoadInt32(&netConn.closeFlag) == 1 {
		return
	}

	netConn.doWrite(nil)
	atomic.StoreInt32(&netConn.closeFlag, 1)
}

func (netConn *NetConn) GetRemoteIp() string {
	return netConn.conn.RemoteAddr().String()
}

func (netConn *NetConn) doWrite(b []byte) error {
	if len(netConn.writeChan) == cap(netConn.writeChan) {
		netConn.ReleaseReadMsg(b)
		log.Error("close conn: channel full")
		netConn.doDestroy()
		return errors.New("close conn: channel full")
	}

	netConn.writeChan <- b
	return nil
}

// b must not be modified by the others goroutines
func (netConn *NetConn) Write(b []byte) error {
	netConn.Lock()
	defer netConn.Unlock()
	if atomic.LoadInt32(&netConn.closeFlag) == 1 || b == nil {
		netConn.ReleaseReadMsg(b)
		return errors.New("conn is close")
	}

	return netConn.doWrite(b)
}

func (netConn *NetConn) Read(b []byte) (int, error) {
	return netConn.conn.Read(b)
}

func (netConn *NetConn) LocalAddr() net.Addr {
	return netConn.conn.LocalAddr()
}

func (netConn *NetConn) RemoteAddr() net.Addr {
	return netConn.conn.RemoteAddr()
}

func (netConn *NetConn) ReadMsg() ([]byte, error) {
	return netConn.msgParser.Read(netConn)
}

func (netConn *NetConn) GetRecyclerReaderBytes() func(data []byte) {
	return netConn.msgParser.GetRecyclerReaderBytes()
}

func (netConn *NetConn) ReleaseReadMsg(byteBuff []byte) {
	netConn.msgParser.ReleaseBytes(byteBuff)
}

func (netConn *NetConn) WriteMsg(args ...[]byte) error {
	if atomic.LoadInt32(&netConn.closeFlag) == 1 {
		return errors.New("conn is close")
	}
	return netConn.msgParser.Write(netConn.conn, args...)
}

func (netConn *NetConn) WriteRawMsg(args []byte) error {
	if atomic.LoadInt32(&netConn.closeFlag) == 1 {
		return errors.New("conn is close")
	}

	return netConn.Write(args)
}

func (netConn *NetConn) IsConnected() bool {
	return atomic.LoadInt32(&netConn.closeFlag) == 0
}

func (netConn *NetConn) SetReadDeadline(d time.Duration) {
	netConn.conn.SetReadDeadline(time.Now().Add(d))
}

func (netConn *NetConn) SetWriteDeadline(d time.Duration) {
	netConn.conn.SetWriteDeadline(time.Now().Add(d))
}

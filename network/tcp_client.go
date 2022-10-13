package network

import (
	"github.com/duanhf2012/origin/log"
	"net"
	"sync"
	"time"
)

type TCPClient struct {
	sync.Mutex
	Addr            string
	ConnNum         int
	ConnectInterval time.Duration
	PendingWriteNum int
	ReadDeadline    time.Duration
	WriteDeadline 	time.Duration
	AutoReconnect   bool
	NewAgent        func(*TCPConn) Agent
	cons            ConnSet
	wg              sync.WaitGroup
	closeFlag       bool

	// msg parser
	LenMsgLen    int
	MinMsgLen    uint32
	MaxMsgLen    uint32
	LittleEndian bool
	msgParser    *MsgParser
}

func (client *TCPClient) Start() {
	client.init()

	for i := 0; i < client.ConnNum; i++ {
		client.wg.Add(1)
		go client.connect()
	}
}

func (client *TCPClient) init() {
	client.Lock()
	defer client.Unlock()

	if client.ConnNum <= 0 {
		client.ConnNum = 1
		log.SRelease("invalid ConnNum, reset to ", client.ConnNum)
	}
	if client.ConnectInterval <= 0 {
		client.ConnectInterval = 3 * time.Second
		log.SRelease("invalid ConnectInterval, reset to ", client.ConnectInterval)
	}
	if client.PendingWriteNum <= 0 {
		client.PendingWriteNum = 1000
		log.SRelease("invalid PendingWriteNum, reset to ", client.PendingWriteNum)
	}
	if client.ReadDeadline == 0 {
		client.ReadDeadline = 15*time.Second
		log.SRelease("invalid ReadDeadline, reset to ", client.ReadDeadline,"s")
	}
	if client.WriteDeadline == 0 {
		client.WriteDeadline = 15*time.Second
		log.SRelease("invalid WriteDeadline, reset to ", client.WriteDeadline,"s")
	}
	if client.NewAgent == nil {
		log.SFatal("NewAgent must not be nil")
	}
	if client.cons != nil {
		log.SFatal("client is running")
	}

	client.cons = make(ConnSet)
	client.closeFlag = false

	// msg parser
	msgParser := NewMsgParser()
	msgParser.SetMsgLen(client.LenMsgLen, client.MinMsgLen, client.MaxMsgLen)
	msgParser.SetByteOrder(client.LittleEndian)
	client.msgParser = msgParser
}

func (client *TCPClient) dial() net.Conn {
	for {
		conn, err := net.Dial("tcp", client.Addr)
		if client.closeFlag {
			return conn
		} else if err == nil && conn != nil {
			conn.(*net.TCPConn).SetNoDelay(true)
			return conn
		}

		log.SWarning("connect to ",client.Addr," error:", err.Error())
		time.Sleep(client.ConnectInterval)
		continue
	}
}

func (client *TCPClient) connect() {
	defer client.wg.Done()

reconnect:
	conn := client.dial()
	if conn == nil {
		return
	}
	
	client.Lock()
	if client.closeFlag {
		client.Unlock()
		conn.Close()
		return
	}
	client.cons[conn] = struct{}{}
	client.Unlock()

	tcpConn := newTCPConn(conn, client.PendingWriteNum, client.msgParser,client.WriteDeadline)
	agent := client.NewAgent(tcpConn)
	agent.Run()

	// cleanup
	tcpConn.Close()
	client.Lock()
	delete(client.cons, conn)
	client.Unlock()
	agent.OnClose()

	if client.AutoReconnect {
		time.Sleep(client.ConnectInterval)
		goto reconnect
	}
}

func (client *TCPClient) Close(waitDone bool) {
	client.Lock()
	client.closeFlag = true
	for conn := range client.cons {
		conn.Close()
	}
	client.cons = nil
	client.Unlock()

	if waitDone == true{
		client.wg.Wait()
	}
}


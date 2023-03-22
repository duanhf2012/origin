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
	MsgParser
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
		log.SRelease("invalid ReadDeadline, reset to ", int64(client.ReadDeadline.Seconds()),"s")
	}
	if client.WriteDeadline == 0 {
		client.WriteDeadline = 15*time.Second
		log.SRelease("invalid WriteDeadline, reset to ", int64(client.WriteDeadline.Seconds()),"s")
	}
	if client.NewAgent == nil {
		log.SFatal("NewAgent must not be nil")
	}
	if client.cons != nil {
		log.SFatal("client is running")
	}

	if client.MinMsgLen == 0 {
		client.MinMsgLen = Default_MinMsgLen
	}
	if client.MaxMsgLen == 0 {
		client.MaxMsgLen = Default_MaxMsgLen
	}
	if client.LenMsgLen ==0 {
		client.LenMsgLen = Default_LenMsgLen
	}
	maxMsgLen := client.MsgParser.getMaxMsgLen(client.LenMsgLen)
	if client.MaxMsgLen > maxMsgLen {
		client.MaxMsgLen = maxMsgLen
		log.SRelease("invalid MaxMsgLen, reset to ", maxMsgLen)
	}

	client.cons = make(ConnSet)
	client.closeFlag = false
	client.MsgParser.init()
}

func (client *TCPClient) GetCloseFlag() bool{
	client.Lock()
	defer client.Unlock()

	return client.closeFlag
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

	tcpConn := newTCPConn(conn, client.PendingWriteNum, &client.MsgParser,client.WriteDeadline)
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


package network

import (
	"github.com/duanhf2012/origin/v2/log"
	"github.com/xtaci/kcp-go/v5"
	"net"
	"sync"
	"time"
)

type KCPClient struct {
	sync.Mutex
	Addr            string
	ConnNum         int
	ConnectInterval time.Duration
	PendingWriteNum int
	ReadDeadline    time.Duration
	WriteDeadline   time.Duration
	AutoReconnect   bool
	NewAgent        func(conn *NetConn) Agent
	cons            ConnSet
	wg              sync.WaitGroup
	closeFlag       bool

	// msg parser
	MsgParser
}

func (client *KCPClient) Start() {
	client.init()

	for i := 0; i < client.ConnNum; i++ {
		client.wg.Add(1)
		go client.connect()
	}
}

func (client *KCPClient) init() {
	client.Lock()
	defer client.Unlock()

	if client.ConnNum <= 0 {
		client.ConnNum = 1
		log.Info("invalid ConnNum", log.Int("reset", client.ConnNum))
	}
	if client.ConnectInterval <= 0 {
		client.ConnectInterval = 3 * time.Second
		log.Info("invalid ConnectInterval", log.Duration("reset", client.ConnectInterval))
	}
	if client.PendingWriteNum <= 0 {
		client.PendingWriteNum = 1000
		log.Info("invalid PendingWriteNum", log.Int("reset", client.PendingWriteNum))
	}
	if client.ReadDeadline == 0 {
		client.ReadDeadline = 15 * time.Second
		log.Info("invalid ReadDeadline", log.Int64("reset", int64(client.ReadDeadline.Seconds())))
	}
	if client.WriteDeadline == 0 {
		client.WriteDeadline = 15 * time.Second
		log.Info("invalid WriteDeadline", log.Int64("reset", int64(client.WriteDeadline.Seconds())))
	}
	if client.NewAgent == nil {
		log.Fatal("NewAgent must not be nil")
	}
	if client.cons != nil {
		log.Fatal("client is running")
	}

	if client.MinMsgLen == 0 {
		client.MinMsgLen = Default_MinMsgLen
	}
	if client.MaxMsgLen == 0 {
		client.MaxMsgLen = Default_MaxMsgLen
	}
	if client.LenMsgLen == 0 {
		client.LenMsgLen = Default_LenMsgLen
	}
	maxMsgLen := client.MsgParser.getMaxMsgLen()
	if client.MaxMsgLen > maxMsgLen {
		client.MaxMsgLen = maxMsgLen
		log.Info("invalid MaxMsgLen", log.Uint32("reset", maxMsgLen))
	}

	client.cons = make(ConnSet)
	client.closeFlag = false
	client.MsgParser.Init()
}

func (client *KCPClient) GetCloseFlag() bool {
	client.Lock()
	defer client.Unlock()

	return client.closeFlag
}

func (client *KCPClient) dial() net.Conn {
	for {
		conn, err := kcp.DialWithOptions(client.Addr, nil, 10, 3)
		if client.closeFlag {
			return conn
		} else if err == nil && conn != nil {
			conn.SetNoDelay(1, 10, 2, 1)
			conn.SetDSCP(46)
			conn.SetStreamMode(true)
			conn.SetWindowSize(1024, 1024)
			return conn
		}

		log.Warning("connect error ", log.String("error", err.Error()), log.String("Addr", client.Addr))
		time.Sleep(client.ConnectInterval)
		continue
	}
}

func (client *KCPClient) connect() {
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

	netConn := newNetConn(conn, client.PendingWriteNum, &client.MsgParser, client.WriteDeadline)
	agent := client.NewAgent(netConn)
	agent.Run()

	// cleanup
	netConn.Close()
	client.Lock()
	delete(client.cons, conn)
	client.Unlock()
	agent.OnClose()

	if client.AutoReconnect {
		time.Sleep(client.ConnectInterval)
		goto reconnect
	}
}

func (client *KCPClient) Close(waitDone bool) {
	client.Lock()
	client.closeFlag = true
	for conn := range client.cons {
		conn.Close()
	}
	client.cons = nil
	client.Unlock()

	if waitDone == true {
		client.wg.Wait()
	}
}

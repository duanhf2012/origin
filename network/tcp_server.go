package network

import (
	"github.com/duanhf2012/origin/log"
	"net"
	"sync"
	"time"
)

const Default_ReadDeadline = 30  //30s
const Default_WriteDeadline = 30 //30s
const Default_MaxConnNum = 3000
const Default_PendingWriteNum = 10000
const Default_LittleEndian = false
const Default_MinMsgLen = 2
const Default_MaxMsgLen = 65535


type TCPServer struct {
	Addr            string
	MaxConnNum      int
	PendingWriteNum int
	ReadDeadline    time.Duration
	WriteDeadline 	time.Duration
	NewAgent        func(*TCPConn) Agent
	ln              net.Listener
	conns           ConnSet
	mutexConns      sync.Mutex
	wgLn            sync.WaitGroup
	wgConns         sync.WaitGroup


	// msg parser
	LenMsgLen    int
	MinMsgLen    uint32
	MaxMsgLen    uint32
	LittleEndian bool
	msgParser    *MsgParser
	netMemPool   INetMempool
}

func (server *TCPServer) Start() {
	server.init()
	go server.run()
}

func (server *TCPServer) init() {
	ln, err := net.Listen("tcp", server.Addr)
	if err != nil {
		log.SFatal("Listen tcp error:", err.Error())
	}

	if server.MaxConnNum <= 0 {
		server.MaxConnNum = Default_MaxConnNum
		log.SRelease("invalid MaxConnNum, reset to ", server.MaxConnNum)
	}
	if server.PendingWriteNum <= 0 {
		server.PendingWriteNum = Default_PendingWriteNum
		log.SRelease("invalid PendingWriteNum, reset to ", server.PendingWriteNum)
	}

	if server.MinMsgLen <= 0 {
		server.MinMsgLen = Default_MinMsgLen
		log.SRelease("invalid MinMsgLen, reset to ", server.MinMsgLen)
	}

	if server.MaxMsgLen <= 0 {
		server.MaxMsgLen = Default_MaxMsgLen
		log.SRelease("invalid MaxMsgLen, reset to ", server.MaxMsgLen)
	}

	if server.WriteDeadline == 0 {
		server.WriteDeadline = time.Second*Default_WriteDeadline
		log.SRelease("invalid WriteDeadline, reset to ", server.WriteDeadline.Seconds(),"s")
	}
	if server.ReadDeadline == 0 {
		server.ReadDeadline = time.Second*Default_ReadDeadline
		log.SRelease("invalid ReadDeadline, reset to ", server.ReadDeadline.Seconds(),"s")
	}

	if server.NewAgent == nil {
		log.SFatal("NewAgent must not be nil")
	}

	server.ln = ln
	server.conns = make(ConnSet)

	// msg parser
	msgParser := NewMsgParser()
	if msgParser.INetMempool == nil {
		msgParser.INetMempool = NewMemAreaPool()
	}

	msgParser.SetMsgLen(server.LenMsgLen, server.MinMsgLen, server.MaxMsgLen)
	msgParser.SetByteOrder(server.LittleEndian)
	server.msgParser = msgParser
}

func (server *TCPServer) SetNetMempool(mempool INetMempool){
	server.msgParser.INetMempool = mempool
}

func (server *TCPServer) GetNetMempool() INetMempool{
	return server.msgParser.INetMempool
}

func (server *TCPServer) run() {
	server.wgLn.Add(1)
	defer server.wgLn.Done()

	var tempDelay time.Duration
	for {
		conn, err := server.ln.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				log.SRelease("accept error:",err.Error(),"; retrying in ", tempDelay)
				time.Sleep(tempDelay)
				continue
			}
			return
		}
		conn.(*net.TCPConn).SetNoDelay(true)
		tempDelay = 0

		server.mutexConns.Lock()
		if len(server.conns) >= server.MaxConnNum {
			server.mutexConns.Unlock()
			conn.Close()
			log.SWarning("too many connections")
			continue
		}
		server.conns[conn] = struct{}{}
		server.mutexConns.Unlock()

		server.wgConns.Add(1)

		tcpConn := newTCPConn(conn, server.PendingWriteNum, server.msgParser,server.WriteDeadline)
		agent := server.NewAgent(tcpConn)
		go func() {
			agent.Run()

			// cleanup
			tcpConn.Close()
			server.mutexConns.Lock()
			delete(server.conns, conn)
			server.mutexConns.Unlock()
			agent.OnClose()

			server.wgConns.Done()
		}()
	}
}

func (server *TCPServer) Close() {
	server.ln.Close()
	server.wgLn.Wait()

	server.mutexConns.Lock()
	for conn := range server.conns {
		conn.Close()
	}
	server.conns = nil
	server.mutexConns.Unlock()
	server.wgConns.Wait()
}

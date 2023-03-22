package network

import (
	"github.com/duanhf2012/origin/log"
	"net"
	"sync"
	"time"
)

const(
	Default_ReadDeadline  = time.Second*30 //默认读超时30s
	Default_WriteDeadline = time.Second*30 //默认写超时30s
	Default_MaxConnNum = 1000000						 //默认最大连接数
	Default_PendingWriteNum = 100000				     //单连接写消息Channel容量
	Default_LittleEndian = false						 //默认大小端
	Default_MinMsgLen = 2								 //最小消息长度2byte
	Default_LenMsgLen = 2								 //包头字段长度占用2byte
	Default_MaxMsgLen = 65535                            //最大消息长度
)

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

	MsgParser
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

	if server.LenMsgLen <= 0 {
		server.LenMsgLen = Default_LenMsgLen
		log.SRelease("invalid LenMsgLen, reset to ", server.LenMsgLen)
	}

	if server.MaxMsgLen <= 0 {
		server.MaxMsgLen = Default_MaxMsgLen
		log.SRelease("invalid MaxMsgLen, reset to ", server.MaxMsgLen)
	}

	maxMsgLen := server.MsgParser.getMaxMsgLen(server.LenMsgLen)
	if server.MaxMsgLen > maxMsgLen {
		server.MaxMsgLen = maxMsgLen
		log.SRelease("invalid MaxMsgLen, reset to ", maxMsgLen)
	}
	
	if server.MinMsgLen <= 0 {
		server.MinMsgLen = Default_MinMsgLen
		log.SRelease("invalid MinMsgLen, reset to ", server.MinMsgLen)
	}

	if server.WriteDeadline == 0 {
		server.WriteDeadline = Default_WriteDeadline
		log.SRelease("invalid WriteDeadline, reset to ", server.WriteDeadline.Seconds(),"s")
	}

	if server.ReadDeadline == 0 {
		server.ReadDeadline = Default_ReadDeadline
		log.SRelease("invalid ReadDeadline, reset to ", server.ReadDeadline.Seconds(),"s")
	}

	if server.NewAgent == nil {
		log.SFatal("NewAgent must not be nil")
	}

	server.ln = ln
	server.conns = make(ConnSet)
	server.MsgParser.init()
}

func (server *TCPServer) SetNetMempool(mempool INetMempool){
	server.INetMempool = mempool
}

func (server *TCPServer) GetNetMempool() INetMempool{
	return server.INetMempool
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

		tcpConn := newTCPConn(conn, server.PendingWriteNum, &server.MsgParser,server.WriteDeadline)
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

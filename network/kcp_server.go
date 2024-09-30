package network

import (
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/network/processor"
	kcp "github.com/xtaci/kcp-go/v5"
	"sync"
	"time"
)

type KCPServer struct {
	NewAgent func(Conn) Agent

	kcpCfg     *KcpCfg
	blockCrypt kcp.BlockCrypt

	process    processor.IRawProcessor
	msgParser  MsgParser
	conns      ConnSet
	mutexConns sync.Mutex
	wgLn       sync.WaitGroup
	wgConns    sync.WaitGroup

	listener *kcp.Listener
}

/*
	NoDelayCfg

普通模式： ikcp_nodelay(kcp, 0, 40, 0, 0);
极速模式： ikcp_nodelay(kcp, 1, 10, 2, 1);
*/
type NoDelayCfg struct {
	NoDelay           int // 是否启用 nodelay模式，0不启用；1启用
	IntervalMill      int // 协议内部工作的 interval，单位毫秒，比如 10ms或者 20ms
	Resend            int // 快速重传模式，默认0关闭，可以设置2（2次ACK跨越将会直接重传）
	CongestionControl int // 是否关闭流控，默认是0代表不关闭，1代表关闭
}

const (
	DefaultNoDelay           = 1
	DefaultIntervalMill      = 10
	DefaultResend            = 2
	DefaultCongestionControl = 1

	DefaultMtu          = 1400
	DefaultSndWndSize   = 4096
	DefaultRcvWndSize   = 4096
	DefaultStreamMode   = true
	DefaultDSCP         = 46
	DefaultDataShards   = 10
	DefaultParityShards = 0

	DefaultReadDeadlineMill  = 15 * time.Second
	DefaultWriteDeadlineMill = 15 * time.Second

	DefaultMaxConnNum = 20000
)

type KcpCfg struct {
	ListenAddr string // 监听地址
	MaxConnNum int    //最大连接数
	NoDelay    *NoDelayCfg

	Mtu               *int           // mtu大小
	SndWndSize        *int           // 发送窗口大小,默认1024
	RcvWndSize        *int           // 接收窗口大小,默认1024
	ReadDeadlineMill  *time.Duration // 读超时毫秒
	WriteDeadlineMill *time.Duration // 写超时毫秒
	StreamMode        *bool          // 是否打开流模式,默认true
	DSCP              *int           // 差分服务代码点，默认46
	ReadBuffSize      *int           // 读Buff大小,默认
	WriteBuffSize     *int           // 写Buff大小

	// 用于 FEC（前向纠错）的数据分片和校验分片数量,，默认10,0
	DataShards   *int
	ParityShards *int

	// 包体内容

	LittleEndian    bool   //是否小端序
	LenMsgLen       int    //消息头占用byte数量，只能是1byte,2byte,4byte。如果是4byte，意味着消息最大可以是math.MaxUint32(4GB)
	MinMsgLen       uint32 //最小消息长度
	MaxMsgLen       uint32 //最大消息长度,超过判定不合法,断开连接
	PendingWriteNum int    //写channel最大消息数量
}

func (kp *KCPServer) Init(kcpCfg *KcpCfg) {
	kp.kcpCfg = kcpCfg
	kp.msgParser.Init()
	kp.msgParser.LenMsgLen = kp.kcpCfg.LenMsgLen
	kp.msgParser.MaxMsgLen = kp.kcpCfg.MaxMsgLen
	kp.msgParser.MinMsgLen = kp.kcpCfg.MinMsgLen
	kp.msgParser.LittleEndian = kp.kcpCfg.LittleEndian

	// setting default noDelay
	if kp.kcpCfg.NoDelay == nil {
		var noDelay NoDelayCfg
		noDelay.NoDelay = DefaultNoDelay
		noDelay.IntervalMill = DefaultIntervalMill
		noDelay.Resend = DefaultResend
		noDelay.CongestionControl = DefaultCongestionControl
		kp.kcpCfg.NoDelay = &noDelay
	}

	if kp.kcpCfg.Mtu == nil {
		mtu := DefaultMtu
		kp.kcpCfg.Mtu = &mtu
	}

	if kp.kcpCfg.SndWndSize == nil {
		sndWndSize := DefaultSndWndSize
		kp.kcpCfg.SndWndSize = &sndWndSize
	}
	if kp.kcpCfg.RcvWndSize == nil {
		rcvWndSize := DefaultRcvWndSize
		kp.kcpCfg.RcvWndSize = &rcvWndSize
	}
	if kp.kcpCfg.ReadDeadlineMill == nil {
		readDeadlineMill := DefaultReadDeadlineMill
		kp.kcpCfg.ReadDeadlineMill = &readDeadlineMill
	} else {
		*kp.kcpCfg.ReadDeadlineMill *= time.Millisecond
	}
	if kp.kcpCfg.WriteDeadlineMill == nil {
		writeDeadlineMill := DefaultWriteDeadlineMill
		kp.kcpCfg.WriteDeadlineMill = &writeDeadlineMill
	} else {
		*kp.kcpCfg.WriteDeadlineMill *= time.Millisecond
	}
	if kp.kcpCfg.StreamMode == nil {
		streamMode := DefaultStreamMode
		kp.kcpCfg.StreamMode = &streamMode
	}
	if kp.kcpCfg.DataShards == nil {
		dataShards := DefaultDataShards
		kp.kcpCfg.DataShards = &dataShards
	}
	if kp.kcpCfg.ParityShards == nil {
		parityShards := DefaultParityShards
		kp.kcpCfg.ParityShards = &parityShards
	}
	if kp.kcpCfg.DSCP == nil {
		dss := DefaultDSCP
		kp.kcpCfg.DSCP = &dss
	}

	if kp.kcpCfg.MaxConnNum == 0 {
		kp.kcpCfg.MaxConnNum = DefaultMaxConnNum
	}

	kp.conns = make(ConnSet, 2048)
	kp.msgParser.Init()
	return
}

func (kp *KCPServer) Start() error {
	listener, err := kcp.ListenWithOptions(kp.kcpCfg.ListenAddr, kp.blockCrypt, *kp.kcpCfg.DataShards, *kp.kcpCfg.ParityShards)
	if err != nil {
		return err
	}

	if kp.kcpCfg.ReadBuffSize != nil {
		err = listener.SetReadBuffer(*kp.kcpCfg.ReadBuffSize)
		if err != nil {
			return err
		}
	}
	if kp.kcpCfg.WriteBuffSize != nil {
		err = listener.SetWriteBuffer(*kp.kcpCfg.WriteBuffSize)
		if err != nil {
			return err
		}
	}
	err = listener.SetDSCP(*kp.kcpCfg.DSCP)
	if err != nil {
		return err
	}

	kp.listener = listener

	kp.wgLn.Add(1)
	go func() {
		defer kp.wgLn.Done()
		for kp.run(listener) {
		}
	}()

	return nil
}

func (kp *KCPServer) initSession(session *kcp.UDPSession) {
	session.SetStreamMode(*kp.kcpCfg.StreamMode)
	session.SetWindowSize(*kp.kcpCfg.SndWndSize, *kp.kcpCfg.RcvWndSize)
	session.SetNoDelay(kp.kcpCfg.NoDelay.NoDelay, kp.kcpCfg.NoDelay.IntervalMill, kp.kcpCfg.NoDelay.Resend, kp.kcpCfg.NoDelay.CongestionControl)
	session.SetDSCP(*kp.kcpCfg.DSCP)
	session.SetMtu(*kp.kcpCfg.Mtu)
	session.SetACKNoDelay(false)

	//session.SetWriteDeadline(time.Now().Add(*kp.kcpCfg.WriteDeadlineMill))
}

func (kp *KCPServer) run(listener *kcp.Listener) bool {
	conn, err := listener.Accept()
	if err != nil {
		log.Error("accept error", log.String("ListenAddr", kp.kcpCfg.ListenAddr), log.ErrorAttr("err", err))
		return false
	}

	kp.mutexConns.Lock()
	if len(kp.conns) >= kp.kcpCfg.MaxConnNum {
		kp.mutexConns.Unlock()
		conn.Close()
		log.Warning("too many connections")
		return true
	}
	kp.conns[conn] = struct{}{}
	kp.mutexConns.Unlock()

	if kp.kcpCfg.ReadBuffSize != nil {
		conn.(*kcp.UDPSession).SetReadBuffer(*kp.kcpCfg.ReadBuffSize)
	}
	if kp.kcpCfg.WriteBuffSize != nil {
		conn.(*kcp.UDPSession).SetWriteBuffer(*kp.kcpCfg.WriteBuffSize)
	}
	kp.initSession(conn.(*kcp.UDPSession))

	netConn := newNetConn(conn, kp.kcpCfg.PendingWriteNum, &kp.msgParser, *kp.kcpCfg.WriteDeadlineMill)
	agent := kp.NewAgent(netConn)
	kp.wgConns.Add(1)
	go func() {
		agent.Run()
		// cleanup
		conn.Close()
		kp.mutexConns.Lock()
		delete(kp.conns, conn)
		kp.mutexConns.Unlock()
		agent.OnClose()

		kp.wgConns.Done()
	}()

	return true
}

func (kp *KCPServer) Close() {
	kp.listener.Close()
	kp.wgLn.Wait()

	kp.mutexConns.Lock()
	for conn := range kp.conns {
		conn.Close()
	}
	kp.conns = nil
	kp.mutexConns.Unlock()
	kp.wgConns.Wait()
}

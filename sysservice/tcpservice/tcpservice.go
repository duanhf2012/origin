package tcpservice

import (
	"fmt"
	"github.com/duanhf2012/origin/v2/event"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/network"
	"github.com/duanhf2012/origin/v2/network/processor"
	"github.com/duanhf2012/origin/v2/service"
	"github.com/duanhf2012/origin/v2/util/bytespool"
	"github.com/google/uuid"
	"runtime"
	"strings"
	"sync"
	"time"
)

type TcpService struct {
	tcpServer network.TCPServer
	service.Service

	mapClientLocker sync.RWMutex
	mapClient       map[string]*Client
	process         processor.IProcessor
}

type TcpPackType int8

const (
	TPT_Connected    TcpPackType = 0
	TPT_DisConnected TcpPackType = 1
	TPT_Pack         TcpPackType = 2
	TPT_UnknownPack  TcpPackType = 3
)

type TcpPack struct {
	Type     TcpPackType //0表示连接 1表示断开 2表示数据
	ClientId string
	Data     interface{}
}

type Client struct {
	id         string
	tcpConn    *network.NetConn
	tcpService *TcpService
}

func (tcpService *TcpService) OnInit() error {
	iConfig := tcpService.GetServiceCfg()
	if iConfig == nil {
		return fmt.Errorf("%s service config is error", tcpService.GetName())
	}
	tcpCfg := iConfig.(map[string]interface{})
	addr, ok := tcpCfg["ListenAddr"]
	if ok == false {
		return fmt.Errorf("%s service config is error", tcpService.GetName())
	}

	tcpService.tcpServer.Addr = addr.(string)
	MaxConnNum, ok := tcpCfg["MaxConnNum"]
	if ok == true {
		tcpService.tcpServer.MaxConnNum = int(MaxConnNum.(float64))
	}

	PendingWriteNum, ok := tcpCfg["PendingWriteNum"]
	if ok == true {
		tcpService.tcpServer.PendingWriteNum = int(PendingWriteNum.(float64))
	}
	LittleEndian, ok := tcpCfg["LittleEndian"]
	if ok == true {
		tcpService.tcpServer.LittleEndian = LittleEndian.(bool)
	}
	LenMsgLen, ok := tcpCfg["LenMsgLen"]
	if ok == true {
		tcpService.tcpServer.LenMsgLen = int(LenMsgLen.(float64))
	}
	MinMsgLen, ok := tcpCfg["MinMsgLen"]
	if ok == true {
		tcpService.tcpServer.MinMsgLen = uint32(MinMsgLen.(float64))
	}
	MaxMsgLen, ok := tcpCfg["MaxMsgLen"]
	if ok == true {
		tcpService.tcpServer.MaxMsgLen = uint32(MaxMsgLen.(float64))
	}

	readDeadline, ok := tcpCfg["ReadDeadline"]
	if ok == true {
		tcpService.tcpServer.ReadDeadline = time.Second * time.Duration(readDeadline.(float64))
	}

	writeDeadline, ok := tcpCfg["WriteDeadline"]
	if ok == true {
		tcpService.tcpServer.WriteDeadline = time.Second * time.Duration(writeDeadline.(float64))
	}

	tcpService.mapClient = make(map[string]*Client, tcpService.tcpServer.MaxConnNum)
	tcpService.tcpServer.NewAgent = tcpService.NewClient
	return tcpService.tcpServer.Start()
}

func (tcpService *TcpService) TcpEventHandler(ev event.IEvent) {
	pack := ev.(*event.Event).Data.(TcpPack)
	switch pack.Type {
	case TPT_Connected:
		tcpService.process.ConnectedRoute(pack.ClientId)
	case TPT_DisConnected:
		tcpService.process.DisConnectedRoute(pack.ClientId)
	case TPT_UnknownPack:
		tcpService.process.UnknownMsgRoute(pack.ClientId, pack.Data, tcpService.recyclerReaderBytes)
	case TPT_Pack:
		tcpService.process.MsgRoute(pack.ClientId, pack.Data, tcpService.recyclerReaderBytes)
	}
}

func (tcpService *TcpService) recyclerReaderBytes(data []byte) {
}

func (tcpService *TcpService) SetProcessor(process processor.IProcessor, handler event.IEventHandler) {
	tcpService.process = process
	tcpService.RegEventReceiverFunc(event.Sys_Event_Tcp, handler, tcpService.TcpEventHandler)
}

func (tcpService *TcpService) NewClient(conn network.Conn) network.Agent {
	tcpService.mapClientLocker.Lock()
	defer tcpService.mapClientLocker.Unlock()

	uuId, _ := uuid.NewUUID()
	clientId := strings.ReplaceAll(uuId.String(), "-", "")
	pClient := &Client{tcpConn: conn.(*network.NetConn), id: clientId}
	pClient.tcpService = tcpService
	tcpService.mapClient[clientId] = pClient

	return pClient
}

func (slf *Client) GetId() string {
	return slf.id
}

func (slf *Client) Run() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			log.Dump(string(buf[:l]), log.String("error", errString))
		}
	}()

	slf.tcpService.NotifyEvent(&event.Event{Type: event.Sys_Event_Tcp, Data: TcpPack{ClientId: slf.id, Type: TPT_Connected}})
	for {
		if slf.tcpConn == nil {
			break
		}

		slf.tcpConn.SetReadDeadline(slf.tcpService.tcpServer.ReadDeadline)
		bytes, err := slf.tcpConn.ReadMsg()
		if err != nil {
			log.Debug("read client failed", log.ErrorAttr("error", err), log.String("clientId", slf.id))
			break
		}
		data, err := slf.tcpService.process.Unmarshal(slf.id, bytes)

		if err != nil {
			slf.tcpService.NotifyEvent(&event.Event{Type: event.Sys_Event_Tcp, Data: TcpPack{ClientId: slf.id, Type: TPT_UnknownPack, Data: bytes}})
			continue
		}
		slf.tcpService.NotifyEvent(&event.Event{Type: event.Sys_Event_Tcp, Data: TcpPack{ClientId: slf.id, Type: TPT_Pack, Data: data}})
	}
}

func (slf *Client) OnClose() {
	slf.tcpService.NotifyEvent(&event.Event{Type: event.Sys_Event_Tcp, Data: TcpPack{ClientId: slf.id, Type: TPT_DisConnected}})
	slf.tcpService.mapClientLocker.Lock()
	defer slf.tcpService.mapClientLocker.Unlock()
	delete(slf.tcpService.mapClient, slf.GetId())
}

func (tcpService *TcpService) SendMsg(clientId string, msg interface{}) error {
	tcpService.mapClientLocker.Lock()
	client, ok := tcpService.mapClient[clientId]
	if ok == false {
		tcpService.mapClientLocker.Unlock()
		return fmt.Errorf("client %s is disconnect", clientId)
	}

	tcpService.mapClientLocker.Unlock()
	bytes, err := tcpService.process.Marshal(clientId, msg)
	if err != nil {
		return err
	}
	return client.tcpConn.WriteMsg(bytes)
}

func (tcpService *TcpService) Close(clientId string) {
	tcpService.mapClientLocker.Lock()
	defer tcpService.mapClientLocker.Unlock()

	client, ok := tcpService.mapClient[clientId]
	if ok == false {
		return
	}

	if client.tcpConn != nil {
		client.tcpConn.Close()
	}

	return
}

func (tcpService *TcpService) GetClientIp(clientId string) string {
	tcpService.mapClientLocker.Lock()
	defer tcpService.mapClientLocker.Unlock()
	pClient, ok := tcpService.mapClient[clientId]
	if ok == false {
		return ""
	}

	return pClient.tcpConn.GetRemoteIp()
}

func (tcpService *TcpService) SendRawMsg(clientId string, msg []byte) error {
	tcpService.mapClientLocker.Lock()
	client, ok := tcpService.mapClient[clientId]
	if ok == false {
		tcpService.mapClientLocker.Unlock()
		return fmt.Errorf("client %s is disconnect", clientId)
	}
	tcpService.mapClientLocker.Unlock()
	return client.tcpConn.WriteMsg(msg)
}

func (tcpService *TcpService) SendRawData(clientId string, data []byte) error {
	tcpService.mapClientLocker.Lock()
	client, ok := tcpService.mapClient[clientId]
	if ok == false {
		tcpService.mapClientLocker.Unlock()
		return fmt.Errorf("client %s is disconnect", clientId)
	}
	tcpService.mapClientLocker.Unlock()
	return client.tcpConn.WriteRawMsg(data)
}

func (tcpService *TcpService) GetConnNum() int {
	tcpService.mapClientLocker.Lock()
	connNum := len(tcpService.mapClient)
	tcpService.mapClientLocker.Unlock()
	return connNum
}

func (tcpService *TcpService) SetNetMemPool(memPool bytespool.IBytesMemPool) {
	tcpService.tcpServer.SetNetMemPool(memPool)
}

func (tcpService *TcpService) GetNetMemPool() bytespool.IBytesMemPool {
	return tcpService.tcpServer.GetNetMemPool()
}

func (tcpService *TcpService) ReleaseNetMem(byteBuff []byte) {
	tcpService.tcpServer.GetNetMemPool().ReleaseBytes(byteBuff)
}

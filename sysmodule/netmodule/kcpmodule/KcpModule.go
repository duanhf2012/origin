package kcpmodule

import (
	"fmt"
	"github.com/duanhf2012/origin/v2/event"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/network"
	"github.com/duanhf2012/origin/v2/network/processor"
	"github.com/duanhf2012/origin/v2/service"
	"github.com/xtaci/kcp-go/v5"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"runtime"
	"sync"
)

type KcpModule struct {
	service.Module
	blockCrypt kcp.BlockCrypt

	mapClientLocker sync.RWMutex
	mapClient       map[string]*Client
	process         processor.IRawProcessor

	kcpServer network.KCPServer
	kcpCfg    *network.KcpCfg
}

type Client struct {
	id        string
	kcpConn   *network.NetConn
	kcpModule *KcpModule
}

type KcpPackType int8

const (
	KPTConnected    KcpPackType = 0
	KPTDisConnected KcpPackType = 1
	KPTPack         KcpPackType = 2
	KPTUnknownPack  KcpPackType = 3
)

type KcpPack struct {
	Type                KcpPackType //0表示连接 1表示断开 2表示数据
	ClientId            string
	Data                interface{}
	RecyclerReaderBytes func(data []byte)
}

func (km *KcpModule) OnInit() error {
	if km.kcpCfg == nil || km.process == nil {
		return fmt.Errorf("please call the Init function correctly")
	}

	km.mapClient = make(map[string]*Client, km.kcpCfg.MaxConnNum)
	km.GetEventProcessor().RegEventReceiverFunc(event.Sys_Event_Kcp, km.GetEventHandler(), km.kcpEventHandler)
	km.process.SetByteOrder(km.kcpCfg.LittleEndian)
	km.kcpServer.Init(km.kcpCfg)
	km.kcpServer.NewAgent = km.NewAgent

	return nil
}

func (km *KcpModule) Init(kcpCfg *network.KcpCfg, process processor.IRawProcessor) {
	km.kcpCfg = kcpCfg
	km.process = process
}

func (km *KcpModule) Start() error {
	return km.kcpServer.Start()
}

func (km *KcpModule) kcpEventHandler(ev event.IEvent) {
	e := ev.(*event.Event)
	switch KcpPackType(e.IntExt[0]) {
	case KPTConnected:
		km.process.ConnectedRoute(e.StringExt[0])
	case KPTDisConnected:
		km.process.DisConnectedRoute(e.StringExt[0])
	case KPTUnknownPack:
		km.process.UnknownMsgRoute(e.StringExt[0], e.Data, e.AnyExt[0].(func(data []byte)))
	case KPTPack:
		km.process.MsgRoute(e.StringExt[0], e.Data, e.AnyExt[0].(func(data []byte)))
	}

	event.DeleteEvent(ev)
}

func (km *KcpModule) SetBlob(blockCrypt kcp.BlockCrypt) {
	km.blockCrypt = blockCrypt
}

func (km *KcpModule) OnConnected(c *Client) {
	ev := event.NewEvent()
	ev.Type = event.Sys_Event_Kcp
	ev.IntExt[0] = int64(KPTConnected)
	ev.StringExt[0] = c.id

	km.NotifyEvent(ev)
}

func (km *KcpModule) OnClose(c *Client) {
	ev := event.NewEvent()
	ev.Type = event.Sys_Event_Kcp
	ev.IntExt[0] = int64(KPTDisConnected)
	ev.StringExt[0] = c.id

	km.NotifyEvent(ev)
}

func (km *KcpModule) newClient(conn network.Conn) *Client {
	km.mapClientLocker.Lock()
	defer km.mapClientLocker.Unlock()

	pClient := &Client{kcpConn: conn.(*network.NetConn), id: primitive.NewObjectID().Hex()}
	pClient.kcpModule = km
	km.mapClient[pClient.id] = pClient

	return pClient
}

func (km *KcpModule) GetProcessor() processor.IRawProcessor {
	return km.process
}

func (km *KcpModule) SendRawMsg(clientId string, data []byte) error {
	km.mapClientLocker.Lock()
	client, ok := km.mapClient[clientId]
	if ok == false {
		km.mapClientLocker.Unlock()
		return fmt.Errorf("client %s is disconnect", clientId)
	}
	km.mapClientLocker.Unlock()
	return client.kcpConn.WriteMsg(data)
}

func (km *KcpModule) Close(clientId string) {
	km.mapClientLocker.Lock()
	client, ok := km.mapClient[clientId]
	if ok == false {
		km.mapClientLocker.Unlock()
		return
	}
	km.mapClientLocker.Unlock()
	client.kcpConn.Close()
}

func (km *KcpModule) GetClientIp(clientId string) string {
	km.mapClientLocker.Lock()
	defer km.mapClientLocker.Unlock()
	client, ok := km.mapClient[clientId]
	if ok == false {
		return ""
	}
	removeAddr := client.kcpConn.RemoteAddr()
	if removeAddr != nil {
		return removeAddr.String()
	}
	return ""
}

func (km *KcpModule) NewAgent(conn network.Conn) network.Agent {
	c := km.newClient(conn)
	return c
}

func (c *Client) Run() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			log.Dump(string(buf[:l]), log.String("error", errString))
		}
	}()

	c.kcpModule.OnConnected(c)
	for c.kcpConn != nil {
		c.kcpConn.SetReadDeadline(*c.kcpModule.kcpCfg.ReadDeadlineMill)
		msgBuff, err := c.kcpConn.ReadMsg()
		if err != nil {
			log.Debug("read client failed", log.ErrorAttr("error", err), log.String("clientId", c.id))
			break
		}

		data, err := c.kcpModule.process.Unmarshal(c.id, msgBuff)
		if err != nil {
			ev := event.NewEvent()
			ev.Type = event.Sys_Event_Kcp
			ev.IntExt[0] = int64(KPTUnknownPack)
			ev.StringExt[0] = c.id
			ev.Data = msgBuff
			ev.AnyExt[0] = c.kcpConn.GetRecyclerReaderBytes()
			c.kcpModule.NotifyEvent(ev)
			continue
		}

		ev := event.NewEvent()
		ev.Type = event.Sys_Event_Kcp
		ev.IntExt[0] = int64(KPTPack)
		ev.StringExt[0] = c.id
		ev.Data = data
		ev.AnyExt[0] = c.kcpConn.GetRecyclerReaderBytes()
		c.kcpModule.NotifyEvent(ev)
	}
}

func (c *Client) OnClose() {
	c.kcpModule.OnClose(c)

	c.kcpModule.mapClientLocker.Lock()
	delete(c.kcpModule.mapClient, c.id)
	c.kcpModule.mapClientLocker.Unlock()
}

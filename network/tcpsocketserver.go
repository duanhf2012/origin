package network

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/util"
	"github.com/golang/protobuf/proto"
	"io"
	"net"
	"unsafe"

	"os"
	"time"
)

type ITcpSocketServerReciver interface {
	OnConnected(pClient *SClient)
	OnDisconnect(pClient *SClient)
	OnRecvMsg(pClient *SClient, pPack *MsgBasePack)
	VerifyPackType(packtype uint16) bool
}


type SClient struct {
	id uint64
	conn net.Conn

	recvPack *util.SyncQueue
	sendPack *util.SyncQueue
	tcpserver *TcpSocketServer
	remoteip string
	starttime int64
	bClose bool
}

type TcpSocketServer struct {
	listenAddr string //ip:port
	mapClient util.Map

	MaxRecvPackSize uint16
	MaxSendPackSize uint16
	iReciver ITcpSocketServerReciver
}

type MsgBasePack struct {
	packsize uint16
	packtype uint16
	body []byte
	StartTime time.Time
}

func (slf *MsgBasePack) PackType() uint16 {
	return slf.packtype
}

func (slf *MsgBasePack) Body() []byte{
	return slf.body
}


func (slf *TcpSocketServer) Register(listenAddr string,iReciver ITcpSocketServerReciver){
	slf.listenAddr = listenAddr
	slf.iReciver = iReciver
}


func (slf *TcpSocketServer) Start(){
	slf.MaxRecvPackSize = 2048
	slf.MaxSendPackSize = 40960

	util.Go(slf.listenServer)
}

func (slf *TcpSocketServer) listenServer(){
	listener, err := net.Listen("tcp", slf.listenAddr)
	if err != nil {
		service.GetLogger().Printf(service.LEVER_FATAL, "TcpSocketServer Listen error %+v",err)
		os.Exit(1)
	}

	var clientId uint64
	for {
		conn, aerr := listener.Accept()
		if aerr != nil {
			service.GetLogger().Printf(service.LEVER_FATAL, "TcpSocketServer accept error %+v",aerr)
			continue
		}

		for {
			clientId += 1
			if slf.mapClient.Get(clientId)!= nil {
				continue
			}

			sc :=&SClient{id:clientId,conn:conn,tcpserver:slf,remoteip:conn.RemoteAddr().String(),starttime:time.Now().UnixNano(),
				recvPack:util.NewSyncQueue(),sendPack:util.NewSyncQueue()}

			slf.mapClient.Set(clientId,sc)
			util.Go(sc.listendata)
			//收来自客户端数据
			util.Go(sc.onrecv)
			//发送数据队列
			util.Go(sc.onsend)

			break
		}
	}
}


func (slf *SClient) listendata(){
	defer func() {
		slf.Close()
		slf.tcpserver.mapClient.Del(slf.id)
		slf.tcpserver.iReciver.OnDisconnect(slf)
		service.GetLogger().Printf(service.LEVER_DEBUG, "clent id %d return listendata...",slf.id)
	}()

	slf.tcpserver.iReciver.OnConnected(slf)
	//获取一个连接的reader读取流
	reader := bufio.NewReader(slf.conn)

	//临时接受数据的buff
	var buff []byte //tmprecvbuf
	var tmpbuff []byte
	var buffDataSize uint16
	tmpbuff = make([]byte,2048)

	//解析包数据
	var pack MsgBasePack
	for {
		n,err := reader.Read(tmpbuff)
		if err != nil || err == io.EOF {
			service.GetLogger().Printf(service.LEVER_INFO, "clent id %d is disconnect  %+v",slf.id,err)
			return
		}
		buff = append(buff,tmpbuff[:n]...)
		buffDataSize += uint16(n)
		if buffDataSize> slf.tcpserver.MaxRecvPackSize {
			service.GetLogger().Print(service.LEVER_WARN,"recv client id %d data size %d is over %d",slf.id,buffDataSize,slf.tcpserver.MaxRecvPackSize)
			return
		}

		fillsize,bfillRet,fillhead := pack.FillData(buff,buffDataSize)
		//提交校验头
		if fillhead == true {
			if pack.packsize>slf.tcpserver.MaxRecvPackSize || slf.tcpserver.iReciver.VerifyPackType(pack.packtype) == false {
				service.GetLogger().Printf(service.LEVER_WARN, "VerifyPackType error clent id %d is disconnect  %d,%d",slf.id,pack.packtype, pack.packsize)
				return
			}
		}
		if bfillRet == true {
			slf.recvPack.Push(pack)
			pack = MsgBasePack{}
		}
		if fillsize>0 {
			buff = append(buff[fillsize:])
			buffDataSize -= fillsize
		}
	}
}


func (slf *MsgBasePack) Bytes() []byte{
	var bRet []byte
	bRet = make([]byte,4)
	binary.BigEndian.PutUint16(bRet,slf.packsize)
	binary.BigEndian.PutUint16(bRet[2:],slf.packtype)
	bRet = append(bRet,slf.body...)

	return bRet
}

//返回值：填充多少字节，是否完成,是否填充头
func (slf *MsgBasePack) FillData(bdata []byte,datasize uint16) (uint16,bool,bool) {
	var fillsize uint16
	fillhead := false
	//解包头
	if slf.packsize ==0 {
		if datasize < 4 {
			return 0,false,fillhead
		}

		slf.packsize= binary.BigEndian.Uint16(bdata[:2])
		slf.packtype= binary.BigEndian.Uint16(bdata[2:4])
		fillsize += 4
		fillhead = true
	}

	//解包体
	if slf.packsize>0 && datasize+4>=slf.packsize {
		slf.body = append(slf.body, bdata[fillsize:slf.packsize]...)
		fillsize += (slf.packsize - fillsize)
		return fillsize,true,fillhead
	}

	return fillsize,false,fillhead
}

func (slf *MsgBasePack) Clear() {
}

func (slf *MsgBasePack) Make(packtype uint16,data []byte) {
	slf.packtype = packtype
	slf.body = data
	slf.packsize = uint16(unsafe.Sizeof(slf.packtype)*2)+uint16(len(data))
}

func (slf *SClient) Send(pack *MsgBasePack){
	slf.sendPack.Push(pack)
}


func (slf *SClient)  SendMsg(packtype uint16,message proto.Message) error{
	if slf.bClose == true {
		return fmt.Errorf("client id %d is close!",slf.id)
	}

	var msg MsgBasePack
	data,err := proto.Marshal(message)
	if err != nil {
		return err
	}

	msg.Make(packtype,data)
	slf.sendPack.Push(&msg)

	return nil
}

func (slf *SClient) onsend(){
	defer func() {
		slf.Close()
		service.GetLogger().Printf(service.LEVER_DEBUG, "clent id %d return onsend...",slf.id)
	}()

	for {
		pack,ok := slf.sendPack.TryPop()
		if slf.bClose == true {
			break
		}
		if ok == false || pack == nil {
			time.Sleep(time.Millisecond*1)
			continue
		}

		pPackData := pack.(*MsgBasePack)
		slf.conn.Write(pPackData.Bytes())
	}
}

func (slf *SClient) onrecv(){
	defer func() {
		slf.Close()
		service.GetLogger().Printf(service.LEVER_DEBUG, "clent id %d return onrecv...",slf.id)
	}()

	for {
		pack,ok  := slf.recvPack.TryPop()
		if slf.bClose == true {
			break
		}
		if ok == false || pack == nil {
			time.Sleep(time.Millisecond*1)
			continue
		}

		pMsg := pack.(MsgBasePack)
		slf.tcpserver.iReciver.OnRecvMsg(slf,&pMsg)
	}
}


func (slf *SClient) Close(){
	if slf.bClose == false {
		slf.conn.Close()
		slf.bClose = true

		slf.recvPack.Close()
		slf.sendPack.Close()
	}
}


func (slf *SClient) GetId() uint64{
	return slf.id
}

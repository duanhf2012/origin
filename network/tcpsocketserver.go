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
	nodelay bool
}

type MsgBasePack struct {
	PackSize uint16
	PackType uint16
	Body []byte
	StartTime time.Time
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
	slf.nodelay = true

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

		if slf.nodelay {
			//conn.(ifaceSetNoDelay)
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

func (slf *TcpSocketServer)  Close(clientid uint64) error {
	pClient := slf.mapClient.Get(clientid)
	if pClient == nil {
		return fmt.Errorf("clientid %d is not in connect pool.",clientid)
	}

	pClient.(*SClient).Close()
	return nil
}

func (slf *TcpSocketServer)  SendMsg(clientid uint64,packtype uint16,message proto.Message) error{
	pClient := slf.mapClient.Get(clientid)
	if pClient == nil {
		return fmt.Errorf("clientid %d is not in connect pool.",clientid)
	}

	return pClient.(*SClient).SendMsg(packtype,message)
}

func (slf *TcpSocketServer) Send(clientid uint64,pack *MsgBasePack) error{
	pClient := slf.mapClient.Get(clientid)
	if pClient == nil {
		return fmt.Errorf("clientid %d is not in connect pool.",clientid)
	}

	return pClient.(*SClient).Send(pack)
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
			if pack.PackSize>slf.tcpserver.MaxRecvPackSize {
				service.GetLogger().Printf(service.LEVER_WARN, "VerifyPackType error clent id %d is disconnect  %d,%d",slf.id,pack.PackType, pack.PackSize)
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
	binary.BigEndian.PutUint16(bRet,slf.PackSize)
	binary.BigEndian.PutUint16(bRet[2:],slf.PackType)
	bRet = append(bRet,slf.Body...)

	return bRet
}

//返回值：填充多少字节，是否完成,是否填充头
func (slf *MsgBasePack) FillData(bdata []byte,datasize uint16) (uint16,bool,bool) {
	var fillsize uint16
	fillhead := false
	//解包头
	if slf.PackSize ==0 {
		if datasize < 4 {
			return 0,false,fillhead
		}

		slf.PackSize= binary.BigEndian.Uint16(bdata[:2])
		slf.PackType= binary.BigEndian.Uint16(bdata[2:4])
		fillsize += 4
		fillhead = true
	}

	//解包体
	if slf.PackSize>0 && datasize+4>=slf.PackSize {
		slf.Body = append(slf.Body, bdata[fillsize:slf.PackSize]...)
		fillsize += (slf.PackSize - fillsize)
		return fillsize,true,fillhead
	}

	return fillsize,false,fillhead
}


func (slf *MsgBasePack) Clear() {
}

func (slf *MsgBasePack) Make(packtype uint16,data []byte) {
	slf.PackType = packtype
	slf.Body = data
	slf.PackSize = uint16(unsafe.Sizeof(slf.PackType)*2)+uint16(len(data))
}

func (slf *SClient) Send(pack *MsgBasePack) error {
	if slf.bClose == true {
		return fmt.Errorf("client id %d is close!",slf.id)
	}

	slf.sendPack.Push(pack)

	return nil
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
		_,e := slf.conn.Write(pPackData.Bytes())
		if e!=nil {
			service.GetLogger().Printf(service.LEVER_DEBUG, "clent id %d write error...",slf.id)
			return
		}
		//fmt.Print("xxxxxxxxxxxxxxx:",n,e)
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

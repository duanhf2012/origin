package network

import (
	"fmt"
	"github.com/golang/protobuf/proto"
	"net"
)

type TcpSocketClient struct {
	conn net.Conn
}

func (slf *TcpSocketClient) Connect(addr string) error{
	tcpAddr,terr := net.ResolveTCPAddr("tcp",addr)
	if terr != nil {
		return terr
	}

	conn,err := net.DialTCP("tcp",nil,tcpAddr)
	if err!=nil {
		fmt.Println("Client connect error ! " + err.Error())
		return err
	}
	slf.conn = conn

	//
	return nil
}

func (slf *TcpSocketClient) SendMsg(packtype uint16,message proto.Message) error{
	var msg MsgBasePack
	data,err := proto.Marshal(message)
	if err != nil {
		return err
	}

	msg.Make(packtype,data)

	slf.conn.Write(msg.Bytes())

	return nil
}

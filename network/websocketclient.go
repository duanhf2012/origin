package network

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"runtime/debug"

	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysmodule"

	"github.com/gorilla/websocket"

	"time"
)

//IWebsocketClient ...
type IWebsocketClient interface {
	Init(slf IWebsocketClient, strurl, strProxyPath string, timeoutsec time.Duration) error
	Start() error
	WriteMessage(msg []byte) error
	OnDisconnect() error
	OnConnected() error
	OnReadMessage(msg []byte) error
	ReConnect()
}

//WebsocketClient ...
type WebsocketClient struct {
	WsDailer   *websocket.Dialer
	conn       *websocket.Conn
	url        string
	state      int //0未连接状态   1正在重连   2连接状态
	bwritemsg  chan []byte
	closer     chan bool
	slf        IWebsocketClient
	timeoutsec time.Duration

	bRun bool
	ping []byte
}

const (
	MAX_WRITE_MSG = 10240
)

//Init ...
func (ws *WebsocketClient) Init(slf IWebsocketClient, strurl, strProxyPath string, timeoutsec time.Duration) error {

	ws.timeoutsec = timeoutsec
	ws.slf = slf
	if strProxyPath != "" {
		proxy := func(_ *http.Request) (*url.URL, error) {
			return url.Parse(strProxyPath)
		}

		if timeoutsec > 0 {
			tosec := timeoutsec * time.Second
			ws.WsDailer = &websocket.Dialer{Proxy: proxy, HandshakeTimeout: tosec}
		} else {
			ws.WsDailer = &websocket.Dialer{Proxy: proxy}
		}
	} else {
		if timeoutsec > 0 {
			tosec := timeoutsec * time.Second
			ws.WsDailer = &websocket.Dialer{HandshakeTimeout: tosec}
		} else {
			ws.WsDailer = &websocket.Dialer{}
		}
	}

	ws.url = strurl
	ws.ping = []byte(`ping`)
	return nil
}

func (ws *WebsocketClient) SetPing(ping string) {
	ws.ping = []byte(ping)
}

//OnRun ...
func (ws *WebsocketClient) OnRun() error {
	defer func() {
		if r := recover(); r != nil {
			coreInfo := string(debug.Stack())
			coreInfo += "\n" + fmt.Sprintf("Core WebsocketClient url is %s. Core information is %v\n", ws.url, r)
			service.GetLogger().Printf(service.LEVER_FATAL, coreInfo)
			go ws.OnRun()
		}
	}()

	for {
		if ws.bRun == false {
			break
		}

		if ws.state == 0 {
			time.Sleep(2 * time.Second)
			ws.StartConnect()
		} else if ws.state == 1 {
			ws.state = 0
			close(ws.closer)
			ws.conn.Close()
			ws.slf.OnDisconnect()
		} else if ws.state == 2 {
			ws.conn.SetReadDeadline(time.Now().Add(ws.timeoutsec * time.Second))
			_, message, err := ws.conn.ReadMessage()

			if err != nil {
				service.GetLogger().Printf(service.LEVER_WARN, "websocket client is disconnect [%s],information is %v", ws.url, err)
				ws.conn.Close()
				ws.state = 0
				close(ws.closer)
				ws.slf.OnDisconnect()
				continue
			}

			ws.slf.OnReadMessage(message)
		}
	}

	return nil
}

//StartConnect ...
func (ws *WebsocketClient) StartConnect() error {

	var err error
	ws.conn, _, err = ws.WsDailer.Dial(ws.url, nil)
	service.GetLogger().Printf(sysmodule.LEVER_INFO, "connecting %s, %+v\n", ws.url, err)
	if err != nil {
		return err
	}
	ws.closer = make(chan bool)
	ws.bwritemsg = make(chan []byte, MAX_WRITE_MSG)
	ws.state = 2
	ws.slf.OnConnected()

	return nil
}

//Start ...
func (ws *WebsocketClient) Start() error {

	if ws.bRun == false {
		ws.bRun = true
		ws.state = 0
		go ws.OnRun()
		go ws.writeMsg()
	}
	return nil
}

//触发
func (ws *WebsocketClient) writeMsg() error {
	//dump处理
	defer func() {
		if r := recover(); r != nil {
			coreInfo := string(debug.Stack())
			coreInfo += "\n" + fmt.Sprintf("Core WebsocketClient url is %s. Core information is %v\n", ws.url, r)
			service.GetLogger().Printf(service.LEVER_FATAL, coreInfo)
			go ws.writeMsg()
		}
	}()

	timerC := time.NewTicker(time.Second * 5).C
	for {
		if ws.bRun == false {
			break
		}

		if ws.state == 0 {
			time.Sleep(1 * time.Second)
			continue
		}
		select {
		case _, ok := <-ws.closer:
			if ok == false {
				break
			}

		case <-timerC:
			if ws.state == 2 {
				err := ws.WriteMessage(ws.ping)
				if err != nil {
					service.GetLogger().Printf(service.LEVER_WARN, "websocket client is disconnect [%s],information is %v", ws.url, err)
					ws.state = 0
					ws.conn.Close()
					ws.slf.OnDisconnect()
				}
			}
		case msg, ok := <-ws.bwritemsg:
			if ok == true && ws.state == 2 {
				ws.conn.SetWriteDeadline(time.Now().Add(ws.timeoutsec * time.Second))
				err := ws.conn.WriteMessage(websocket.TextMessage, msg)
				if err != nil {
					service.GetLogger().Printf(service.LEVER_WARN, "websocket client is disconnect [%s],information is %v", ws.url, err)
					ws.state = 0
					ws.conn.Close()
					ws.slf.OnDisconnect()
				}
			}
		}
	}

	return nil
}

//ReConnect ...
func (ws *WebsocketClient) ReConnect() {
	ws.state = 1
}

//WriteMessage ...
func (ws *WebsocketClient) WriteMessage(msg []byte) error {
	if ws.closer == nil || ws.bwritemsg == nil {
		service.GetLogger().Printf(service.LEVER_WARN, "WriteMessage data fail,websocket client is disconnect.")
		return errors.New("riteMessage data fail,websocket client is disconnect.")
	}
	select {
	case <-ws.closer:
		service.GetLogger().Printf(service.LEVER_WARN, "WriteMessage data fail,websocket client is disconnect.")
		return errors.New("riteMessage data fail,websocket client is disconnect.")
	default:
		if len(ws.bwritemsg) < MAX_WRITE_MSG {
			ws.bwritemsg <- msg
		} else {
			service.GetLogger().Printf(service.LEVER_ERROR, "WriteMessage data fail,bwriteMsg is overload.")
			return errors.New("WriteMessage data fail,bwriteMsg is overload.")
		}

	}

	return nil
}

//OnDisconnect ...
func (ws *WebsocketClient) OnDisconnect() error {

	return nil
}

//OnConnected ...
func (ws *WebsocketClient) OnConnected() error {

	return nil
}

//OnReadMessage 触发
func (ws *WebsocketClient) OnReadMessage(msg []byte) error {

	return nil
}

//Stop ...
func (ws *WebsocketClient) Stop() {
	ws.bRun = false
}

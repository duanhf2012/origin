package rpc

import "sync/atomic"

type RpcNats struct {
	NatsServer
	NatsClient
}

func (rn *RpcNats) Start() error{
	err := rn.NatsServer.Start()
	if err != nil {
		return err
	}

	return rn.NatsClient.Start(rn.NatsServer.natsConn)
}

func (rn *RpcNats) Init(natsUrl string, noRandomize bool, nodeId string,compressBytesLen int,rpcHandleFinder RpcHandleFinder){
	rn.NatsClient.localNodeId = nodeId
	rn.NatsServer.initServer(natsUrl,noRandomize, nodeId,compressBytesLen,rpcHandleFinder)
	rn.NatsServer.iServer = rn
}

func  (rn *RpcNats) NewNatsClient(targetNodeId string,localNodeId string,callSet *CallSet) *Client{
	var client Client

	client.clientId = atomic.AddUint32(&clientSeq, 1)
	client.targetNodeId = targetNodeId
	//client.maxCheckCallRpcCount = DefaultMaxCheckCallRpcCount
	//client.callRpcTimeout = DefaultRpcTimeout

	natsClient := &rn.NatsClient
	natsClient.localNodeId = localNodeId
	natsClient.client = &client

	client.IRealClient = natsClient
	client.CallSet = callSet

	return &client
}
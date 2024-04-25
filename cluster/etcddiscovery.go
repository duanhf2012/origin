package cluster


import (
	"github.com/duanhf2012/origin/v2/event"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/rpc"
	"github.com/duanhf2012/origin/v2/service"
	"github.com/duanhf2012/origin/v2/util/timer"
	"go.etcd.io/etcd/api/v3/mvccpb"
	"go.etcd.io/etcd/client/v3"
	"google.golang.org/protobuf/proto"
	"time"

	"context"
	"errors"
	"fmt"
	"go.uber.org/zap"
	"path"
	"runtime"
	"strings"
	"sync/atomic"
)

const originDir = "/origin"
const testKey = originDir+"/_inner/_test_7501f3ed-b716-44c2-0090-fc1ed0166d7a"

type etcdClientInfo struct {
	watchKeys []string
	leaseID clientv3.LeaseID
	keepAliveChan <-chan *clientv3.LeaseKeepAliveResponse
}

type EtcdDiscoveryService struct {
	service.Service
	funDelNode FunDelNode
	funSetNode FunSetNode
	localNodeId   string

	byteLocalNodeInfo string
	mapClient map[*clientv3.Client]*etcdClientInfo
	isClose int32
	bRetire bool
	mapDiscoveryNodeId map[string]map[string]struct{} //map[networkName]map[nodeId]
}

func getEtcdDiscovery() (IServiceDiscovery) {
	etcdDiscovery := &EtcdDiscoveryService{}
	return etcdDiscovery
}

func (ed *EtcdDiscoveryService) InitDiscovery(localNodeId string,funDelNode FunDelNode,funSetNode FunSetNode) error {
	ed.localNodeId = localNodeId

	ed.funDelNode = funDelNode
	ed.funSetNode = funSetNode

	return nil
}

const(
	eeGets = 0
	eePut = 1
	eeDelete = 2
)

type etcdDiscoveryEvent struct {
	typ int
	watchKey string
	Kvs []*mvccpb.KeyValue
}

func (ee *etcdDiscoveryEvent) GetEventType() event.EventType{
	return event.Sys_Event_EtcdDiscovery
}

func (ed *EtcdDiscoveryService) OnInit() error {
	ed.mapClient = make(map[*clientv3.Client]*etcdClientInfo,1)
	ed.mapDiscoveryNodeId = make(map[string]map[string]struct{})

	ed.GetEventProcessor().RegEventReceiverFunc(event.Sys_Event_EtcdDiscovery, ed.GetEventHandler(), ed.OnEtcdDiscovery)

	err := ed.marshalNodeInfo()
	if err != nil {
		return err
	}

	etcdDiscoveryCfg := cluster.GetEtcdDiscovery()
	if etcdDiscoveryCfg == nil {
		return errors.New("etcd discovery config is nil.")
	}

	for i:=0;i<len(etcdDiscoveryCfg.EtcdList);i++{
		client, cerr := clientv3.New(clientv3.Config{
			Endpoints:  etcdDiscoveryCfg.EtcdList[i].Endpoints,
			DialTimeout: etcdDiscoveryCfg.DialTimeoutMillisecond,
			Logger: zap.NewNop(),
		})

		if cerr != nil {
			log.Error("etcd discovery init fail",log.ErrorAttr("err",cerr))
			return cerr
		}

		ctx,_:=context.WithTimeout(context.Background(),time.Second*3)
		_,err = client.Leases(ctx)
		if err != nil {
			log.Error("etcd discovery init fail",log.Any("endpoint",etcdDiscoveryCfg.EtcdList[i].Endpoints),log.ErrorAttr("err",err))
			return err
		}

		ec := &etcdClientInfo{}
		for _, networkName := range etcdDiscoveryCfg.EtcdList[i].NetworkName {
			ec.watchKeys = append(ec.watchKeys,fmt.Sprintf("%s/%s",originDir,networkName))
		}

		ed.mapClient[client] = ec
	}

	return nil
}

func (ed *EtcdDiscoveryService) getRegisterKey(watchkey string) string{
	return watchkey+"/"+ed.localNodeId
}

func (ed *EtcdDiscoveryService) registerServiceByClient(client *clientv3.Client,etcdClient *etcdClientInfo) {
	// 创建租约
	var err error
	var resp *clientv3.LeaseGrantResponse
	resp, err = client.Grant(context.Background(), cluster.GetEtcdDiscovery().TTLSecond)
	if err != nil {
		log.Error("etcd registerService fail",log.ErrorAttr("err",err))
		ed.tryRegisterService(client,etcdClient)
		return
	}

	etcdClient.leaseID = resp.ID
	for _,watchKey:= range etcdClient.watchKeys {
		// 注册服务节点到 etcd
		_, err = client.Put(context.Background(), ed.getRegisterKey(watchKey), ed.byteLocalNodeInfo, clientv3.WithLease(resp.ID))
		if err != nil {
			log.Error("etcd Put fail",log.ErrorAttr("err",err))
			ed.tryRegisterService(client,etcdClient)
			return
		}
	}

	etcdClient.keepAliveChan,err = client.KeepAlive(context.Background(), etcdClient.leaseID)
	if err != nil {
		log.Error("etcd KeepAlive fail",log.ErrorAttr("err",err))
		ed.tryRegisterService(client,etcdClient)
		return
	}

	go func() {
		for {
			select {
			case _, ok := <-etcdClient.keepAliveChan:
				//log.Debug("ok",log.Any("addr",client.Endpoints()))
				if !ok {
					log.Error("etcd keepAliveChan fail",log.Any("watchKeys",etcdClient.watchKeys))
					ed.tryRegisterService(client,etcdClient)
					return
				}
			}
		}
	}()
}


func (ed *EtcdDiscoveryService) tryRegisterService(client *clientv3.Client,etcdClient *etcdClientInfo){
	if ed.isStop() {
		return
	}

	ed.AfterFunc(time.Second*3, func(t *timer.Timer) {
		ed.registerServiceByClient(client,etcdClient)
	})
}

func (ed *EtcdDiscoveryService) tryWatch(client *clientv3.Client,etcdClient *etcdClientInfo) {
	if ed.isStop() {
		return
	}
	ed.AfterFunc(time.Second*3, func(t *timer.Timer) {
		ed.watchByClient(client,etcdClient)
	})
}

func (ed *EtcdDiscoveryService) tryLaterRetire() {
	ed.AfterFunc(time.Second, func(*timer.Timer) {
		if ed.retire() != nil {
			ed.tryLaterRetire()
		}
	})
}

func (ed *EtcdDiscoveryService) retire() error{
	//从etcd中更新
	for c,ec := range ed.mapClient {
		for _, watchKey := range ec.watchKeys {
			// 注册服务节点到 etcd
			_, err := c.Put(context.Background(), ed.getRegisterKey(watchKey), ed.byteLocalNodeInfo, clientv3.WithLease(ec.leaseID))
			if err != nil {
				log.Error("etcd Put fail", log.ErrorAttr("err", err))
				return err
			}
		}
	}

	return nil
}

func (ed *EtcdDiscoveryService) OnRetire(){
	ed.bRetire = true
	ed.marshalNodeInfo()

	if ed.retire()!= nil {
		ed.tryLaterRetire()
	}
}

func (ed *EtcdDiscoveryService) OnRelease(){
	atomic.StoreInt32(&ed.isClose,1)
	ed.close()
}

func (ed *EtcdDiscoveryService) isStop() bool{
	return atomic.LoadInt32(&ed.isClose) == 1
}

func (nd *EtcdDiscoveryService) OnStart() {
	for c, ec := range nd.mapClient {
		nd.tryRegisterService(c,ec)
		nd.tryWatch(c,ec)
	}
}

func (ed *EtcdDiscoveryService) marshalNodeInfo() error{
	nInfo := cluster.GetLocalNodeInfo()
	var nodeInfo rpc.NodeInfo
	nodeInfo.NodeId = nInfo.NodeId
	nodeInfo.ListenAddr = nInfo.ListenAddr
	nodeInfo.Retire = ed.bRetire
	nodeInfo.PublicServiceList = nInfo.PublicServiceList
	nodeInfo.MaxRpcParamLen = nInfo.MaxRpcParamLen

	byteLocalNodeInfo,err := proto.Marshal(&nodeInfo)
	if err ==nil{
		ed.byteLocalNodeInfo = string(byteLocalNodeInfo)
	}

	return err
}

func (ed *EtcdDiscoveryService) setNodeInfo(networkName string,nodeInfo *rpc.NodeInfo) bool{
	if nodeInfo == nil || nodeInfo.Private == true || nodeInfo.NodeId == ed.localNodeId {
		return false
	}

	//筛选关注的服务
	var discoverServiceSlice = make([]string, 0, 24)
	for _, pubService := range nodeInfo.PublicServiceList {
		if cluster.CanDiscoveryService(networkName,pubService) == true {
			discoverServiceSlice = append(discoverServiceSlice,pubService)
		}
	}

	if len(discoverServiceSlice) == 0 {
		return false
	}

	var nInfo NodeInfo
	nInfo.ServiceList = discoverServiceSlice
	nInfo.PublicServiceList = discoverServiceSlice
	nInfo.NodeId = nodeInfo.NodeId
	nInfo.ListenAddr = nodeInfo.ListenAddr
	nInfo.MaxRpcParamLen = nodeInfo.MaxRpcParamLen
	nInfo.Retire = nodeInfo.Retire
	nInfo.Private = nodeInfo.Private

	ed.funSetNode(&nInfo)

	return true
}

func (ed *EtcdDiscoveryService) close(){
	for c, ec := range ed.mapClient {
		if _, err := c.Revoke(context.Background(), ec.leaseID); err != nil {
			log.Error("etcd Revoke fail",log.ErrorAttr("err",err))
		}
		c.Watcher.Close()
		err := c.Close()
		if err != nil {
			log.Error("etcd Close fail",log.ErrorAttr("err",err))
		}
	}
}

func (ed *EtcdDiscoveryService) getServices(client *clientv3.Client,etcdClient *etcdClientInfo,watchKey string) bool {
	// 根据前缀获取现有的key
	resp, err := client.Get(context.Background(), watchKey, clientv3.WithPrefix())
	if err != nil {
		log.Error("etcd Get fail", log.ErrorAttr("err", err))
		ed.tryWatch(client, etcdClient)
		return false
	}

	// 遍历获取得到的k和v
	ed.notifyGets(watchKey,resp.Kvs)

	return true
}

func (ed *EtcdDiscoveryService) watchByClient(client *clientv3.Client,etcdClient *etcdClientInfo){
	//先关闭所有的watcher
	for _, watchKey := range etcdClient.watchKeys {
		// 监视前缀，修改变更server
		go ed.watcher(client,etcdClient, watchKey)
	}
}

// watcher 监听Key的前缀
func (ed *EtcdDiscoveryService) watcher(client *clientv3.Client,etcdClient *etcdClientInfo,watchKey string) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			log.Dump(string(buf[:l]),log.String("error",errString))

			ed.tryWatch(client,etcdClient)
		}
	}()

	rch := client.Watch(context.Background(), watchKey, clientv3.WithPrefix())

	if ed.getServices(client,etcdClient,watchKey) == false {
		return
	}

	for wresp := range rch {
		for _, ev := range wresp.Events {
			switch ev.Type {
			case clientv3.EventTypePut: // 修改或者新增
				ed.notifyPut(watchKey,ev.Kv)
			case clientv3.EventTypeDelete: // 删除
				ed.notifyDelete(watchKey,ev.Kv)
			}
		}
	}
	
	ed.tryWatch(client,etcdClient)
}

func (ed *EtcdDiscoveryService) setNode(netWorkName string,byteNode []byte) string{
	var nodeInfo rpc.NodeInfo
	err := proto.Unmarshal(byteNode,&nodeInfo)
	if err != nil {
		log.Error("Unmarshal fail",log.String("netWorkName",netWorkName),log.ErrorAttr("err",err))
		return ""
	}

	ed.setNodeInfo(netWorkName,&nodeInfo)

	return nodeInfo.NodeId
}

func (ed *EtcdDiscoveryService) delNode(fullKey string) string{
	nodeId := ed.getNodeId(fullKey)
	if nodeId == ed.localNodeId {
		return ""
	}

	 ed.funDelNode(nodeId)
	return nodeId
}

func (ed *EtcdDiscoveryService) getNetworkNameByWatchKey(watchKey string) string{
	return watchKey[strings.LastIndex(watchKey,"/")+1:]
}

func (ed *EtcdDiscoveryService) getNetworkNameByFullKey(fullKey string) string{
	return fullKey[len(originDir)+1:strings.LastIndex(fullKey,"/")]
}

func (ed *EtcdDiscoveryService) getNodeId(fullKey string) string{
	return fullKey[strings.LastIndex(fullKey,"/")+1:]
}

func (ed *EtcdDiscoveryService) OnEtcdDiscovery(ev event.IEvent){
	disEvent := ev.(*etcdDiscoveryEvent)
	switch disEvent.typ {
	case eeGets:
		ed.OnEventGets(disEvent.watchKey,disEvent.Kvs)
	case eePut:
		if len(disEvent.Kvs) == 1 {
			ed.OnEventPut(disEvent.watchKey,disEvent.Kvs[0])
		}
	case eeDelete:
		if len(disEvent.Kvs) == 1 {
			ed.OnEventDelete(disEvent.watchKey,disEvent.Kvs[0])
		}
	}
}

func (ed *EtcdDiscoveryService) notifyGets(watchKey string,Kvs []*mvccpb.KeyValue) {
	var ev etcdDiscoveryEvent
	ev.typ = eeGets
	ev.watchKey = watchKey
	ev.Kvs = Kvs
	ed.NotifyEvent(&ev)
}

func (ed *EtcdDiscoveryService) notifyPut(watchKey string,Kvs *mvccpb.KeyValue) {
	var ev etcdDiscoveryEvent
	ev.typ = eePut
	ev.watchKey = watchKey
	ev.Kvs = append(ev.Kvs,Kvs)
	ed.NotifyEvent(&ev)
}

func (ed *EtcdDiscoveryService) notifyDelete(watchKey string,Kvs *mvccpb.KeyValue) {
	var ev etcdDiscoveryEvent
	ev.typ = eeDelete
	ev.watchKey = watchKey
	ev.Kvs = append(ev.Kvs,Kvs)
	ed.NotifyEvent(&ev)
}

func (ed *EtcdDiscoveryService) OnEventGets(watchKey string,Kvs []*mvccpb.KeyValue) {
	mapNode := make(map[string]struct{},32)
	for _, kv := range Kvs {
		nodeId := ed.setNode(ed.getNetworkNameByFullKey(string(kv.Key)), kv.Value)
		mapNode[nodeId] = struct{}{}
		ed.addNodeId(watchKey,nodeId)
	}
	
	// 此段代码为遍历并删除过期节点的逻辑。
	// 对于mapDiscoveryNodeId中与watchKey关联的所有节点ID，遍历该集合。
	// 如果某个节点ID不在mapNode中且不是本地节点ID，则调用funDelNode函数删除该节点。
	mapLastNodeId := ed.mapDiscoveryNodeId[watchKey] // 根据watchKey获取对应的节点ID集合
	for nodeId := range mapLastNodeId { // 遍历所有节点ID
	    if _,ok := mapNode[nodeId];ok == false && nodeId != ed.localNodeId { // 检查节点是否不存在于mapNode且不是本地节点
	        ed.funDelNode(nodeId) // 调用函数删除该节点
			delete(ed.mapDiscoveryNodeId[watchKey],nodeId)
	    }
	}
}

func (ed *EtcdDiscoveryService) OnEventPut(watchKey string,Kv *mvccpb.KeyValue) {
	nodeId := ed.setNode(ed.getNetworkNameByFullKey(string(Kv.Key)), Kv.Value)
	ed.addNodeId(watchKey,nodeId)
}

func (ed *EtcdDiscoveryService) OnEventDelete(watchKey string,Kv *mvccpb.KeyValue) {
	nodeId := ed.delNode(string(Kv.Key))
	delete(ed.mapDiscoveryNodeId[watchKey],nodeId)
}

func (ed *EtcdDiscoveryService) addNodeId(watchKey string,nodeId string) {
	if _,ok := ed.mapDiscoveryNodeId[watchKey];ok == false {
		ed.mapDiscoveryNodeId[watchKey] = make(map[string]struct{})
	}

	ed.mapDiscoveryNodeId[watchKey][nodeId] = struct{}{}
}

func (ed *EtcdDiscoveryService) OnNodeDisconnect(nodeId string) {
	//将Discard结点清理
	cluster.DiscardNode(nodeId)
}

func (ed *EtcdDiscoveryService) RPC_ServiceRecord(etcdServiceRecord *service.EtcdServiceRecordEvent,empty *service.Empty) error{
		var client *clientv3.Client

		//写入到etcd中
		for c, info := range ed.mapClient{
			for _,watchKey := range info.watchKeys {
				if ed.getNetworkNameByWatchKey(watchKey) == etcdServiceRecord.NetworkName {
					client = c
					break
				}
			}
		}

		if client == nil {
			log.Error("etcd record fail,cannot find network name",log.String("networkName",etcdServiceRecord.NetworkName))
			return errors.New("annot find network name")
		}

		var lg *clientv3.LeaseGrantResponse
		var err error

		if etcdServiceRecord.TTLSecond > 0 {
			ctx,_:=context.WithTimeout(context.Background(),time.Second*3)
			lg, err = client.Grant(ctx, etcdServiceRecord.TTLSecond)
			if err != nil {
				log.Error("etcd record fail,cannot grant lease",log.ErrorAttr("err",err))
				return errors.New("cannot grant lease")
			}
		}

		if lg != nil {
			ctx,_:=context.WithTimeout(context.Background(),time.Second*3)
			_, err = client.Put(ctx, path.Join(originDir,etcdServiceRecord.RecordKey),etcdServiceRecord.RecordInfo, clientv3.WithLease(lg.ID))
			if err != nil {
				log.Error("etcd record fail,cannot put record",log.ErrorAttr("err",err))
			}
			return errors.New("cannot put record")
		}

		_,err = client.Put(context.Background(), path.Join(originDir,etcdServiceRecord.RecordKey),etcdServiceRecord.RecordInfo)
		if err != nil {
			log.Error("etcd record fail,cannot put record",log.ErrorAttr("err",err))
			return errors.New("cannot put record")
		}

		return nil
}

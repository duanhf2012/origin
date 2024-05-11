package cluster

import (
	"fmt"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/rpc"
	jsoniter "github.com/json-iterator/go"
	"os"
	"path/filepath"
	"strings"
	"time"
	"errors"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

type EtcdList struct {
	NetworkName []string
	Endpoints []string
}

type EtcdDiscovery struct {
	DialTimeoutMillisecond time.Duration
	TTLSecond int64

	EtcdList []EtcdList
}

type OriginDiscovery struct {
	TTLSecond int64
	MasterNodeList []NodeInfo
}

type DiscoveryType int

const (
	InvalidType = 0
	OriginType = 1
	EtcdType = 2
)

const MinTTL = 3

type DiscoveryInfo struct {
	discoveryType DiscoveryType
	Etcd          *EtcdDiscovery  //etcd
	Origin        *OriginDiscovery      //orign
}

type NatsConfig struct {
	NatsUrl string
	NoRandomize bool
}

type RpcMode struct {
	Typ string `json:"Type"`
	Nats  NatsConfig

}

type NodeInfoList struct {
	RpcMode             RpcMode
	Discovery 			DiscoveryInfo
	NodeList            []NodeInfo
}

func (d *DiscoveryInfo) getDiscoveryType() DiscoveryType{
	return d.discoveryType
}

func (d *DiscoveryInfo) setDiscovery(discoveryInfo *DiscoveryInfo) error{
	var err error
	err = d.setOrigin(discoveryInfo.Origin)
	if err != nil {
		return err
	}

	err = d.setEtcd(discoveryInfo.Etcd)
	if err != nil {
		return err
	}

	return nil
}

func (d *DiscoveryInfo) setEtcd(etcd *EtcdDiscovery) error{
	if etcd == nil {
		return nil
	}

	if  d.discoveryType != InvalidType {
		return fmt.Errorf("Repeat configuration of Discovery")
	}

	//Endpoints不允许重复
	mapAddr:=make (map[string]struct{})
	for _, n := range etcd.EtcdList {
		for _,endPoint := range n.Endpoints {
			if _,ok:=mapAddr[endPoint];ok == true {
				return fmt.Errorf("etcd discovery config Etcd.EtcdList.Endpoints %+v is repeat",endPoint)
			}
			mapAddr[endPoint] = struct{}{}
		}

		//networkName不允许重复
		mapNetworkName := make(map[string]struct{})
		for _,netName := range n.NetworkName{
			if _,ok := mapNetworkName[netName];ok == true {
				return fmt.Errorf("etcd discovery config Etcd.EtcdList.NetworkName %+v is repeat",n.NetworkName)
			}

			mapNetworkName[netName] = struct{}{}
		}
	}

	if etcd.TTLSecond < MinTTL {
		etcd.TTLSecond = MinTTL
	}
	etcd.DialTimeoutMillisecond = etcd.DialTimeoutMillisecond * time.Millisecond

	d.Etcd = etcd
	d.discoveryType = EtcdType

	return nil
}

func (d *DiscoveryInfo) setOrigin(originDiscovery *OriginDiscovery) error{
	if originDiscovery== nil || len(originDiscovery.MasterNodeList)==0 {
		return nil
	}

	if d.discoveryType != InvalidType {
		return fmt.Errorf("Repeat configuration of Discovery")
	}

	mapListenAddr := make(map[string]struct{})
	mapNodeId := make(map[string]struct{})
	for _, n := range originDiscovery.MasterNodeList {
		if _, ok := mapListenAddr[n.ListenAddr]; ok == true {
			return fmt.Errorf("discovery config Origin.ListenAddr %s is repeat", n.ListenAddr)
		}
		mapListenAddr[n.ListenAddr] = struct{}{}

		if _, ok := mapNodeId[n.NodeId]; ok == true {
			return fmt.Errorf("discovery config Origin.NodeId %s is repeat", n.NodeId)
		}
		mapNodeId[n.NodeId] = struct{}{}
	}

	d.Origin = originDiscovery
	if d.Origin.TTLSecond < MinTTL {
		d.Origin.TTLSecond = MinTTL
	}
	d.discoveryType = OriginType
	return nil
}

func (cls *Cluster) ReadClusterConfig(filepath string) (*NodeInfoList, error) {
	c := &NodeInfoList{}
	d, err := os.ReadFile(filepath)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(d, c)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (cls *Cluster) readServiceConfig(filepath string) (interface{}, map[string]interface{}, map[string]map[string]interface{}, error) {
	c := map[string]interface{}{}
	//读取配置
	d, err := os.ReadFile(filepath)
	if err != nil {
		return nil, nil, nil, err
	}
	err = json.Unmarshal(d, &c)
	if err != nil {
		return nil, nil, nil, err
	}

	GlobalCfg, ok := c["Global"]
	serviceConfig := map[string]interface{}{}
	serviceCfg, ok := c["Service"]
	if ok == true {
		serviceConfig = serviceCfg.(map[string]interface{})
	}

	mapNodeService := map[string]map[string]interface{}{}
	nodeServiceCfg, ok := c["NodeService"]
	if ok == true {
		nodeServiceList := nodeServiceCfg.([]interface{})
		for _, v := range nodeServiceList {
			serviceCfg := v.(map[string]interface{})
			nodeId, ok := serviceCfg["NodeId"]
			if ok == false {
				log.Fatal("NodeService list not find nodeId field")
			}
			mapNodeService[nodeId.(string)] = serviceCfg
		}
	}
	return GlobalCfg, serviceConfig, mapNodeService, nil
}

func (cls *Cluster) SetRpcMode(cfgRpcMode *RpcMode,rpcMode *RpcMode) error {
	//忽略掉没有设置的配置
	if cfgRpcMode.Typ == "" {
		return nil
	}

	//不允许重复的配置Rpc模式

	if cfgRpcMode.Typ != "" && rpcMode.Typ != ""{
		return errors.New("repeat config RpcMode")
	}

	//检查Typ是否合法
	if cfgRpcMode.Typ!="Nats" && cfgRpcMode.Typ!="Default" {
		return fmt.Errorf("RpcMode %s is not support", rpcMode.Typ)
	}

	if cfgRpcMode.Typ == "Nats" && len(cfgRpcMode.Nats.NatsUrl)==0 {
		return fmt.Errorf("nats rpc mode config NatsUrl is empty")
	}

	*rpcMode = *cfgRpcMode

	return nil
}

func (cls *Cluster) readLocalClusterConfig(nodeId string) (DiscoveryInfo, []NodeInfo,RpcMode, error) {
	var nodeInfoList []NodeInfo
	var discoveryInfo DiscoveryInfo
	var rpcMode RpcMode

	clusterCfgPath := strings.TrimRight(configDir, "/") + "/cluster"
	fileInfoList, err := os.ReadDir(clusterCfgPath)
	if err != nil {
		return discoveryInfo, nil,rpcMode, fmt.Errorf("Read dir %s is fail :%+v", clusterCfgPath, err)
	}

	//读取任何文件,只读符合格式的配置,目录下的文件可以自定义分文件
	for _, f := range fileInfoList {
		if f.IsDir() == false {
			filePath := strings.TrimRight(strings.TrimRight(clusterCfgPath, "/"), "\\") + "/" + f.Name()
			fileNodeInfoList, rerr := cls.ReadClusterConfig(filePath)

			if rerr != nil {
				return discoveryInfo, nil,rpcMode, fmt.Errorf("read file path %s is error:%+v", filePath, rerr)
			}

			err = cls.SetRpcMode(&fileNodeInfoList.RpcMode,&rpcMode)
			if err != nil {
				return discoveryInfo, nil,rpcMode, err
			}

			err = discoveryInfo.setDiscovery(&fileNodeInfoList.Discovery)
			if err != nil {
				return  discoveryInfo,nil,rpcMode,err
			}

			for _, nodeInfo := range fileNodeInfoList.NodeList {
				if nodeInfo.NodeId == nodeId || nodeId == rpc.NodeIdNull {
					nodeInfoList = append(nodeInfoList, nodeInfo)
				}
			}
		}
	}

	if nodeId != rpc.NodeIdNull && (len(nodeInfoList) != 1) {
		return discoveryInfo, nil,rpcMode, fmt.Errorf("nodeid %s configuration error in NodeList", nodeId)
	}

	for i, _ := range nodeInfoList {
		for j, s := range nodeInfoList[i].ServiceList {
			//私有结点不加入到Public服务列表中
			if strings.HasPrefix(s, "_") == false && nodeInfoList[i].Private == false {
				nodeInfoList[i].PublicServiceList = append(nodeInfoList[i].PublicServiceList, strings.TrimLeft(s, "_"))
			} else {
				nodeInfoList[i].ServiceList[j] = strings.TrimLeft(s, "_")
			}
		}
	}

	return discoveryInfo, nodeInfoList, rpcMode,nil
}

func (cls *Cluster) readLocalService(localNodeId string) error {
	clusterCfgPath := strings.TrimRight(configDir, "/") + "/cluster"
	fileInfoList, err := os.ReadDir(clusterCfgPath)
	if err != nil {
		return fmt.Errorf("Read dir %s is fail :%+v", clusterCfgPath, err)
	}

	var globalCfg  interface{}
	publicService := map[string]interface{}{}
	nodeService := map[string]interface{}{}

	//读取任何文件,只读符合格式的配置,目录下的文件可以自定义分文件
	for _, f := range fileInfoList {
		if f.IsDir() == true {
			continue
		}

		if filepath.Ext(f.Name())!= ".json" {
			continue
		}

		filePath := strings.TrimRight(strings.TrimRight(clusterCfgPath, "/"), "\\") + "/" + f.Name()
			currGlobalCfg, serviceConfig, mapNodeService, err := cls.readServiceConfig(filePath)
		if err != nil {
			continue
		}

		if currGlobalCfg != nil {
			//不允许重复的配置global配置
			if globalCfg != nil {
				return fmt.Errorf("[Global] does not allow repeated configuration in %s.",f.Name())
			}
			globalCfg = currGlobalCfg
		}

		//保存公共配置
		for _, s := range cls.localNodeInfo.ServiceList {
			for {
				splitServiceName := strings.Split(s,":")
				if len(splitServiceName) == 2 {
					s = splitServiceName[0]
				}
				//取公共服务配置
				pubCfg, ok := serviceConfig[s]
				if ok == true {
					if _,publicOk := publicService[s];publicOk == true {
						return fmt.Errorf("public service [%s] does not allow repeated configuration in %s.",s,f.Name())
					}
					publicService[s] = pubCfg
				}

				//取指定结点配置的服务
				nodeServiceCfg,ok := mapNodeService[localNodeId]
				if ok == false {
					break
				}
				nodeCfg, ok := nodeServiceCfg[s]
				if ok == false {
					break
				}

				if _,nodeOK := nodeService[s];nodeOK == true {
					return fmt.Errorf("NodeService NodeId[%d] Service[%s] does not allow repeated configuration in %s.",cls.localNodeInfo.NodeId,s,f.Name())
				}
				nodeService[s] = nodeCfg
				break
			}
		}
	}

	//组合所有的配置
	for _, s := range cls.localNodeInfo.ServiceList {
		splitServiceName := strings.Split(s,":")
		if len(splitServiceName) == 2 {
			s = splitServiceName[0]
		}

		//先从NodeService中找
		var serviceCfg interface{}
		var ok bool
		serviceCfg,ok = nodeService[s]
		if ok == true {
			cls.localServiceCfg[s] =serviceCfg
			continue
		}

		//如果找不到从PublicService中找
		serviceCfg,ok = publicService[s]
		if ok == true {
			cls.localServiceCfg[s] =serviceCfg
		}
	}
	cls.globalCfg = globalCfg

	return nil
}

func (cls *Cluster) parseLocalCfg() {
	rpcInfo := NodeRpcInfo{}
	rpcInfo.nodeInfo = cls.localNodeInfo
	rpcInfo.client = rpc.NewLClient(rpcInfo.nodeInfo.NodeId,&cls.callSet)

	cls.mapRpc[cls.localNodeInfo.NodeId] = &rpcInfo

	for _, serviceName := range cls.localNodeInfo.ServiceList {
		splitServiceName := strings.Split(serviceName,":")
		if len(splitServiceName) == 2 {
			serviceName = splitServiceName[0]
			templateServiceName := splitServiceName[1]
			//记录模板
			if _, ok := cls.mapTemplateServiceNode[templateServiceName]; ok == false {
				cls.mapTemplateServiceNode[templateServiceName]=map[string]struct{}{}
			}
			cls.mapTemplateServiceNode[templateServiceName][serviceName] = struct{}{}
		}


		if _, ok := cls.mapServiceNode[serviceName]; ok == false {
			cls.mapServiceNode[serviceName] = make(map[string]struct{})
		}

		cls.mapServiceNode[serviceName][cls.localNodeInfo.NodeId] = struct{}{}
	}
}

func (cls *Cluster) IsNatsMode() bool {
	return cls.rpcMode.Typ == "Nats"
}

func (cls *Cluster) GetNatsUrl() string {
	return cls.rpcMode.Nats.NatsUrl
}

func (cls *Cluster) InitCfg(localNodeId string) error {
	cls.localServiceCfg = map[string]interface{}{}
	cls.mapRpc = map[string]*NodeRpcInfo{}
	cls.mapServiceNode = map[string]map[string]struct{}{}
	cls.mapTemplateServiceNode = map[string]map[string]struct{}{}

	//加载本地结点的NodeList配置
	discoveryInfo, nodeInfoList,rpcMode, err := cls.readLocalClusterConfig(localNodeId)
	if err != nil {
		return err
	}
	cls.localNodeInfo = nodeInfoList[0]
	cls.discoveryInfo = discoveryInfo
	cls.rpcMode = rpcMode

	//读取本地服务配置
	err = cls.readLocalService(localNodeId)
	if err != nil {
		return err
	}

	//本地配置服务加到全局map信息中
	cls.parseLocalCfg()
	return nil
}

func (cls *Cluster) IsConfigService(serviceName string) bool {
	cls.locker.RLock()
	defer cls.locker.RUnlock()
	mapNode, ok := cls.mapServiceNode[serviceName]
	if ok == false {
		return false
	}

	_, ok = mapNode[cls.localNodeInfo.NodeId]
	return ok
}

func (cls *Cluster) GetNodeIdByTemplateService(templateServiceName string, rpcClientList []*rpc.Client, filterRetire bool) (error, []*rpc.Client) {
	cls.locker.RLock()
	defer cls.locker.RUnlock()

	mapServiceName := cls.mapTemplateServiceNode[templateServiceName]
	 for serviceName := range mapServiceName {
		 mapNodeId, ok := cls.mapServiceNode[serviceName]
		 if ok == true {
			 for nodeId, _ := range mapNodeId {
				 pClient,retire := GetCluster().getRpcClient(nodeId)
				 if pClient == nil || pClient.IsConnected() == false {
					 continue
				 }

				 //如果需要筛选掉退休的，对retire状态的结点略过
				 if filterRetire == true && retire == true {
					 continue
				 }

				 rpcClientList = append(rpcClientList,pClient)
			 }
		 }
	 }

	return nil, rpcClientList
}

func (cls *Cluster) GetNodeIdByService(serviceName string, rpcClientList []*rpc.Client, filterRetire bool) (error, []*rpc.Client) {
	cls.locker.RLock()
	defer cls.locker.RUnlock()
	mapNodeId, ok := cls.mapServiceNode[serviceName]
	if ok == true {
		for nodeId, _ := range mapNodeId {
			pClient,retire := GetCluster().getRpcClient(nodeId)
			if pClient == nil || pClient.IsConnected() == false {
				continue
			}

			//如果需要筛选掉退休的，对retire状态的结点略过
			if filterRetire == true && retire == true {
				continue
			}

			rpcClientList = append(rpcClientList,pClient)
		}
	}

	return nil, rpcClientList
}

func (cls *Cluster) GetServiceCfg(serviceName string) interface{} {
	serviceCfg, ok := cls.localServiceCfg[serviceName]
	if ok == false {
		return nil
	}

	return serviceCfg
}

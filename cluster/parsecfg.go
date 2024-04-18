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

type DiscoveryType int

const (
	InvalidType = 0
	OriginType = 1
	EtcdType = 2
)

type DiscoveryInfo struct {
	discoveryType DiscoveryType
	Etcd          *EtcdDiscovery  //etcd
	Origin        []NodeInfo      //orign
}

type NodeInfoList struct {
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

	etcd.TTLSecond = etcd.TTLSecond
	etcd.DialTimeoutMillisecond = etcd.DialTimeoutMillisecond * time.Millisecond

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

	d.Etcd = etcd
	d.discoveryType = EtcdType
	return nil
}

func (d *DiscoveryInfo) setOrigin(nodeInfos []NodeInfo) error{
	if nodeInfos == nil {
		return nil
	}

	if d.discoveryType != InvalidType {
		return fmt.Errorf("Repeat configuration of Discovery")
	}

	mapListenAddr := make(map[string]struct{})
	mapNodeId := make(map[string]struct{})
	for _, n := range nodeInfos {
		if _, ok := mapListenAddr[n.ListenAddr]; ok == true {
			return fmt.Errorf("discovery config Origin.ListenAddr %s is repeat", n.ListenAddr)
		}
		mapListenAddr[n.ListenAddr] = struct{}{}

		if _, ok := mapNodeId[n.NodeId]; ok == true {
			return fmt.Errorf("discovery config Origin.NodeId %s is repeat", n.NodeId)
		}
		mapNodeId[n.NodeId] = struct{}{}
	}

	d.Origin = nodeInfos
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

func (cls *Cluster) readLocalClusterConfig(nodeId string) (DiscoveryInfo, []NodeInfo, error) {
	var nodeInfoList []NodeInfo
	var discoveryInfo DiscoveryInfo

	clusterCfgPath := strings.TrimRight(configDir, "/") + "/cluster"
	fileInfoList, err := os.ReadDir(clusterCfgPath)
	if err != nil {
		return discoveryInfo, nil, fmt.Errorf("Read dir %s is fail :%+v", clusterCfgPath, err)
	}

	//读取任何文件,只读符合格式的配置,目录下的文件可以自定义分文件
	for _, f := range fileInfoList {
		if f.IsDir() == false {
			filePath := strings.TrimRight(strings.TrimRight(clusterCfgPath, "/"), "\\") + "/" + f.Name()
			fileNodeInfoList, rerr := cls.ReadClusterConfig(filePath)

			if rerr != nil {
				return discoveryInfo, nil, fmt.Errorf("read file path %s is error:%+v", filePath, rerr)
			}

			err = discoveryInfo.setDiscovery(&fileNodeInfoList.Discovery)
			if err != nil {
				return  discoveryInfo,nil,err
			}

			for _, nodeInfo := range fileNodeInfoList.NodeList {
				if nodeInfo.NodeId == nodeId || nodeId == rpc.NodeIdNull {
					nodeInfoList = append(nodeInfoList, nodeInfo)
				}
			}
		}
	}

	if nodeId != rpc.NodeIdNull && (len(nodeInfoList) != 1) {
		return discoveryInfo, nil, fmt.Errorf("%d configurations were found for the configuration with node ID %d!", len(nodeInfoList), nodeId)
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

	return discoveryInfo, nodeInfoList, nil
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
	rpcInfo.client = rpc.NewLClient(rpcInfo.nodeInfo.NodeId)

	cls.mapRpc[cls.localNodeInfo.NodeId] = &rpcInfo

	for _, sName := range cls.localNodeInfo.ServiceList {
		if _, ok := cls.mapServiceNode[sName]; ok == false {
			cls.mapServiceNode[sName] = make(map[string]struct{})
		}

		cls.mapServiceNode[sName][cls.localNodeInfo.NodeId] = struct{}{}
	}
}

func (cls *Cluster) InitCfg(localNodeId string) error {
	cls.localServiceCfg = map[string]interface{}{}
	cls.mapRpc = map[string]*NodeRpcInfo{}
	cls.mapServiceNode = map[string]map[string]struct{}{}

	//加载本地结点的NodeList配置
	discoveryInfo, nodeInfoList, err := cls.readLocalClusterConfig(localNodeId)
	if err != nil {
		return err
	}
	cls.localNodeInfo = nodeInfoList[0]
	cls.discoveryInfo = discoveryInfo

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


func (cls *Cluster) GetNodeIdByService(serviceName string, rpcClientList []*rpc.Client, filterRetire bool) (error, int) {
	cls.locker.RLock()
	defer cls.locker.RUnlock()
	mapNodeId, ok := cls.mapServiceNode[serviceName]
	count := 0
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

			rpcClientList[count] = pClient
			count++
			if count >= cap(rpcClientList) {
				break
			}
		}
	}

	return nil, count
}

func (cls *Cluster) GetServiceCfg(serviceName string) interface{} {
	serviceCfg, ok := cls.localServiceCfg[serviceName]
	if ok == false {
		return nil
	}

	return serviceCfg
}

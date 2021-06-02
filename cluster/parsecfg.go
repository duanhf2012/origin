package cluster

import (
	"fmt"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/rpc"
	jsoniter "github.com/json-iterator/go"
	"io/ioutil"
	"strings"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary
type NodeInfoList struct {
	MasterDiscoveryNode []NodeInfo  //用于服务发现Node
	NodeList []NodeInfo
}

func (cls *Cluster) ReadClusterConfig(filepath string) (*NodeInfoList,error) {
	c := &NodeInfoList{}
	d, err := ioutil.ReadFile(filepath)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(d, c)
	if err != nil {
		return nil, err
	}

	return c,nil
}

func (cls *Cluster) readServiceConfig(filepath string)  (map[string]interface{},map[int]map[string]interface{},error) {
	c := map[string]interface{}{}
	//读取配置
	d, err := ioutil.ReadFile(filepath)
	if err != nil {
		return nil,nil, err
	}
	err = json.Unmarshal(d, &c)
	if err != nil {
		return nil,nil, err
	}

	serviceConfig := map[string]interface{}{}
	serviceCfg,ok  := c["Service"]
	if ok == true {
		serviceConfig = serviceCfg.(map[string]interface{})
	}

	mapNodeService := map[int]map[string]interface{}{}
	nodeServiceCfg,ok  := c["NodeService"]
	if ok == true {
		nodeServiceList := nodeServiceCfg.([]interface{})
		for _,v := range nodeServiceList{
			serviceCfg :=v.(map[string]interface{})
			nodeId,ok := serviceCfg["NodeId"]
			if ok == false {
				log.Fatal("NodeService list not find nodeId field: %+v",nodeServiceList)
			}
			mapNodeService[int(nodeId.(float64))] = serviceCfg
		}
	}
	return serviceConfig,mapNodeService,nil
}

func (cls *Cluster) readLocalClusterConfig(nodeId int) ([]NodeInfo,[]NodeInfo,error) {
	var nodeInfoList []NodeInfo
	var masterDiscoverNodeList []NodeInfo
	clusterCfgPath :=strings.TrimRight(configDir,"/")  +"/cluster"
	fileInfoList,err := ioutil.ReadDir(clusterCfgPath)
	if err != nil {
		return nil,nil,fmt.Errorf("Read dir %s is fail :%+v",clusterCfgPath,err)
	}

	//读取任何文件,只读符合格式的配置,目录下的文件可以自定义分文件
	for _,f := range fileInfoList{
		if f.IsDir() == false {
			filePath := strings.TrimRight(strings.TrimRight(clusterCfgPath,"/"),"\\")+"/"+f.Name()
			localNodeInfoList,err := cls.ReadClusterConfig(filePath)
			if err != nil {
				return nil,nil,fmt.Errorf("read file path %s is error:%+v" ,filePath,err)
			}
			masterDiscoverNodeList = append(masterDiscoverNodeList,localNodeInfoList.MasterDiscoveryNode...)
			for _,nodeInfo := range localNodeInfoList.NodeList {
				if nodeInfo.NodeId == nodeId || nodeId == 0 {
					nodeInfoList = append(nodeInfoList,nodeInfo)
				}
			}
		}
	}

	if nodeId != 0 &&  (len(nodeInfoList)!=1){
		return nil,nil,fmt.Errorf("%d configurations were found for the configuration with node ID %d!",len(nodeInfoList),nodeId)
	}

	for i,_ := range nodeInfoList{
		for j,s := range nodeInfoList[i].ServiceList{
			//私有结点不加入到Public服务列表中
			if strings.HasPrefix(s,"_") == false && nodeInfoList[i].Private==false {
				nodeInfoList[i].PublicServiceList = append(nodeInfoList[i].PublicServiceList,strings.TrimLeft(s,"_"))
			}else{
				nodeInfoList[i].ServiceList[j] = strings.TrimLeft(s,"_")
			}
		}
	}


	return masterDiscoverNodeList,nodeInfoList,nil
}

func (cls *Cluster) readLocalService(localNodeId int) error {
	clusterCfgPath :=strings.TrimRight(configDir,"/")  +"/cluster"
	fileInfoList,err := ioutil.ReadDir(clusterCfgPath)
	if err != nil {
		return fmt.Errorf("Read dir %s is fail :%+v",clusterCfgPath,err)
	}

	//读取任何文件,只读符合格式的配置,目录下的文件可以自定义分文件
	for _,f := range fileInfoList {
		if f.IsDir() == false {
			filePath := strings.TrimRight(strings.TrimRight(clusterCfgPath, "/"), "\\") + "/" + f.Name()
			serviceConfig,mapNodeService,err := cls.readServiceConfig(filePath)
			if err != nil {
				continue
			}

			for _,s := range cls.localNodeInfo.ServiceList{
				for{
					//取公共服务配置
					pubCfg,ok := serviceConfig[s]
					if ok == true {
						cls.localServiceCfg[s] = pubCfg
					}

					//如果结点也配置了该服务，则覆盖之
					nodeService,ok := mapNodeService[localNodeId]
					if ok == false {
						break
					}
					sCfg,ok := nodeService[s]
					if ok == false{
						break
					}

					cls.localServiceCfg[s] = sCfg
					break
				}
			}
		}
	}

	return nil
}

func (cls *Cluster) parseLocalCfg(){
	cls.mapIdNode[cls.localNodeInfo.NodeId] = cls.localNodeInfo

	for _,sName := range cls.localNodeInfo.ServiceList{
		if _,ok:=cls.mapServiceNode[sName];ok==false{
			cls.mapServiceNode[sName] = make(map[int]struct{})
		}

		cls.mapServiceNode[sName][cls.localNodeInfo.NodeId]= struct{}{}
	}
}


func (cls *Cluster) checkDiscoveryNodeList(discoverMasterNode []NodeInfo) bool{
	for i:=0;i<len(discoverMasterNode)-1;i++{
		for j:=i+1;j<len(discoverMasterNode);j++{
			if discoverMasterNode[i].NodeId == discoverMasterNode[j].NodeId ||
				discoverMasterNode[i].ListenAddr == discoverMasterNode[j].ListenAddr {
				return false
			}
		}
	}

	return true
}

func (cls *Cluster) InitCfg(localNodeId int) error{
	cls.localServiceCfg = map[string]interface{}{}
	cls.mapRpc = map[int] NodeRpcInfo{}
	cls.mapIdNode = map[int]NodeInfo{}
	cls.mapServiceNode = map[string]map[int]struct{}{}

	//加载本地结点的NodeList配置
	discoveryNode,nodeInfoList,err := cls.readLocalClusterConfig(localNodeId)
	if err != nil {
		return err
	}
	cls.localNodeInfo = nodeInfoList[0]
	if cls.checkDiscoveryNodeList(discoveryNode) ==false {
		return fmt.Errorf("DiscoveryNode config is error!")
	}
	cls.masterDiscoveryNodeList = discoveryNode

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
	mapNode,ok := cls.mapServiceNode[serviceName]
	if ok == false {
		return false
	}

	_,ok = mapNode[cls.localNodeInfo.NodeId]
	return ok
}

func (cls *Cluster) GetNodeIdByService(serviceName string,rpcClientList []*rpc.Client,bAll bool) (error,int) {
	cls.locker.RLock()
	defer cls.locker.RUnlock()
	mapNodeId,ok := cls.mapServiceNode[serviceName]
	count := 0
	if ok == true {
		for nodeId,_ := range mapNodeId {
			pClient := GetCluster().getRpcClient(nodeId)
			if pClient==nil  || (bAll == false && pClient.IsConnected()==false) {
				continue
			}
			rpcClientList[count] = pClient
			count++
			if count>=cap(rpcClientList) {
				break
			}
		}
	}

	return nil,count
}

func (cls *Cluster) getServiceCfg(serviceName string) interface{}{
	v,ok := cls.localServiceCfg[serviceName]
	if ok == false {
		return nil
	}

	return v
}

func (cls *Cluster) GetServiceCfg(serviceName string) interface{}{
	serviceCfg,ok := cls.localServiceCfg[serviceName]
	if ok == false {
		return nil
	}

	return serviceCfg
}

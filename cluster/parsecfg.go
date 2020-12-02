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

func (cls *Cluster) readLocalClusterConfig(nodeId int) ([]NodeInfo,error) {
	var nodeInfoList [] NodeInfo
	clusterCfgPath :=strings.TrimRight(configDir,"/")  +"/cluster"
	fileInfoList,err := ioutil.ReadDir(clusterCfgPath)
	if err != nil {
		return nil,fmt.Errorf("Read dir %s is fail :%+v",clusterCfgPath,err)
	}

	//读取任何文件,只读符合格式的配置,目录下的文件可以自定义分文件
	for _,f := range fileInfoList{
		if f.IsDir() == false {
			filePath := strings.TrimRight(strings.TrimRight(clusterCfgPath,"/"),"\\")+"/"+f.Name()
			localNodeInfoList,err := cls.ReadClusterConfig(filePath)
			if err != nil {
				return nil,fmt.Errorf("read file path %s is error:%+v" ,filePath,err)
			}

			for _,nodeInfo := range localNodeInfoList.NodeList {
				if nodeInfo.NodeId == nodeId || nodeId == 0 {
					nodeInfoList = append(nodeInfoList,nodeInfo)
				}
			}
		}
	}

	if nodeId != 0 &&  (len(nodeInfoList)!=1){
		return nil,fmt.Errorf("%d configurations were found for the configuration with node ID %d!",len(nodeInfoList),nodeId)
	}

	return nodeInfoList,nil
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
		cls.mapServiceNode[sName] = append(cls.mapServiceNode[sName], cls.localNodeInfo.NodeId)
	}
}

func (cls *Cluster) localPrivateService(localNodeInfo *NodeInfo){
	for i:=0;i<len(localNodeInfo.ServiceList);i++{
		localNodeInfo.ServiceList[i] = strings.TrimLeft(localNodeInfo.ServiceList[i],"_")
	}
}

func (cls *Cluster) InitCfg(localNodeId int) error{
	cls.localServiceCfg = map[string]interface{}{}
	cls.mapRpc = map[int] NodeRpcInfo{}
	cls.mapIdNode = map[int]NodeInfo{}
	cls.mapServiceNode = map[string][]int{}

	//加载本地结点的NodeList配置
	nodeInfoList,err := cls.readLocalClusterConfig(localNodeId)
	if err != nil {
		return err
	}
	cls.localNodeInfo = nodeInfoList[0]
	cls.localPrivateService(&cls.localNodeInfo)

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
	nodeList,ok := cls.mapServiceNode[serviceName]
	if ok == false {
		return false
	}

	for _,nodeId := range nodeList{
		if cls.localNodeInfo.NodeId == nodeId {
			return true
		}
	}

	return false
}

func (cls *Cluster) GetNodeIdByService(serviceName string,rpcClientList []*rpc.Client,bAll bool) (error,int) {
	cls.locker.RLock()
	defer cls.locker.RUnlock()
	nodeIdList,ok := cls.mapServiceNode[serviceName]
	count := 0
	if ok == true {
		for _,nodeId := range nodeIdList {
			pClient := GetCluster().GetRpcClient(nodeId)
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

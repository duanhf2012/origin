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


func (slf *Cluster) ReadClusterConfig(filepath string) (*NodeInfoList,error) {
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


func (slf *Cluster) readServiceConfig(filepath string)  (map[string]interface{},map[int]map[string]interface{},error) {

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


func (slf *Cluster) readLocalClusterConfig(nodeId int) ([]NodeInfo,error) {
	var nodeInfoList [] NodeInfo
	clusterCfgPath :=strings.TrimRight(configdir,"/")  +"/cluster"
	fileInfoList,err := ioutil.ReadDir(clusterCfgPath)
	if err != nil {
		return nil,fmt.Errorf("Read dir %s is fail :%+v",clusterCfgPath,err)
	}

	//读取任何文件,只读符合格式的配置,目录下的文件可以自定义分文件
	for _,f := range fileInfoList{
		if f.IsDir() == false {
			filePath := strings.TrimRight(strings.TrimRight(clusterCfgPath,"/"),"\\")+"/"+f.Name()
			localNodeInfoList,err := slf.ReadClusterConfig(filePath)
			if err != nil {
				return nil,fmt.Errorf("read file path %s is error:%+v" ,filePath,err)
			}

			for _,nodeInfo := range localNodeInfoList.NodeList {
				if nodeInfo.NodeId == nodeId || nodeId == 0 {
					nodeInfoList = append(nodeInfoList,nodeInfo)
					//slf.localNodeInfo = nodeInfo
				}
			}
		}
	}

	if nodeId != 0 &&  (len(nodeInfoList)!=1){
		return nil,fmt.Errorf("%d configurations were found for the configuration with node ID %d!",len(nodeInfoList),nodeId)
	}

	return nodeInfoList,nil
}

func (slf *Cluster) readLocalService(localNodeId int) error {
	clusterCfgPath :=strings.TrimRight(configdir,"/")  +"/cluster"
	fileInfoList,err := ioutil.ReadDir(clusterCfgPath)
	if err != nil {
		return fmt.Errorf("Read dir %s is fail :%+v",clusterCfgPath,err)
	}

	//读取任何文件,只读符合格式的配置,目录下的文件可以自定义分文件
	for _,f := range fileInfoList {
		if f.IsDir() == false {
			filePath := strings.TrimRight(strings.TrimRight(clusterCfgPath, "/"), "\\") + "/" + f.Name()
			serviceConfig,mapNodeService,err := slf.readServiceConfig(filePath)
			if err != nil {
				continue
			}

			for _,s := range slf.localNodeInfo.ServiceList{
				for{
					//取公共服务配置
					pubCfg,ok := serviceConfig[s]
					if ok == true {
						slf.localServiceCfg[s] = pubCfg
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

					slf.localServiceCfg[s] = sCfg
					break
				}
			}
		}
	}

	if len(slf.localServiceCfg)==0{
		return fmt.Errorf("No service configuration was found.")
	}
	return nil
}

func (slf *Cluster) parseLocalCfg(){
	slf.mapIdNode[slf.localNodeInfo.NodeId] = slf.localNodeInfo

	for _,sName := range slf.localNodeInfo.ServiceList{
		slf.mapServiceNode[sName] = append(slf.mapServiceNode[sName],slf.localNodeInfo.NodeId)
	}
}

func (slf *Cluster) InitCfg(localNodeId int) error{
	slf.localServiceCfg = map[string]interface{}{}
	slf.mapRpc = map[int] NodeRpcInfo{}
	slf.mapIdNode = map[int]NodeInfo{}
	slf.mapServiceNode = map[string][]int{}

	//加载本地结点的NodeList配置
	nodeInfoList,err := slf.readLocalClusterConfig(localNodeId)
	if err != nil {
		return err
	}
	slf.localNodeInfo = nodeInfoList[0]

	//读取本地服务配置
	err = slf.readLocalService(localNodeId)
	if err != nil {
		return err
	}

	//本地配置服务加到全局map信息中
	slf.parseLocalCfg()
	return nil
}


func (slf *Cluster) IsConfigService(serviceName string) bool {
	slf.locker.RLock()
	defer slf.locker.RUnlock()
	nodeList,ok := slf.mapServiceNode[serviceName]
	if ok == false {
		return false
	}

	for _,nodeId := range nodeList{
		if slf.localNodeInfo.NodeId == nodeId {
			return true
		}
	}

	return false
}

func (slf *Cluster) GetNodeIdByService(servicename string,rpcClientList *[]*rpc.Client) {
	slf.locker.RLock()
	defer slf.locker.RUnlock()
	nodeIdList,ok := slf.mapServiceNode[servicename]
	if ok == true {
		for _,nodeId := range nodeIdList {
			pClient := GetCluster().GetRpcClient(nodeId)
			if pClient==nil {
				log.Error("Cannot connect node id %d",nodeId)
				continue
			}
			*rpcClientList = append(*rpcClientList,pClient)
		}
	}
}


func (slf *Cluster) getServiceCfg(servicename string) interface{}{
	v,ok := slf.localServiceCfg[servicename]
	if ok == false {
		return nil
	}

	return v
}

func (slf *Cluster) GetServiceCfg(serviceName string) interface{}{
	serviceCfg,ok := slf.localServiceCfg[serviceName]
	if ok == false {
		return nil
	}

	return serviceCfg
}

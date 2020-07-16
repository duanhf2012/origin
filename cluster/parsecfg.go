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

func (slf *Cluster) ReadClusterConfig(filepath string) (*SubNet,error) {
	c := &SubNet{}
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


func (slf *Cluster) ReadServiceConfig(filepath string)  (map[string]interface{},map[int]map[string]interface{},error) {

	c := map[string]interface{}{}

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
			nodeid,ok := serviceCfg["NodeId"]
			if ok == false {
				log.Fatal("nodeservice list not find nodeid field: %+v",nodeServiceList)
			}
			mapNodeService[int(nodeid.(float64))] = serviceCfg
		}
	}
	return serviceConfig,mapNodeService,nil
}

func (slf *Cluster) ReadAllSubNetConfig() error {
	clusterCfgPath :=strings.TrimRight(configdir,"/")  +"/cluster"
	fileInfoList,err := ioutil.ReadDir(clusterCfgPath)
	if err != nil {
		return fmt.Errorf("Read dir %s is fail :%+v",clusterCfgPath,err)
	}

	slf.mapSubNetInfo =map[string] SubNet{}
	for _,f := range fileInfoList{
		if f.IsDir() == true {
			filePath := strings.TrimRight(strings.TrimRight(clusterCfgPath,"/"),"\\")+"/"+f.Name()+"/"+"cluster.json"
			subnetinfo,err:=slf.ReadClusterConfig(filePath)
			if err != nil {
				return fmt.Errorf("read file path %s is error:%+v" ,filePath,err)
			}
			slf.mapSubNetInfo[f.Name()] = *subnetinfo
		}
	}

	return nil
}

func (slf *Cluster) ReadLocalSubNetServiceConfig(subnet string) error {
	clusterCfgPath :=strings.TrimRight(configdir,"/")  +"/cluster"
	fileInfoList,err := ioutil.ReadDir(clusterCfgPath)
	if err != nil {
		return fmt.Errorf("Read %s dir is fail:%+v ",clusterCfgPath,err)
	}

	slf.mapSubNetInfo =map[string] SubNet{}
	for _,f := range fileInfoList{
		if f.IsDir() == true && f.Name()==subnet{ //同一子网
			filePath := strings.TrimRight(strings.TrimRight(clusterCfgPath,"/"),"\\")+"/"+f.Name()+"/"+"service.json"
			localServiceCfg,localNodeServiceCfg,err:=slf.ReadServiceConfig(filePath)
			if err != nil {
				return fmt.Errorf("Read file %s is fail :%+v",filePath,err)
			}
			slf.localServiceCfg = localServiceCfg
			slf.localNodeServiceCfg =localNodeServiceCfg
		}
	}

	return nil
}



func (slf *Cluster) InitCfg(currentNodeId int) error{
	//mapSubNetInfo  := map[string] SubNet{} //子网名称，子网信息
	mapSubNetNodeInfo := map[string]map[int]NodeInfo{} //map[子网名称]map[NodeId]NodeInfo
	localSubNetMapNode := map[int]NodeInfo{}           //本子网内 map[NodeId]NodeInfo
	localSubNetMapService := map[string][]NodeInfo{}   //本子网内所有ServiceName对应的结点列表
	localNodeMapService := map[string]interface{}{}    //本Node支持的服务
	localNodeInfo := NodeInfo{}

	err := slf.ReadAllSubNetConfig()
	if err != nil {
		return err
	}

	//分析配置
	var localSubnetName string
	for subnetName,subnetInfo := range slf.mapSubNetInfo {
		for _,nodeinfo := range subnetInfo.NodeList {
			//装载slf.mapNodeInfo
			_,ok := mapSubNetNodeInfo[subnetName]
			if ok == false {
				mapnodeInfo := make(map[int]NodeInfo,1)
				mapnodeInfo[nodeinfo.NodeId] = nodeinfo
				mapSubNetNodeInfo[subnetName] = mapnodeInfo
			}else{
				mapSubNetNodeInfo[subnetName][nodeinfo.NodeId] = nodeinfo
			}

			//判断本进程的子网
			if nodeinfo.NodeId == currentNodeId {
				localSubnetName = subnetName
			}
		}
	}


	//装载
	subnet,ok := slf.mapSubNetInfo[localSubnetName]
	if ok == false {
		return fmt.Errorf("NodeId %d not in any subnet",currentNodeId)
	}
	subnet.SubNetName = localSubnetName
	for _,nodeinfo := range subnet.NodeList {
		localSubNetMapNode[nodeinfo.NodeId] = nodeinfo

		//装载本Node进程所有的服务
		if nodeinfo.NodeId == currentNodeId {
			for _,s := range nodeinfo.ServiceList {
				servicename := s
				if strings.Index(s,"_") == 0 {
					servicename = s[1:]
				}
				localNodeMapService[servicename] = nil
			}
			localNodeInfo = nodeinfo
		}

		for _,s := range nodeinfo.ServiceList {
			//以_打头的，表示只在本机进程，不对整个子网开发
			if strings.Index(s,"_") == 0 {
				continue
			}

			if _,ok := localSubNetMapService[s];ok== true{
				localSubNetMapService[s] = []NodeInfo{}
			}
			localSubNetMapService[s] = append(localSubNetMapService[s],nodeinfo)
		}
	}
	if localNodeInfo.NodeId == 0 {
		return fmt.Errorf("Canoot find NodeId %d not in any config file.",currentNodeId)
	}


	slf.mapSubNetNodeInfo=mapSubNetNodeInfo
	slf.localSubNetMapNode=localSubNetMapNode
	slf.localSubNetMapService = localSubNetMapService
	slf.localNodeMapService = localNodeMapService
	slf.localsubnet = subnet
	slf.localNodeInfo =localNodeInfo

	//读取服务
	return slf.ReadLocalSubNetServiceConfig(slf.localsubnet.SubNetName)
}


func (slf *Cluster) IsConfigService(servicename string) bool {
	_,ok := slf.localNodeMapService[servicename]
	return ok
}



func (slf *Cluster) GetNodeIdByService(servicename string,rpcClientList *[]*rpc.Client) {
	nodeInfoList,ok := slf.localSubNetMapService[servicename]
	if ok == true {
		for _,node := range nodeInfoList {
			pClient := GetCluster().GetRpcClient(node.NodeId)
			if pClient==nil {
				log.Error("Cannot connect node id %d",node.NodeId)
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

func (slf *Cluster) GetServiceCfg(nodeid int,servicename string) interface{}{
	nodeService,ok := slf.localNodeServiceCfg[nodeid]
	if ok == false {
		return slf.getServiceCfg(servicename)
	}

	v,ok := nodeService[servicename]
	if ok == false{
		return slf.getServiceCfg(servicename)
	}

	return v
}

package cluster

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"
)

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


func (slf *Cluster) ReadServiceConfig(filepath string)  (map[string]interface{},error) {
	c := map[string]interface{}{}

	d, err := ioutil.ReadFile(filepath)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(d, &c)
	if err != nil {
		return nil, err
	}

	return c,nil

}

func (slf *Cluster) ReadAllSubNetConfig() error {
	clusterCfgPath :=strings.TrimRight(configdir,"/")  +"/cluster"
	fileInfoList,err := ioutil.ReadDir(clusterCfgPath)
	if err != nil {
		return err
	}
	slf.mapSubNetInfo =map[string] SubNet{}
	for _,f := range fileInfoList{
		if f.IsDir() == true {
			subnetinfo,err:=slf.ReadClusterConfig(strings.TrimRight(strings.TrimRight(clusterCfgPath,"/"),"\\")+"/"+f.Name()+"/"+"cluster.json")
			if err != nil {
				return err
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
		return err
	}
	slf.mapSubNetInfo =map[string] SubNet{}
	for _,f := range fileInfoList{
		if f.IsDir() == true && f.Name()==subnet{ //同一子网
			localNodeServiceCfg,err:=slf.ReadServiceConfig(strings.TrimRight(strings.TrimRight(clusterCfgPath,"/"),"\\")+"/"+f.Name()+"/"+"service.json")
			if err != nil {
				return err
			}
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
	slf.ReadLocalSubNetServiceConfig(slf.localsubnet.SubNetName)
	return err
}


func (slf *Cluster) IsConfigService(servicename string) bool {
	_,ok := slf.localNodeMapService[servicename]
	return ok
}

func (slf *Cluster) GetNodeIdByService(servicename string) []int{
	var nodelist []int
	nodeInfoList,ok := slf.localSubNetMapService[servicename]
	if ok == true {
		for _,node := range nodeInfoList {
			nodelist = append(nodelist,node.NodeId)
		}
	}

	return nodelist
}

func (slf *Cluster) GetServiceCfg(servicename string) interface{}{
	v,ok := slf.localNodeServiceCfg[servicename]
	if ok == false{
		return nil
	}

	return v
}

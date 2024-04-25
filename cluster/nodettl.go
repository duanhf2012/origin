package cluster

import (
	"time"
	"container/list"
)

type nodeTTL struct {
	nodeId string
	refreshTime time.Time
}

type nodeSetTTL struct {
	l *list.List
	mapElement map[string]*list.Element
	ttl time.Duration
}

func (ns *nodeSetTTL) init(ttl time.Duration) {
	ns.ttl = ttl
	ns.mapElement = make(map[string]*list.Element,32)
	ns.l = list.New()
}

func (ns *nodeSetTTL) removeNode(nodeId string) {
	ele,ok:=ns.mapElement[nodeId]
	if ok == false {
		return
	}

	ns.l.Remove(ele)
	delete(ns.mapElement,nodeId)
}

func (ns *nodeSetTTL) addAndRefreshNode(nodeId string){
	ele,ok:=ns.mapElement[nodeId]
	if ok == false {
		ele = ns.l.PushBack(nodeId)
		ele.Value = &nodeTTL{nodeId,time.Now()}
		ns.mapElement[nodeId] = ele
		return
	}

	ele.Value.(*nodeTTL).refreshTime =  time.Now()
	ns.l.MoveToBack(ele)
}

func (ns *nodeSetTTL) checkTTL(cb func(nodeIdList []string)){
	nodeIdList := []string{}
	for{
		f := ns.l.Front()
		if f == nil {
			break
		}

		nt := f.Value.(*nodeTTL)
		if time.Now().Sub(nt.refreshTime) > ns.ttl {
			nodeIdList = append(nodeIdList,nt.nodeId)
		}else{
			break
		}

		//删除结点
		ns.l.Remove(f)
		delete(ns.mapElement,nt.nodeId)
	}

	if len(nodeIdList) >0 {
		cb(nodeIdList)
	}
}

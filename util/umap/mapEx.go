package umap

import (
	"fmt"
	"github.com/duanhf2012/origin/util/hash"
	"sync"
	"sync/atomic"
)

const (
	DEFAULT_SAFE_MAP_MAX_HASH_NUM = 10
)

type MapEx struct {
	sync.RWMutex
	m          []map[interface{}]interface{}
	l          []sync.RWMutex
	hashMapNum int
	rangeIdx uint32
}

func (m *MapEx) Init(hashMapNum int) {
	m.hashMapNum = hashMapNum

	m.m = []map[interface{}]interface{}{}
	m.l = []sync.RWMutex{}

	for i := 0; i < hashMapNum; i++ {
		m.m = append(m.m, make(map[interface{}]interface{}))
		m.l = append(m.l, sync.RWMutex{})
	}
}

func NewMapEx() *MapEx {
	mapEx := MapEx{}
	mapEx.Init(DEFAULT_SAFE_MAP_MAX_HASH_NUM)
	return &mapEx
}


func (m *MapEx) NextRLockRange(f func(key interface{}, value interface{})) {
	i := atomic.AddUint32(&m.rangeIdx,1)%uint32(m.hashMapNum)

	m.l[i].RLock()
	for key, val := range m.m[i] {
		f(key, val)
	}

	m.l[i].RUnlock()
}


func (m *MapEx) ClearMap() {
	for i := 0; i < DEFAULT_SAFE_MAP_MAX_HASH_NUM; i++ {
		m.l[i].Lock()
		m.m[i] = map[interface{}]interface{}{}
		m.l[i].Unlock()
	}
}

func (m *MapEx) GetHashCode(key interface{}) int {
	return int(hash.HashNumber(fmt.Sprint(key)))
}

func (m *MapEx) GetArrayIdByKey(key interface{}) int {
	if m.hashMapNum ==0 {
		return -1
	}
	idx := m.GetHashCode(key) % m.hashMapNum
	if idx > m.hashMapNum {
		return -1
	}

	return idx
}

func (m *MapEx) GetMapByKey(key interface{}) map[interface{}]interface{} {
	idx := m.GetArrayIdByKey(key)
	if idx < 0 || idx > m.hashMapNum {
		return nil
	}

	return m.m[idx]
}

func (m *MapEx) UnsafeGet(key interface{}) interface{} {

	mapData := m.GetMapByKey(key)
	if mapData == nil {
		return nil
	}

	val, ok := mapData[key]
	if ok == false {
		return nil
	}

	return val
}

func (m *MapEx) Get(key interface{}) interface{} {
	idx := m.GetArrayIdByKey(key)
	if idx < 0 || idx > m.hashMapNum {
		return nil
	}

	m.l[idx].RLock()
	defer m.l[idx].RUnlock()

	val := m.m[idx]
	ret, ok := val[key]
	if ok == false {
		return nil
	}
	return ret
}

func (m *MapEx) Set(key interface{}, value interface{}) {
	idx := m.GetArrayIdByKey(key)
	if idx < 0 || idx > m.hashMapNum {
		return
	}

	m.l[idx].Lock()
	defer m.l[idx].Unlock()

	val := m.m[idx]
	val[key] = value
}

func (m *MapEx) UnsafeDel(key interface{}) {
	mapData := m.GetMapByKey(key)
	if mapData == nil {
		return
	}

	delete(mapData, key)
}

func (m *MapEx) Del(key interface{}) {
	idx := m.GetArrayIdByKey(key)
	if idx < 0 || idx > m.hashMapNum {
		return
	}

	m.l[idx].Lock()
	defer m.l[idx].Unlock()

	val := m.m[idx]
	delete(val, key)
}

func (m *MapEx) Len() int {
	lens := 0
	for i := 0; i < m.hashMapNum; i++ {
		m.l[i].RLock()
		lens += len(m.m[i])
		m.l[i].RUnlock()
	}

	return lens
}

func (m *MapEx) RLockRange(f func(key interface{}, value interface{})) {
	for i := 0; i < m.hashMapNum; i++ {
		m.l[i].RLock()
		for key, val := range m.m[i] {
			f(key, val)
		}
		m.l[i].RUnlock()
	}
}

func (m *MapEx) LockRange(f func(key interface{}, value interface{})) {
	for i := 0; i < m.hashMapNum; i++ {
		m.l[i].Lock()
		for key, val := range m.m[i] {
			f(key, val)
		}
		m.l[i].Unlock()
	}
}

func (m *MapEx) LockGet(key interface{}, f func(value interface{})) {
	idx := m.GetArrayIdByKey(key)
	if idx < 0 || idx > m.hashMapNum {
		f(nil)
		return
	}

	m.l[idx].Lock()
	val := m.m[idx]
	ret, ok := val[key]
	if ok == false {
		f(nil)
	} else {
		f(ret)
	}
	m.l[idx].Unlock()
}

func (m *MapEx) LockSet(key interface{}, f func(value interface{}) interface{}) {
	idx := m.GetArrayIdByKey(key)
	if idx < 0 || idx > m.hashMapNum {
		f(nil)
		return
	}

	m.l[idx].Lock()
	val := m.m[idx]
	ret, ok := val[key]

	if ok == false {
		ret := f(nil)
		if ret != nil {
			val[key] =ret
		}
	} else {
		ret := f(ret)
		if ret != nil {
			val[key] =ret
		}
	}

	m.l[idx].Unlock()
}
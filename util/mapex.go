package util

import (
	"fmt"

	"github.com/duanhf2012/origin/util/hash"
)

const (
	DEFAULT_MAX_HASH_NUM = 100
)

type MapEx struct {
	m          []Map
	hashMapNum uint
}

func NewMapEx() *MapEx {
	mapEx := MapEx{}
	mapEx.Init(DEFAULT_MAX_HASH_NUM)
	return &mapEx
}

func (m *MapEx) Init(hashMapNum uint) {
	var i uint
	for i = 0; i < hashMapNum; i++ {
		m.m = append(m.m, Map{})
	}

	m.hashMapNum = hashMapNum
}

func (m *MapEx) ClearMap() {
	var i uint
	for i = 0; i < m.hashMapNum; i++ {
		m.m[i].ClearMap()
	}
}

func (m *MapEx) GetHashCode(key interface{}) uint {
	return hash.HashNumber(fmt.Sprint(key))
}

func (m *MapEx) GetMapByKey(key interface{}) *Map {
	idx := m.GetHashCode(key) % m.hashMapNum
	if idx < 0 || idx > m.hashMapNum {
		return nil
	}

	return &m.m[idx]
}

func (m *MapEx) Get(key interface{}) interface{} {
	mapData := m.GetMapByKey(key)
	if mapData == nil {
		return nil
	}
	return mapData.Get(key)
}

func (m *MapEx) Set(key interface{}, value interface{}) {
	mapData := m.GetMapByKey(key)
	if mapData == nil {
		return
	}

	mapData.Set(key, value)
}

func (m *MapEx) Del(key interface{}) {

	mapData := m.GetMapByKey(key)
	if mapData == nil {
		return
	}

	mapData.Del(key)
}

func (m *MapEx) RLockRange(f func(key interface{}, value interface{})) {
	var i uint
	for i = 0; i < m.hashMapNum; i++ {
		m.m[i].RLockRange(f)
	}

}

func (m *MapEx) LockRange(f func(key interface{}, value interface{})) {
	var i uint
	for i = 0; i < m.hashMapNum; i++ {
		m.m[i].LockRange(f)
	}
}

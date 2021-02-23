package sync

import (
	sysSync "sync"
)

type Pool struct {
	C chan interface{}  //最大缓存的数量
	syncPool sysSync.Pool
}

type IPoolData interface {
	Reset()
	IsRef()bool
	Ref()
	UnRef()
}

type PoolEx struct{
	C chan IPoolData  //最大缓存的数量
	syncPool sysSync.Pool
}

func (pool *Pool) Get() interface{}{
	select {
	case d := <-pool.C:
		return d
	default:
		return pool.syncPool.Get()
	}

	return nil
}

func (pool *Pool) Put(data interface{}){
	select {
	case pool.C <- data:
	default:
		pool.syncPool.Put(data)
	}

}

func NewPool(C chan interface{},New func()interface{}) *Pool{
	var p Pool
	p.C = C
	p.syncPool.New = New
	return &p
}

func NewPoolEx(C chan IPoolData,New func()IPoolData) *PoolEx{
	var pool PoolEx
	pool.C = C
	//pool.New = New
	pool.syncPool.New = func() interface{} {
		return New()
	}
	return &pool
}

func (pool *PoolEx) Get() IPoolData{
	select {
	case d := <-pool.C:
		d.Ref()
		return d
	default:
		data := pool.syncPool.Get().(IPoolData)
		data.Ref()
		return data
	}

	return nil
}

func (pool *PoolEx) Put(data IPoolData){
	if data.IsRef() == false {
		panic("Repeatedly freeing memory")
	}
	data.Reset()
	data.UnRef()
	select {
	case pool.C <- data:
	default:
		pool.syncPool.Put(data)
	}
}



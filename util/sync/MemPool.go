package sync

import sysSync "sync"

type Pool struct {
	New func()interface{} //构建对象函数
	C chan interface{}  //最大缓存的数量
}

type IPoolData interface {
	Reset()
	IsRef()bool
	Ref()
	UnRef()
}

type poolEx struct{
	C chan IPoolData  //最大缓存的数量
	syncPool sysSync.Pool
}


func (pool *Pool) Get() interface{}{
	select {
	case d := <-pool.C:
		return d
	default:
		return pool.New()
	}

	return nil
}

func (pool *Pool) Put(data interface{}){
	select {
	case pool.C <- data:
	default:
	}
}

func NewPoolEx(C chan IPoolData,New func()IPoolData) *poolEx{
	var pool poolEx
	pool.C = C
	//pool.New = New
	pool.syncPool.New = func() interface{} {
		return New()
	}
	return &pool
}

func (pool *poolEx) Get() IPoolData{
	select {
	case d := <-pool.C:
		d.Ref()
		d.Reset()
		return d
	default:
		data := pool.syncPool.Get().(IPoolData)
		data.Reset()
		data.Ref()
		return data
	}

	return nil
}

func (pool *poolEx) Put(data IPoolData){
	if data.IsRef() == false {
		panic("Repeatedly freeing memory")
	}

	data.UnRef()
	select {
	case pool.C <- data:
	default:
		pool.syncPool.Put(data)
	}
}



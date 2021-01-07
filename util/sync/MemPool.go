package sync

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

type PoolEx struct{
	New func()IPoolData //构建对象函数
	C chan IPoolData  //最大缓存的数量
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

func (pool *PoolEx) Get() IPoolData{
	select {
	case d := <-pool.C:
		d.Ref()
		d.Reset()
		return d
	default:
		data := pool.New()
		data.Reset()
		data.Ref()
	}

	return nil
}

func (pool *PoolEx) Put(data IPoolData){
	if data.IsRef() == false {
		panic("Repeatedly freeing memory")
	}

	data.UnRef()
	select {
	case pool.C <- data:
	default:
	}
}


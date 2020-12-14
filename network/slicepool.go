package network

import (
	"sync"
)

type INetMempool interface {
	MakeByteSlice(size int) []byte
	ReleaseByteSlice(byteBuff []byte) bool
}

type memAreaPool struct {
	minAreaValue int   //最小范围值
	maxAreaValue int   //最大范围值
	growthValue int   //内存增长值
	pool []sync.Pool
}

//小于2048时，按64步长增长.>2048时则按2048长度增长
var memAreaPoolList = [2]*memAreaPool{&memAreaPool{minAreaValue:1,maxAreaValue: 2048,growthValue:64}, &memAreaPool{minAreaValue: 2049,maxAreaValue:65536,growthValue:2048}}

func init(){
	for i:=0;i<len(memAreaPoolList);i++{
		memAreaPoolList[i].makePool()
	}
}

func NewMemAreaPool() *memAreaPool{
	return &memAreaPool{}
}

func (areaPool *memAreaPool) makePool(){
	poolLen := (areaPool.maxAreaValue - areaPool.minAreaValue+1)/areaPool.growthValue
	areaPool.pool = make([]sync.Pool,poolLen)
	for i:=0;i<poolLen;i++{
		memSize := (areaPool.minAreaValue-1)+(i+1)*areaPool.growthValue
		areaPool.pool[i] = sync.Pool{New: func() interface{}{
			//fmt.Println("make memsize:",memSize)
			return make([]byte,memSize)
		}}
	}
}

func (areaPool *memAreaPool) makeByteSlice(size int) []byte{
	pos := areaPool.getPosByteSize(size)
	if pos > len(areaPool.pool) || pos == -1 {
		return nil
	}

	return areaPool.pool[pos].Get().([]byte)[:size]
}

func (areaPool *memAreaPool) getPosByteSize(size int) int{
	pos := (size - areaPool.minAreaValue)/areaPool.growthValue
	if pos >= len(areaPool.pool) {
		return -1
	}

	return pos
}

func (areaPool *memAreaPool) releaseByteSlice(byteBuff []byte) bool{
	pos := areaPool.getPosByteSize(cap(byteBuff))
	if pos > len(areaPool.pool) || pos == -1{
		panic("assert!")
		return false
	}
	
	areaPool.pool[pos].Put(byteBuff)
	return true
}

func (areaPool *memAreaPool) MakeByteSlice(size int) []byte{
	for i:=0;i<len(memAreaPoolList);i++{
		if size <= memAreaPoolList[i].maxAreaValue {
			return memAreaPoolList[i].makeByteSlice(size)
		}
	}

	return nil
}

func (areaPool *memAreaPool) ReleaseByteSlice(byteBuff []byte) bool {
	for i:=0;i<len(memAreaPoolList);i++{
		if cap(byteBuff) <= memAreaPoolList[i].maxAreaValue {
			return memAreaPoolList[i].releaseByteSlice(byteBuff)
		}
	}

	return false
}

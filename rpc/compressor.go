package rpc

import (
	"runtime"
	"errors"
	"github.com/pierrec/lz4/v4"
	"fmt"
	"github.com/duanhf2012/origin/network"
)

var memPool network.INetMempool = network.NewMemAreaPool()

type ICompressor interface {
	CompressBlock(src []byte) ([]byte, error)   //dst如果有预申请使用dst内存，传入nil时内部申请
	UncompressBlock(src []byte) ([]byte, error) //dst如果有预申请使用dst内存，传入nil时内部申请

	CompressBufferCollection(buffer []byte)   	//压缩的Buffer内存回收
	UnCompressBufferCollection(buffer []byte) 	//解压缩的Buffer内存回收
}

var compressor ICompressor
func init(){
	SetCompressor(&Lz4Compressor{})
}

func SetCompressor(cp ICompressor){
	compressor = cp
}

type Lz4Compressor struct {
}

func (lc *Lz4Compressor) CompressBlock(src []byte) (dest []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			err = errors.New("core dump info[" + errString + "]\n" + string(buf[:l]))
		}
	}()

	var c lz4.Compressor
	var cnt int
	dest = memPool.MakeByteSlice(lz4.CompressBlockBound(len(src))+1)
	cnt, err = c.CompressBlock(src, dest[1:])
	if err != nil {
		memPool.ReleaseByteSlice(dest)
		return nil,err
	}

	ratio := len(src) / cnt
	if len(src) % cnt > 0 {
		ratio += 1
	}

	if ratio > 255 {
		memPool.ReleaseByteSlice(dest)
		return nil,fmt.Errorf("Impermissible errors")
	}

	dest[0] = uint8(ratio)
	dest = dest[:cnt+1]
	return
}

func (lc *Lz4Compressor) UncompressBlock(src []byte) (dest []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			err = errors.New("core dump info[" + errString + "]\n" + string(buf[:l]))
		}
	}()

	radio := uint8(src[0])
	if radio == 0 {
		return nil,fmt.Errorf("Impermissible errors")
	}

	dest = memPool.MakeByteSlice(len(src)*int(radio))
	cnt, err := lz4.UncompressBlock(src[1:], dest)
	if err != nil {
		memPool.ReleaseByteSlice(dest)
		return nil,err
	}

	return dest[:cnt],nil
}

func (lc *Lz4Compressor) compressBlockBound(n int) int{
	return lz4.CompressBlockBound(n)
}

func (lc *Lz4Compressor) CompressBufferCollection(buffer []byte){
	memPool.ReleaseByteSlice(buffer)
}

func (lc *Lz4Compressor) UnCompressBufferCollection(buffer []byte) {
	memPool.ReleaseByteSlice(buffer)
}

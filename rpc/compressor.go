package rpc

import (
	"runtime"
	"errors"
	"github.com/pierrec/lz4/v4"
	"fmt"
)

type ICompressor interface {
	CompressBlock(src, dst []byte) (int, error)     //dst如果有预申请使用dst内存，传入nil时内部申请
	UncompressBlock(src []byte, dst []byte) (int, error)//dst如果有预申请使用dst内存，传入nil时内部申请

	CompressBlockBound(n int) int
	UnCompressBlockBound(n int) int
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

func (lc *Lz4Compressor) CompressBlock(src, dst []byte) (cnt int, err error) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			err = errors.New("core dump info[" + errString + "]\n" + string(buf[:l]))
		}
	}()

	var c lz4.Compressor
	cnt, err = c.CompressBlock(src, dst)

	return
}

func (lc *Lz4Compressor) UncompressBlock(src, dst []byte) (cnt int, err error) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			err = errors.New("core dump info[" + errString + "]\n" + string(buf[:l]))
		}
	}()

	cnt, err = lz4.UncompressBlock(src, dst)
	return
}

func (lc *Lz4Compressor) CompressBlockBound(n int) int{
	return lz4.CompressBlockBound(n)
}

func (lc *Lz4Compressor) UnCompressBlockBound(n int) int{
	return n*10
}



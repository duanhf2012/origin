package rpc

import (
	"runtime"
	"errors"
	"github.com/pierrec/lz4/v4"
	"fmt"
)

type ICompressor interface {
	CompressBlock(src, dst []byte) ([]byte,int, error)     //dst如果有预申请使用dst内存，传入nil时内部申请
	UncompressBlock(src []byte, dst []byte) ([]byte,int, error)//dst如果有预申请使用dst内存，传入nil时内部申请

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

func (lc *Lz4Compressor) CompressBlock(src, dst []byte) (dest []byte,cnt int, err error) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			err = errors.New("core dump info[" + errString + "]\n" + string(buf[:l]))
		}
	}()

	dest = dst
	var c lz4.Compressor
	maxCompressSize := lc.CompressBlockBound(len(src))
	if len(dest) < maxCompressSize {
		dest = make([]byte,maxCompressSize)
	}

	cnt, err = c.CompressBlock(src, dest)

	return
}

func (lc *Lz4Compressor) UncompressBlock(src, dst []byte) (dest []byte,cnt int, err error) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			err = errors.New("core dump info[" + errString + "]\n" + string(buf[:l]))
		}
	}()

	dest = dst
	maxUncompressSize := lc.UnCompressBlockBound(len(src))
	if len(dest) < maxUncompressSize {
		dest = make([]byte,maxUncompressSize)
	}

	cnt, err = lz4.UncompressBlock(src, dest)
	return
}

func (lc *Lz4Compressor) CompressBlockBound(n int) int{
	return lz4.CompressBlockBound(n)
}

func (lc *Lz4Compressor) UnCompressBlockBound(n int) int{
	return n*10
}



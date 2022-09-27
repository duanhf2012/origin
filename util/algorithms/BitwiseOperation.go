package algorithms

import (
	"errors"
	"unsafe"
)

type BitNumber interface {
	int | int8 | int16 | int32 | int64 | uint | uint8 | uint16 | uint32 | uint64 | uintptr
}

type UnsignedNumber interface {
	uint | uint8 | uint16 | uint32 | uint64 | uintptr
}

func getBitTagIndex[Number BitNumber, UNumber UnsignedNumber](bitBuff []Number, bitPositionIndex UNumber) (uintptr, uintptr, bool) {
	sliceIndex := uintptr(bitPositionIndex) / (8 * unsafe.Sizeof(bitBuff[0]))
	sliceBitIndex := uintptr(bitPositionIndex) % (8 * unsafe.Sizeof(bitBuff[0]))

	//位index不能越界
	if uintptr(bitPositionIndex) >= uintptr(len(bitBuff))*unsafe.Sizeof(bitBuff[0])*8 {
		return 0, 0, false
	}
	return sliceIndex, sliceBitIndex, true
}

func setBitTagByIndex[Number BitNumber, UNumber UnsignedNumber](bitBuff []Number, bitPositionIndex UNumber, setTag bool) bool {
	sliceIndex, sliceBitIndex, ret := getBitTagIndex(bitBuff, bitPositionIndex)
	if ret == false {
		return ret
	}

	if setTag {
		bitBuff[sliceIndex] = bitBuff[sliceIndex] | 1<<sliceBitIndex
	} else {
		bitBuff[sliceIndex] = bitBuff[sliceIndex] &^ (1 << sliceBitIndex)
	}

	return true
}

func GetBitwiseTag[Number BitNumber, UNumber UnsignedNumber](bitBuff []Number, bitPositionIndex UNumber) (bool, error) {
	sliceIndex, sliceBitIndex, ret := getBitTagIndex(bitBuff, bitPositionIndex)
	if ret == false {
		return false, errors.New("Invalid parameter")
	}

	return (bitBuff[sliceIndex] & (1 << sliceBitIndex)) > 0, nil
}

func SetBitwiseTag[Number BitNumber, UNumber UnsignedNumber](bitBuff []Number, bitPositionIndex UNumber) bool {
	return setBitTagByIndex(bitBuff, bitPositionIndex, true)
}

func ClearBitwiseTag[Number BitNumber, UNumber UnsignedNumber](bitBuff []Number, bitPositionIndex UNumber) bool {
	return setBitTagByIndex(bitBuff, bitPositionIndex, false)
}

func GetBitwiseNum[Number BitNumber](bitBuff []Number) int {
	return len(bitBuff) * int(unsafe.Sizeof(bitBuff[0])*8)
}

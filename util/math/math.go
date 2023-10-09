package math

import (
	"github.com/duanhf2012/origin/log"
)

type NumberType interface {
	int | int8 | int16 | int32 | int64 | float32 | float64 | uint | uint8 | uint16 | uint32 | uint64
}

type SignedNumberType interface {
	int | int8 | int16 | int32 | int64 | float32 | float64
}

type FloatType interface {
	float32 | float64
}

func Max[NumType NumberType](number1 NumType, number2 NumType) NumType {
	if number1 > number2 {
		return number1
	}

	return number2
}

func Min[NumType NumberType](number1 NumType, number2 NumType) NumType {
	if number1 < number2 {
		return number1
	}

	return number2
}

func Abs[NumType SignedNumberType](Num NumType) NumType {
	if Num < 0 {
		return -1 * Num
	}

	return Num
}

func AddSafe[NumType NumberType](number1 NumType, number2 NumType) (NumType, bool) {
	ret := number1 + number2
	if number2 > 0 && ret < number1 {
		log.Stack("Calculation overflow", log.Any("number1", number1), log.Any("number2", number2))
		return ret, false
	} else if number2 < 0 && ret > number1 {
		log.Stack("Calculation overflow", log.Any("number1", number1), log.Any("number2", number2))
		return ret, false
	}

	return ret, true
}

func SubSafe[NumType NumberType](number1 NumType, number2 NumType) (NumType, bool) {
	ret := number1 - number2
	if number2 > 0 && ret > number1 {
		log.Stack("Calculation overflow", log.Any("number1", number1), log.Any("number2", number2))
		return ret, false
	} else if number2 < 0 && ret < number1 {
		log.Stack("Calculation overflow", log.Any("number1", number1), log.Any("number2", number2))
		return ret, false
	}

	return ret, true
}

func MulSafe[NumType NumberType](number1 NumType, number2 NumType) (NumType, bool) {
	ret := number1 * number2
	if number1 == 0 || number2 == 0 {
		return ret, true
	}

	if ret/number2 == number1 {
		return ret, true
	}

	log.Stack("Calculation overflow", log.Any("number1", number1), log.Any("number2", number2))
	return ret, true
}

func Add[NumType NumberType](number1 NumType, number2 NumType) NumType {
	ret, _ := AddSafe(number1, number2)
	return ret
}

func Sub[NumType NumberType](number1 NumType, number2 NumType) NumType {
	ret, _ := SubSafe(number1, number2)
	return ret
}

func Mul[NumType NumberType](number1 NumType, number2 NumType) NumType {
	ret, _ := MulSafe(number1, number2)
	return ret
}

// 安全的求比例
func PercentRateSafe[NumType NumberType, OutNumType NumberType](maxValue int64, rate NumType, numbers ...NumType) (OutNumType, bool) {
	// 比例不能为负数
	if rate < 0 {
		log.Stack("rate must not positive")
		return 0, false
	}

	if rate == 0 {
		// 比例为0
		return 0, true
	}

	ret := int64(rate)
	for _, number := range numbers {
		number64 := int64(number)
		result, success := MulSafe(number64, ret)
		if !success {
			// 基数*比例越界了，int64都越界了，没办法了
			return 0, false
		}

		ret = result
	}

	ret = ret / 10000
	if ret > maxValue {
		return 0, false
	}

	return OutNumType(ret), true
}

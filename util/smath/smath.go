package smath

import (
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/util/typedef"
)

func Max[NumType typedef.Number](number1 NumType, number2 NumType) NumType {
	if number1 > number2 {
		return number1
	}

	return number2
}

func Min[NumType typedef.Number](number1 NumType, number2 NumType) NumType {
	if number1 < number2 {
		return number1
	}

	return number2
}

func Abs[NumType typedef.Signed|typedef.Float](Num NumType) NumType {
	if Num < 0 {
		return -1 * Num
	}

	return Num
}

func AddSafe[NumType typedef.Number](number1 NumType, number2 NumType) (NumType, bool) {
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

func SubSafe[NumType typedef.Number](number1 NumType, number2 NumType) (NumType, bool) {
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

func MulSafe[NumType typedef.Number](number1 NumType, number2 NumType) (NumType, bool) {
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

func Add[NumType typedef.Number](number1 NumType, number2 NumType) NumType {
	ret, _ := AddSafe(number1, number2)
	return ret
}

func Sub[NumType typedef.Number](number1 NumType, number2 NumType) NumType {
	ret, _ := SubSafe(number1, number2)
	return ret
}

func Mul[NumType typedef.Number](number1 NumType, number2 NumType) NumType {
	ret, _ := MulSafe(number1, number2)
	return ret
}


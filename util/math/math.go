package math

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

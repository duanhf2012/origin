package algorithms


type NumberType interface {
	int | int8 | int16 | int32 | int64 | string | float32 | float64 | uint | uint8 | uint16 | uint32 | uint64
}

type Element[ValueType NumberType] interface {
	GetValue() ValueType
}

//BiSearch 二分查找,切片必需有序号。matchUp表示是否向上范围查找。比如：数列10 20  30 ，当value传入25时，返回结果是2,表示落到3的范围
func BiSearch[ValueType NumberType, T Element[ValueType]](sElement []T, value ValueType, matchUp int) int {
	low, high := 0, len(sElement)-1
	if high == -1 {
		return -1
	}

	var mid int
	for low <= high {
		mid = low + (high-low)>>1
		if sElement[mid].GetValue() > value {
			high = mid - 1
		} else if sElement[mid].GetValue() < value {
			low = mid + 1
		} else {
			return mid
		}
	}

	switch matchUp {
	case 1:
		if (sElement[mid].GetValue()) < value &&
			(mid+1 < len(sElement)-1) {
			return mid + 1
		}
		return mid
	case -1:
		if (sElement[mid].GetValue()) > value {
			if mid - 1 < 0 {
				return -1
			} else {
				return mid - 1
			}
		} else if (sElement[mid].GetValue()) < value {
			if (mid+1 < len(sElement)-1) {
				return mid + 1
			} else {
				return mid
			}
		} else {
			return mid
		}
	}

	return -1
}

package algorithms

type NumberType interface {
	int | int8 | int16 | int32 | int64 | string | float32 | float64 | uint | uint8 | uint16 | uint32 | uint64
}

type Element[ValueType NumberType] interface {
	GetValue() ValueType
}

/*
BiSearch 二分查找,切片必需有序
matchUp规则如下：
参数为0时，则一定要找到相等的值
参数-1时，找value左边的值，例如：[10,20,30,40],当value为9时返回-1; 当value为11时，返回0  当value为41时，返回 3
参数 1时，找value右边的值，例如：[10,20,30,40],当value为9时返回 0; 当value为11时，返回1  当value为41时，返回-1

返回-1时代表没有找到下标
*/
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
		if (sElement[mid].GetValue()) < value {
			if mid+1 >= len(sElement) {
				return -1
			}
			return mid + 1
		}
		return mid
	case -1:
		if (sElement[mid].GetValue()) > value {
			if mid-1 < 0 {
				return -1
			} else {
				return mid - 1
			}
		} else if (sElement[mid].GetValue()) < value {
			//if mid+1 < len(sElement)-1 {
			//	return mid + 1
			//} else {
			return mid
			//}
		} else {
			return mid
		}
	}

	return -1
}

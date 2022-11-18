package rankservice

func transformLevel(level int32) interface{} {
	switch level {
	case 8:
		return uint8(0)
	case 16:
		return uint16(0)
	case 32:
		return uint32(0)
	case 64:
		return uint64(0)
	default:
		return uint32(0)
	}
}

func compareIsEqual(firstSortData, secondSortData []int64) bool {
	firstLen := len(firstSortData)
	if firstLen != len(secondSortData) {
		return false
	}

	for i := firstLen - 1; i >= 0; i-- {
		if firstSortData[i] != secondSortData[i] {
			return false
		}
	}

	return true
}

func compareMoreThan(firstSortData, secondSortData []int64) int {
	firstLen := len(firstSortData)
	secondLen := len(secondSortData)
	minLen := firstLen
	if firstLen > secondLen {
		minLen = secondLen
	}

	for i := 0; i < minLen; i++ {
		if firstSortData[i] > secondSortData[i] {
			return 1
		}

		if firstSortData[i] < secondSortData[i] {
			return -1
		}
	}

	return 0
}

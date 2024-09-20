package srand

import (
	"github.com/duanhf2012/origin/v2/util/typedef"
	"math/rand"
	"slices"
)

func Sum[E ~[]T, T typedef.Number](arr E) T {
	var sum T
	for i := range arr {
		sum += arr[i]
	}
	return sum
}

func SumFunc[E ~[]V, V any, T typedef.Number](arr E, getValue func(i int) T) T {
	var sum T
	for i := range arr {
		sum += getValue(i)
	}
	return sum
}

func Shuffle[E ~[]T, T any](arr E) {
	rand.Shuffle(len(arr), func(i, j int) {
		arr[i], arr[j] = arr[j], arr[i]
	})
}

func RandOne[E ~[]T, T any](arr E) T {
	return arr[rand.Intn(len(arr))]
}

func RandN[E ~[]T, T any](arr E, num int) []T {
	index := make([]int, 0, len(arr))
	for i := range arr {
		index = append(index, i)
	}
	Shuffle(index)
	if len(index) > num {
		index = index[:num]
	}
	ret := make([]T, 0, len(index))
	for i := range index {
		ret = append(ret, arr[index[i]])
	}
	
	return ret
}

func RandWeight[E ~[]T, T typedef.Integer](weights E) int {
	totalWeight := Sum(weights)
	if totalWeight <= 0 {
		return -1
	}

	t := T(rand.Intn(int(totalWeight)))
	for i := range weights {
		if t < weights[i] {
			return i
		}
		t -= weights[i]
	}
	return -1
}

func RandWeightFunc[E ~[]U, U any, T typedef.Integer](arr E, getWeight func(i int) T) int {
	weights := make([]T, 0, len(arr))
	for i := range arr {
		weights = append(weights, getWeight(i))
	}
	return RandWeight(weights)
}

func Get[E ~[]T, T any, U typedef.Integer](arr E, index U) (ret T, ok bool) {
	if index < 0 || int(index) >= len(arr) {
		return
	}
	ret = arr[index]
	ok = true
	return
}

func GetPointer[E ~[]T, T any, U typedef.Integer](arr E, index U) *T {
	if index < 0 || int(index) >= len(arr) {
		return nil
	}
	return &arr[index]
}

func GetFunc[E ~[]T, T any](arr E, f func(T) bool) (ret T, ok bool) {
	index := slices.IndexFunc(arr, f)
	if index < 0 {
		return
	}

	return arr[index], true
}

func GetPointerFunc[E ~[]T, T any](arr E, f func(T) bool) *T {
	index := slices.IndexFunc(arr, f)
	if index < 0 {
		return nil
	}

	return &arr[index]
}

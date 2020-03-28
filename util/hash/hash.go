package hash

import "hash/crc32"

func HashNumber(s string) uint {
	return uint(crc32.ChecksumIEEE([]byte(s)))
}
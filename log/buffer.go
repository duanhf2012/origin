package log

import (
	"strconv"
)

const _size = 9216

type Buffer struct {
	bs []byte
	//mu     sync.Mutex // ensures atomic writes; protects the following fields
}

func (buff *Buffer) Init() {
	buff.bs = make([]byte, _size)
}

// AppendByte writes a single byte to the Buffer.
func (buff *Buffer) AppendByte(v byte) {
	buff.bs = append(buff.bs, v)
}

func (buff *Buffer) AppendBytes(v []byte) {
	buff.bs = append(buff.bs, v...)
}

// AppendString writes a string to the Buffer.
func (buff *Buffer) AppendString(s string) {
	buff.bs = append(buff.bs, s...)
}

// AppendInt appends an integer to the underlying buffer (assuming base 10).
func (buff *Buffer) AppendInt(i int64) {
	buff.bs = strconv.AppendInt(buff.bs, i, 10)
}

// AppendUint appends an unsigned integer to the underlying buffer (assuming
// base 10).
func (buff *Buffer) AppendUint(i uint64) {
	buff.bs = strconv.AppendUint(buff.bs, i, 10)
}

// AppendBool appends a bool to the underlying buffer.
func (buff *Buffer) AppendBool(v bool) {
	buff.bs = strconv.AppendBool(buff.bs, v)
}

// AppendFloat appends a float to the underlying buffer. It doesn't quote NaN
// or +/- Inf.
func (buff *Buffer) AppendFloat(f float64, bitSize int) {
	buff.bs = strconv.AppendFloat(buff.bs, f, 'f', -1, bitSize)
}

// Len returns the length of the underlying byte slice.
func (buff *Buffer) Len() int {
	return len(buff.bs)
}

// Cap returns the capacity of the underlying byte slice.
func (buff *Buffer) Cap() int {
	return cap(buff.bs)
}

// Bytes returns a mutable reference to the underlying byte slice.
func (buff *Buffer) Bytes() []byte {
	return buff.bs
}

// String returns a string copy of the underlying byte slice.
func (buff *Buffer) String() string {
	return string(buff.bs)
}

// Reset resets the underlying byte slice. Subsequent writes re-use the slice's
// backing array.
func (buff *Buffer) Reset() {
	buff.bs = buff.bs[:0]
}

// Write implements io.Writer.
func (buff *Buffer) Write(bs []byte) (int, error) {
	buff.bs = append(buff.bs, bs...)
	return len(bs), nil
}

// TrimNewline trims any final "\n" byte from the end of the buffer.
func (buff *Buffer) TrimNewline() {
	if i := len(buff.bs) - 1; i >= 0 {
		if buff.bs[i] == '\n' {
			buff.bs = buff.bs[:i]
		}
	}
}

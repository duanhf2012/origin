package log // import "go.uber.org/zap/buffer"

import (
	"strconv"
	"sync"
)

const _size = 9216

type Buffer struct {
	bs   []byte
	mu     sync.Mutex // ensures atomic writes; protects the following fields
}

func (buff *Buffer) Init(){
	buff.bs = make([]byte,_size)
}

func (buff *Buffer) Locker() {
	buff.mu.Lock()
}

func (buff *Buffer) UnLocker() {
	buff.mu.Unlock()
}

// AppendByte writes a single byte to the Buffer.
func (b *Buffer) AppendByte(v byte) {
	b.bs = append(b.bs, v)
}

func (b *Buffer) AppendBytes(v []byte) {
	b.bs = append(b.bs, v...)
}

// AppendString writes a string to the Buffer.
func (b *Buffer) AppendString(s string) {
	b.bs = append(b.bs, s...)
}

// AppendInt appends an integer to the underlying buffer (assuming base 10).
func (b *Buffer) AppendInt(i int64) {
	b.bs = strconv.AppendInt(b.bs, i, 10)
}

// AppendUint appends an unsigned integer to the underlying buffer (assuming
// base 10).
func (b *Buffer) AppendUint(i uint64) {
	b.bs = strconv.AppendUint(b.bs, i, 10)
}

// AppendBool appends a bool to the underlying buffer.
func (b *Buffer) AppendBool(v bool) {
	b.bs = strconv.AppendBool(b.bs, v)
}

// AppendFloat appends a float to the underlying buffer. It doesn't quote NaN
// or +/- Inf.
func (b *Buffer) AppendFloat(f float64, bitSize int) {
	b.bs = strconv.AppendFloat(b.bs, f, 'f', -1, bitSize)
}

// Len returns the length of the underlying byte slice.
func (b *Buffer) Len() int {
	return len(b.bs)
}

// Cap returns the capacity of the underlying byte slice.
func (b *Buffer) Cap() int {
	return cap(b.bs)
}

// Bytes returns a mutable reference to the underlying byte slice.
func (b *Buffer) Bytes() []byte {
	return b.bs
}

// String returns a string copy of the underlying byte slice.
func (b *Buffer) String() string {
	return string(b.bs)
}

// Reset resets the underlying byte slice. Subsequent writes re-use the slice's
// backing array.
func (b *Buffer) Reset() {
	b.bs = b.bs[:0]
}

// Write implements io.Writer.
func (b *Buffer) Write(bs []byte) (int, error) {
	b.bs = append(b.bs, bs...)
	return len(bs), nil
}

// TrimNewline trims any final "\n" byte from the end of the buffer.
func (b *Buffer) TrimNewline() {
	if i := len(b.bs) - 1; i >= 0 {
		if b.bs[i] == '\n' {
			b.bs = b.bs[:i]
		}
	}
}


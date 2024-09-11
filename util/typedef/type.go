package typedef

type Signed interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64
}

type Unsigned interface {
	~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

type Float interface {
	~float32 | ~float64
}

type Integer interface {
	Signed|Unsigned
}

type Number interface {
	Signed|Unsigned
}

type Ordered interface {
	Number|Float|~string
}

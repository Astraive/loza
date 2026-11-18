package jsonenc

import (
	"math"
	"strconv"
)

// AppendInt64 appends the decimal representation of v to dst.
func AppendInt64(dst []byte, v int64) []byte {
	return strconv.AppendInt(dst, v, 10)
}

// AppendUint64 appends the decimal representation of v to dst.
func AppendUint64(dst []byte, v uint64) []byte {
	return strconv.AppendUint(dst, v, 10)
}

// AppendFloat64 appends v to dst as a JSON number.
// NaN and Inf are encoded as null to stay valid JSON.
func AppendFloat64(dst []byte, v float64) []byte {
	switch {
	case math.IsNaN(v):
		return append(dst, "null"...)
	case math.IsInf(v, 0):
		return append(dst, "null"...)
	}
	return strconv.AppendFloat(dst, v, 'f', -1, 64)
}

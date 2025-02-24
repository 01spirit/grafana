package models // import "github.com/influxdata/influxdb1-client/models"

import (
	"reflect"
	"strconv"
	"unsafe"
)

func parseIntBytes(b []byte, base int, bitSize int) (i int64, err error) {
	s := unsafeBytesToString(b)
	return strconv.ParseInt(s, base, bitSize)
}

func parseUintBytes(b []byte, base int, bitSize int) (i uint64, err error) {
	s := unsafeBytesToString(b)
	return strconv.ParseUint(s, base, bitSize)
}

func parseFloatBytes(b []byte, bitSize int) (float64, error) {
	s := unsafeBytesToString(b)
	return strconv.ParseFloat(s, bitSize)
}

func parseBoolBytes(b []byte) (bool, error) {
	return strconv.ParseBool(unsafeBytesToString(b))
}

func unsafeBytesToString(in []byte) string {
	src := *(*reflect.SliceHeader)(unsafe.Pointer(&in))
	dst := reflect.StringHeader{
		Data: src.Data,
		Len:  src.Len,
	}
	s := *(*string)(unsafe.Pointer(&dst))
	return s
}

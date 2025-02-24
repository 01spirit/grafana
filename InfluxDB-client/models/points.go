// Package models implements basic objects used throughout the TICK stack.
package models // import "github.com/influxdata/influxdb1-client/models"

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/influxdata/influxdb1-client/pkg/escape"
)

type escapeSet struct {
	k   [1]byte
	esc [2]byte
}

var (
	measurementEscapeCodes = [...]escapeSet{
		{k: [1]byte{','}, esc: [2]byte{'\\', ','}},
		{k: [1]byte{' '}, esc: [2]byte{'\\', ' '}},
	}

	tagEscapeCodes = [...]escapeSet{
		{k: [1]byte{','}, esc: [2]byte{'\\', ','}},
		{k: [1]byte{' '}, esc: [2]byte{'\\', ' '}},
		{k: [1]byte{'='}, esc: [2]byte{'\\', '='}},
	}

	ErrPointMustHaveAField = errors.New("point without fields is unsupported")

	ErrInvalidNumber = errors.New("invalid number")

	ErrInvalidPoint = errors.New("point is invalid")
)

const (
	MaxKeyLength = 65535
)

var enableUint64Support = false

func EnableUintSupport() {
	enableUint64Support = true
}

type Point interface {
	Name() []byte

	SetName(string)

	Tags() Tags

	ForEachTag(fn func(k, v []byte) bool)

	AddTag(key, value string)

	SetTags(tags Tags)

	HasTag(tag []byte) bool

	Fields() (Fields, error)

	Time() time.Time

	SetTime(t time.Time)

	UnixNano() int64

	HashID() uint64

	Key() []byte

	String() string

	MarshalBinary() ([]byte, error)

	PrecisionString(precision string) string

	RoundedString(d time.Duration) string

	Split(size int) []Point

	Round(d time.Duration)

	StringSize() int

	AppendString(buf []byte) []byte

	FieldIterator() FieldIterator
}

type FieldType int

const (
	Integer FieldType = iota

	Float

	Boolean

	String

	Empty

	Unsigned
)

type FieldIterator interface {
	Next() bool

	FieldKey() []byte

	Type() FieldType

	StringValue() string

	IntegerValue() (int64, error)

	UnsignedValue() (uint64, error)

	BooleanValue() (bool, error)

	FloatValue() (float64, error)

	Reset()
}

type Points []Point

func (a Points) Len() int { return len(a) }

func (a Points) Less(i, j int) bool { return a[i].Time().Before(a[j].Time()) }

func (a Points) Swap(i, j int) { a[i], a[j] = a[j], a[i] }

type point struct {
	time time.Time

	key []byte

	fields []byte

	ts []byte

	cachedFields map[string]interface{}

	cachedName string

	cachedTags Tags

	it fieldIterator
}

var (
	_ Point         = (*point)(nil)
	_ FieldIterator = (*point)(nil)
)

const (
	maxInt64Digits = 19

	minInt64Digits = 20

	maxUint64Digits = 20

	maxFloat64Digits = 25

	minFloat64Digits = 27
)

func ParsePoints(buf []byte) ([]Point, error) {
	return ParsePointsWithPrecision(buf, time.Now().UTC(), "n")
}

func ParsePointsString(buf string) ([]Point, error) {
	return ParsePoints([]byte(buf))
}

func ParseKey(buf []byte) (string, Tags) {
	name, tags := ParseKeyBytes(buf)
	return string(name), tags
}

func ParseKeyBytes(buf []byte) ([]byte, Tags) {
	return ParseKeyBytesWithTags(buf, nil)
}

func ParseKeyBytesWithTags(buf []byte, tags Tags) ([]byte, Tags) {
	state, i, _ := scanMeasurement(buf, 0)

	var name []byte
	if state == tagKeyState {
		tags = parseTags(buf, tags)
		name = buf[:i-1]
	} else {
		name = buf[:i]
	}
	return unescapeMeasurement(name), tags
}

func ParseTags(buf []byte) Tags {
	return parseTags(buf, nil)
}

func ParseName(buf []byte) []byte {
	state, i, _ := scanMeasurement(buf, 0)
	var name []byte
	if state == tagKeyState {
		name = buf[:i-1]
	} else {
		name = buf[:i]
	}

	return unescapeMeasurement(name)
}

func ParsePointsWithPrecision(buf []byte, defaultTime time.Time, precision string) ([]Point, error) {
	points := make([]Point, 0, bytes.Count(buf, []byte{'\n'})+1)
	var (
		pos    int
		block  []byte
		failed []string
	)
	for pos < len(buf) {
		pos, block = scanLine(buf, pos)
		pos++

		if len(block) == 0 {
			continue
		}

		start := skipWhitespace(block, 0)

		if start >= len(block) {
			continue
		}

		if block[start] == '#' {
			continue
		}

		if block[len(block)-1] == '\n' {
			block = block[:len(block)-1]
		}

		pt, err := parsePoint(block[start:], defaultTime, precision)
		if err != nil {
			failed = append(failed, fmt.Sprintf("unable to parse '%s': %v", string(block[start:]), err))
		} else {
			points = append(points, pt)
		}

	}
	if len(failed) > 0 {
		return points, fmt.Errorf("%s", strings.Join(failed, "\n"))
	}
	return points, nil

}

func parsePoint(buf []byte, defaultTime time.Time, precision string) (Point, error) {
	pos, key, err := scanKey(buf, 0)
	if err != nil {
		return nil, err
	}

	if len(key) == 0 {
		return nil, fmt.Errorf("missing measurement")
	}

	if len(key) > MaxKeyLength {
		return nil, fmt.Errorf("max key length exceeded: %v > %v", len(key), MaxKeyLength)
	}

	pos, fields, err := scanFields(buf, pos)
	if err != nil {
		return nil, err
	}

	if len(fields) == 0 {
		return nil, fmt.Errorf("missing fields")
	}

	var maxKeyErr error
	err = walkFields(fields, func(k, v []byte) bool {
		if sz := seriesKeySize(key, k); sz > MaxKeyLength {
			maxKeyErr = fmt.Errorf("max key length exceeded: %v > %v", sz, MaxKeyLength)
			return false
		}
		return true
	})

	if err != nil {
		return nil, err
	}

	if maxKeyErr != nil {
		return nil, maxKeyErr
	}

	pos, ts, err := scanTime(buf, pos)
	if err != nil {
		return nil, err
	}

	pt := &point{
		key:    key,
		fields: fields,
		ts:     ts,
	}

	if len(ts) == 0 {
		pt.time = defaultTime
		pt.SetPrecision(precision)
	} else {
		ts, err := parseIntBytes(ts, 10, 64)
		if err != nil {
			return nil, err
		}
		pt.time, err = SafeCalcTime(ts, precision)
		if err != nil {
			return nil, err
		}

		for pos < len(buf) {
			if buf[pos] != ' ' {
				return nil, ErrInvalidPoint
			}
			pos++
		}
	}
	return pt, nil
}

func GetPrecisionMultiplier(precision string) int64 {
	d := time.Nanosecond
	switch precision {
	case "u":
		d = time.Microsecond
	case "ms":
		d = time.Millisecond
	case "s":
		d = time.Second
	case "m":
		d = time.Minute
	case "h":
		d = time.Hour
	}
	return int64(d)
}

func scanKey(buf []byte, i int) (int, []byte, error) {
	start := skipWhitespace(buf, i)

	i = start

	sorted := true

	indices := make([]int, 100)

	commas := 0

	state, i, err := scanMeasurement(buf, i)
	if err != nil {
		return i, buf[start:i], err
	}

	if state == tagKeyState {
		i, commas, indices, err = scanTags(buf, i, indices)
		if err != nil {
			return i, buf[start:i], err
		}
	}

	for j := 0; j < commas-1; j++ {
		_, left := scanTo(buf[indices[j]:indices[j+1]-1], 0, '=')
		_, right := scanTo(buf[indices[j+1]:indices[j+2]-1], 0, '=')

		if cmp := bytes.Compare(left, right); cmp > 0 {
			sorted = false
			break
		} else if cmp == 0 {
			return i, buf[start:i], fmt.Errorf("duplicate tags")
		}
	}

	if !sorted && commas > 0 {
		measurement := buf[start : indices[0]-1]

		indices := indices[:commas]
		insertionSort(0, commas, buf, indices)

		b := make([]byte, len(buf[start:i]))
		pos := copy(b, measurement)
		for _, i := range indices {
			b[pos] = ','
			pos++
			_, v := scanToSpaceOr(buf, i, ',')
			pos += copy(b[pos:], v)
		}

		for j := 0; j < commas-1; j++ {
			_, left := scanTo(buf[indices[j]:], 0, '=')
			_, right := scanTo(buf[indices[j+1]:], 0, '=')

			if bytes.Equal(left, right) {
				return i, b, fmt.Errorf("duplicate tags")
			}
		}

		return i, b, nil
	}

	return i, buf[start:i], nil
}

const (
	tagKeyState = iota
	tagValueState
	fieldsState
)

func scanMeasurement(buf []byte, i int) (int, int, error) {
	if i >= len(buf) || buf[i] == ',' {
		return -1, i, fmt.Errorf("missing measurement")
	}

	for {
		i++
		if i >= len(buf) {
			// cpu
			return -1, i, fmt.Errorf("missing fields")
		}

		if buf[i-1] == '\\' {
			continue
		}

		if buf[i] == ',' {
			return tagKeyState, i + 1, nil
		}

		if buf[i] == ' ' {
			// cpu value=1.0
			return fieldsState, i, nil
		}
	}
}

func scanTags(buf []byte, i int, indices []int) (int, int, []int, error) {
	var (
		err    error
		commas int
		state  = tagKeyState
	)

	for {
		switch state {
		case tagKeyState:
			if commas >= len(indices) {
				newIndics := make([]int, cap(indices)*2)
				copy(newIndics, indices)
				indices = newIndics
			}
			indices[commas] = i
			commas++

			i, err = scanTagsKey(buf, i)
			state = tagValueState
		case tagValueState:
			state, i, err = scanTagsValue(buf, i)
		case fieldsState:
			indices[commas] = i + 1
			return i, commas, indices, nil
		}

		if err != nil {
			return i, commas, indices, err
		}
	}
}

func scanTagsKey(buf []byte, i int) (int, error) {
	if i >= len(buf) || buf[i] == ' ' || buf[i] == ',' || buf[i] == '=' {
		// cpu,{'', ' ', ',', '='}
		return i, fmt.Errorf("missing tag key")
	}
	for {
		i++
		if i >= len(buf) ||
			((buf[i] == ' ' || buf[i] == ',') && buf[i-1] != '\\') {
			// cpu,tag{'', ' ', ','}
			return i, fmt.Errorf("missing tag value")
		}

		if buf[i] == '=' && buf[i-1] != '\\' {
			// cpu,tag=
			return i + 1, nil
		}
	}
}

func scanTagsValue(buf []byte, i int) (int, int, error) {
	if i >= len(buf) || buf[i] == ',' || buf[i] == ' ' {
		// cpu,tag={',', ' '}
		return -1, i, fmt.Errorf("missing tag value")
	}

	for {
		i++
		if i >= len(buf) {
			// cpu,tag=value
			return -1, i, fmt.Errorf("missing fields")
		}

		if buf[i] == '=' && buf[i-1] != '\\' {
			// cpu,tag={'=', 'fo=o'}
			return -1, i, fmt.Errorf("invalid tag format")
		}

		if buf[i] == ',' && buf[i-1] != '\\' {
			// cpu,tag=foo,
			return tagKeyState, i + 1, nil
		}

		if buf[i] == ' ' && buf[i-1] != '\\' {
			return fieldsState, i, nil
		}
	}
}

func insertionSort(l, r int, buf []byte, indices []int) {
	for i := l + 1; i < r; i++ {
		for j := i; j > l && less(buf, indices, j, j-1); j-- {
			indices[j], indices[j-1] = indices[j-1], indices[j]
		}
	}
}

func less(buf []byte, indices []int, i, j int) bool {
	_, a := scanTo(buf, indices[i], '=')
	_, b := scanTo(buf, indices[j], '=')
	return bytes.Compare(a, b) < 0
}

func scanFields(buf []byte, i int) (int, []byte, error) {
	start := skipWhitespace(buf, i)
	i = start
	quoted := false

	equals := 0

	commas := 0

	for {
		if i >= len(buf) {
			break
		}

		if buf[i] == '\\' && i+1 < len(buf) {
			i += 2
			continue
		}

		if buf[i] == '"' && equals > commas {
			quoted = !quoted
			i++
			continue
		}

		if buf[i] == '=' && !quoted {
			equals++

			if buf[i-1] == ' ' && buf[i-2] != '\\' {
				return i, buf[start:i], fmt.Errorf("missing field key")
			}

			if buf[i-1] == ',' && buf[i-2] != '\\' {
				return i, buf[start:i], fmt.Errorf("missing field key")
			}

			if i+1 >= len(buf) {
				return i, buf[start:i], fmt.Errorf("missing field value")
			}

			if buf[i+1] == ',' || buf[i+1] == ' ' {
				return i, buf[start:i], fmt.Errorf("missing field value")
			}

			if isNumeric(buf[i+1]) || buf[i+1] == '-' || buf[i+1] == 'N' || buf[i+1] == 'n' {
				var err error
				i, err = scanNumber(buf, i+1)
				if err != nil {
					return i, buf[start:i], err
				}
				continue
			}
			if buf[i+1] != '"' {
				var err error
				i, _, err = scanBoolean(buf, i+1)
				if err != nil {
					return i, buf[start:i], err
				}
				continue
			}
		}

		if buf[i] == ',' && !quoted {
			commas++
		}

		if buf[i] == ' ' && !quoted {
			break
		}
		i++
	}

	if quoted {
		return i, buf[start:i], fmt.Errorf("unbalanced quotes")
	}

	if equals == 0 || commas != equals-1 {
		return i, buf[start:i], fmt.Errorf("invalid field format")
	}

	return i, buf[start:i], nil
}

func scanTime(buf []byte, i int) (int, []byte, error) {
	start := skipWhitespace(buf, i)
	i = start

	for {
		if i >= len(buf) {
			break
		}

		if buf[i] == '\n' || buf[i] == ' ' {
			break
		}

		if i == start && buf[i] == '-' {
			i++
			continue
		}

		if buf[i] < '0' || buf[i] > '9' {
			return i, buf[start:i], fmt.Errorf("bad timestamp")
		}
		i++
	}
	return i, buf[start:i], nil
}

func isNumeric(b byte) bool {
	return (b >= '0' && b <= '9') || b == '.'
}

func scanNumber(buf []byte, i int) (int, error) {
	start := i
	var isInt, isUnsigned bool

	if i < len(buf) && buf[i] == '-' {
		i++
		if i == len(buf) {
			return i, ErrInvalidNumber
		}
	}

	decimal := false

	scientific := false

	for {
		if i >= len(buf) {
			break
		}

		if buf[i] == ',' || buf[i] == ' ' {
			break
		}

		if buf[i] == 'i' && i > start && !(isInt || isUnsigned) {
			isInt = true
			i++
			continue
		} else if buf[i] == 'u' && i > start && !(isInt || isUnsigned) {
			isUnsigned = true
			i++
			continue
		}

		if buf[i] == '.' {
			if decimal {
				return i, ErrInvalidNumber
			}
			decimal = true
		}

		if i > start && (buf[i] == 'e' || buf[i] == 'E') {
			scientific = true
			i++
			continue
		}

		if (buf[i] == '+' || buf[i] == '-') && (buf[i-1] == 'e' || buf[i-1] == 'E') {
			i++
			continue
		}

		if i+2 < len(buf) && (buf[i] == 'N' || buf[i] == 'n') {
			return i, ErrInvalidNumber
		}

		if !isNumeric(buf[i]) {
			return i, ErrInvalidNumber
		}
		i++
	}

	if (isInt || isUnsigned) && (decimal || scientific) {
		return i, ErrInvalidNumber
	}

	numericDigits := i - start
	if isInt {
		numericDigits--
	}
	if decimal {
		numericDigits--
	}
	if buf[start] == '-' {
		numericDigits--
	}

	if numericDigits == 0 {
		return i, ErrInvalidNumber
	}

	if isInt {
		if buf[i-1] != 'i' {
			return i, ErrInvalidNumber
		}
		if len(buf[start:i-1]) >= maxInt64Digits || len(buf[start:i-1]) >= minInt64Digits {
			if _, err := parseIntBytes(buf[start:i-1], 10, 64); err != nil {
				return i, fmt.Errorf("unable to parse integer %s: %s", buf[start:i-1], err)
			}
		}
	} else if isUnsigned {
		if !enableUint64Support {
			return i, ErrInvalidNumber
		}
		if buf[i-1] != 'u' {
			return i, ErrInvalidNumber
		}
		if buf[start] == '-' {
			return i, ErrInvalidNumber
		}
		if len(buf[start:i-1]) >= maxUint64Digits {
			if _, err := parseUintBytes(buf[start:i-1], 10, 64); err != nil {
				return i, fmt.Errorf("unable to parse unsigned %s: %s", buf[start:i-1], err)
			}
		}
	} else {
		if scientific || len(buf[start:i]) >= maxFloat64Digits || len(buf[start:i]) >= minFloat64Digits {
			if _, err := parseFloatBytes(buf[start:i], 10); err != nil {
				return i, fmt.Errorf("invalid float")
			}
		}
	}

	return i, nil
}

func scanBoolean(buf []byte, i int) (int, []byte, error) {
	start := i

	if i < len(buf) && (buf[i] != 't' && buf[i] != 'f' && buf[i] != 'T' && buf[i] != 'F') {
		return i, buf[start:i], fmt.Errorf("invalid boolean")
	}

	i++
	for {
		if i >= len(buf) {
			break
		}

		if buf[i] == ',' || buf[i] == ' ' {
			break
		}
		i++
	}

	if i-start == 1 {
		return i, buf[start:i], nil
	}

	if (buf[start] == 't' || buf[start] == 'T') && i-start != 4 {
		return i, buf[start:i], fmt.Errorf("invalid boolean")
	}

	if (buf[start] == 'f' || buf[start] == 'F') && i-start != 5 {
		return i, buf[start:i], fmt.Errorf("invalid boolean")
	}

	valid := false
	switch buf[start] {
	case 't':
		valid = bytes.Equal(buf[start:i], []byte("true"))
	case 'f':
		valid = bytes.Equal(buf[start:i], []byte("false"))
	case 'T':
		valid = bytes.Equal(buf[start:i], []byte("TRUE")) || bytes.Equal(buf[start:i], []byte("True"))
	case 'F':
		valid = bytes.Equal(buf[start:i], []byte("FALSE")) || bytes.Equal(buf[start:i], []byte("False"))
	}

	if !valid {
		return i, buf[start:i], fmt.Errorf("invalid boolean")
	}

	return i, buf[start:i], nil

}

func skipWhitespace(buf []byte, i int) int {
	for i < len(buf) {
		if buf[i] != ' ' && buf[i] != '\t' && buf[i] != 0 {
			break
		}
		i++
	}
	return i
}

func scanLine(buf []byte, i int) (int, []byte) {
	start := i
	quoted := false
	fields := false

	equals := 0
	commas := 0
	for {
		if i >= len(buf) {
			break
		}

		if buf[i] == '\\' && i+2 < len(buf) {
			i += 2
			continue
		}

		if buf[i] == ' ' {
			fields = true
		}

		if fields {
			if !quoted && buf[i] == '=' {
				i++
				equals++
				continue
			} else if !quoted && buf[i] == ',' {
				i++
				commas++
				continue
			} else if buf[i] == '"' && equals > commas {
				i++
				quoted = !quoted
				continue
			}
		}

		if buf[i] == '\n' && !quoted {
			break
		}

		i++
	}

	return i, buf[start:i]
}

func scanTo(buf []byte, i int, stop byte) (int, []byte) {
	start := i
	for {
		if i >= len(buf) {
			break
		}

		if buf[i] == stop && (i == 0 || buf[i-1] != '\\') {
			break
		}
		i++
	}

	return i, buf[start:i]
}

func scanToSpaceOr(buf []byte, i int, stop byte) (int, []byte) {
	start := i
	if buf[i] == stop || buf[i] == ' ' {
		return i, buf[start:i]
	}

	for {
		i++
		if buf[i-1] == '\\' {
			continue
		}

		if i >= len(buf) {
			return i, buf[start:i]
		}

		if buf[i] == stop || buf[i] == ' ' {
			return i, buf[start:i]
		}
	}
}

func scanTagValue(buf []byte, i int) (int, []byte) {
	start := i
	for {
		if i >= len(buf) {
			break
		}

		if buf[i] == ',' && buf[i-1] != '\\' {
			break
		}
		i++
	}
	if i > len(buf) {
		return i, nil
	}
	return i, buf[start:i]
}

func scanFieldValue(buf []byte, i int) (int, []byte) {
	start := i
	quoted := false
	for i < len(buf) {
		if buf[i] == '\\' && i+1 < len(buf) && (buf[i+1] == '"' || buf[i+1] == '\\') {
			i += 2
			continue
		}

		if buf[i] == '"' {
			i++
			quoted = !quoted
			continue
		}

		if buf[i] == ',' && !quoted {
			break
		}
		i++
	}
	return i, buf[start:i]
}

func EscapeMeasurement(in []byte) []byte {
	for _, c := range measurementEscapeCodes {
		if bytes.IndexByte(in, c.k[0]) != -1 {
			in = bytes.Replace(in, c.k[:], c.esc[:], -1)
		}
	}
	return in
}

func unescapeMeasurement(in []byte) []byte {
	if bytes.IndexByte(in, '\\') == -1 {
		return in
	}

	for i := range measurementEscapeCodes {
		c := &measurementEscapeCodes[i]
		if bytes.IndexByte(in, c.k[0]) != -1 {
			in = bytes.Replace(in, c.esc[:], c.k[:], -1)
		}
	}
	return in
}

func escapeTag(in []byte) []byte {
	for i := range tagEscapeCodes {
		c := &tagEscapeCodes[i]
		if bytes.IndexByte(in, c.k[0]) != -1 {
			in = bytes.Replace(in, c.k[:], c.esc[:], -1)
		}
	}
	return in
}

func unescapeTag(in []byte) []byte {
	if bytes.IndexByte(in, '\\') == -1 {
		return in
	}

	for i := range tagEscapeCodes {
		c := &tagEscapeCodes[i]
		if bytes.IndexByte(in, c.k[0]) != -1 {
			in = bytes.Replace(in, c.esc[:], c.k[:], -1)
		}
	}
	return in
}

// escapeStringFieldReplacer replaces double quotes and backslashes
// with the same character preceded by a backslash.
// As of Go 1.7 this benchmarked better in allocations and CPU time
// compared to iterating through a string byte-by-byte and appending to a new byte slice,
// calling strings.Replace twice, and better than (*Regex).ReplaceAllString.
var escapeStringFieldReplacer = strings.NewReplacer(`"`, `\"`, `\`, `\\`)

// EscapeStringField returns a copy of in with any double quotes or
// backslashes with escaped values.
func EscapeStringField(in string) string {
	return escapeStringFieldReplacer.Replace(in)
}

// unescapeStringField returns a copy of in with any escaped double-quotes
// or backslashes unescaped.
func unescapeStringField(in string) string {
	if strings.IndexByte(in, '\\') == -1 {
		return in
	}

	var out []byte
	i := 0
	for {
		if i >= len(in) {
			break
		}
		// unescape backslashes
		if in[i] == '\\' && i+1 < len(in) && in[i+1] == '\\' {
			out = append(out, '\\')
			i += 2
			continue
		}
		// unescape double-quotes
		if in[i] == '\\' && i+1 < len(in) && in[i+1] == '"' {
			out = append(out, '"')
			i += 2
			continue
		}
		out = append(out, in[i])
		i++

	}
	return string(out)
}

// NewPoint returns a new point with the given measurement name, tags, fields and timestamp.  If
// an unsupported field value (NaN, or +/-Inf) or out of range time is passed, this function
// returns an error.
func NewPoint(name string, tags Tags, fields Fields, t time.Time) (Point, error) {
	key, err := pointKey(name, tags, fields, t)
	if err != nil {
		return nil, err
	}

	return &point{
		key:    key,
		time:   t,
		fields: fields.MarshalBinary(),
	}, nil
}

// pointKey checks some basic requirements for valid points, and returns the
// key, along with an possible error.
func pointKey(measurement string, tags Tags, fields Fields, t time.Time) ([]byte, error) {
	if len(fields) == 0 {
		return nil, ErrPointMustHaveAField
	}

	if !t.IsZero() {
		if err := CheckTime(t); err != nil {
			return nil, err
		}
	}

	for key, value := range fields {
		switch value := value.(type) {
		case float64:
			// Ensure the caller validates and handles invalid field values
			if math.IsInf(value, 0) {
				return nil, fmt.Errorf("+/-Inf is an unsupported value for field %s", key)
			}
			if math.IsNaN(value) {
				return nil, fmt.Errorf("NaN is an unsupported value for field %s", key)
			}
		case float32:
			// Ensure the caller validates and handles invalid field values
			if math.IsInf(float64(value), 0) {
				return nil, fmt.Errorf("+/-Inf is an unsupported value for field %s", key)
			}
			if math.IsNaN(float64(value)) {
				return nil, fmt.Errorf("NaN is an unsupported value for field %s", key)
			}
		}
		if len(key) == 0 {
			return nil, fmt.Errorf("all fields must have non-empty names")
		}
	}

	key := MakeKey([]byte(measurement), tags)
	for field := range fields {
		sz := seriesKeySize(key, []byte(field))
		if sz > MaxKeyLength {
			return nil, fmt.Errorf("max key length exceeded: %v > %v", sz, MaxKeyLength)
		}
	}

	return key, nil
}

func seriesKeySize(key, field []byte) int {
	// 4 is the length of the tsm1.fieldKeySeparator constant.  It's inlined here to avoid a circular
	// dependency.
	return len(key) + 4 + len(field)
}

// NewPointFromBytes returns a new Point from a marshalled Point.
func NewPointFromBytes(b []byte) (Point, error) {
	p := &point{}
	if err := p.UnmarshalBinary(b); err != nil {
		return nil, err
	}

	// This does some basic validation to ensure there are fields and they
	// can be unmarshalled as well.
	iter := p.FieldIterator()
	var hasField bool
	for iter.Next() {
		if len(iter.FieldKey()) == 0 {
			continue
		}
		hasField = true
		switch iter.Type() {
		case Float:
			_, err := iter.FloatValue()
			if err != nil {
				return nil, fmt.Errorf("unable to unmarshal field %s: %s", string(iter.FieldKey()), err)
			}
		case Integer:
			_, err := iter.IntegerValue()
			if err != nil {
				return nil, fmt.Errorf("unable to unmarshal field %s: %s", string(iter.FieldKey()), err)
			}
		case Unsigned:
			_, err := iter.UnsignedValue()
			if err != nil {
				return nil, fmt.Errorf("unable to unmarshal field %s: %s", string(iter.FieldKey()), err)
			}
		case String:
			// Skip since this won't return an error
		case Boolean:
			_, err := iter.BooleanValue()
			if err != nil {
				return nil, fmt.Errorf("unable to unmarshal field %s: %s", string(iter.FieldKey()), err)
			}
		}
	}

	if !hasField {
		return nil, ErrPointMustHaveAField
	}

	return p, nil
}

// MustNewPoint returns a new point with the given measurement name, tags, fields and timestamp.  If
// an unsupported field value (NaN) is passed, this function panics.
func MustNewPoint(name string, tags Tags, fields Fields, time time.Time) Point {
	pt, err := NewPoint(name, tags, fields, time)
	if err != nil {
		panic(err.Error())
	}
	return pt
}

// Key returns the key (measurement joined with tags) of the point.
func (p *point) Key() []byte {
	return p.key
}

func (p *point) name() []byte {
	_, name := scanTo(p.key, 0, ',')
	return name
}

func (p *point) Name() []byte {
	return escape.Unescape(p.name())
}

// SetName updates the measurement name for the point.
func (p *point) SetName(name string) {
	p.cachedName = ""
	p.key = MakeKey([]byte(name), p.Tags())
}

// Time return the timestamp for the point.
func (p *point) Time() time.Time {
	return p.time
}

// SetTime updates the timestamp for the point.
func (p *point) SetTime(t time.Time) {
	p.time = t
}

// Round will round the timestamp of the point to the given duration.
func (p *point) Round(d time.Duration) {
	p.time = p.time.Round(d)
}

// Tags returns the tag set for the point.
func (p *point) Tags() Tags {
	if p.cachedTags != nil {
		return p.cachedTags
	}
	p.cachedTags = parseTags(p.key, nil)
	return p.cachedTags
}

func (p *point) ForEachTag(fn func(k, v []byte) bool) {
	walkTags(p.key, fn)
}

func (p *point) HasTag(tag []byte) bool {
	if len(p.key) == 0 {
		return false
	}

	var exists bool
	walkTags(p.key, func(key, value []byte) bool {
		if bytes.Equal(tag, key) {
			exists = true
			return false
		}
		return true
	})

	return exists
}

func walkTags(buf []byte, fn func(key, value []byte) bool) {
	if len(buf) == 0 {
		return
	}

	pos, name := scanTo(buf, 0, ',')

	// it's an empty key, so there are no tags
	if len(name) == 0 {
		return
	}

	hasEscape := bytes.IndexByte(buf, '\\') != -1
	i := pos + 1
	var key, value []byte
	for {
		if i >= len(buf) {
			break
		}
		i, key = scanTo(buf, i, '=')
		i, value = scanTagValue(buf, i+1)

		if len(value) == 0 {
			continue
		}

		if hasEscape {
			if !fn(unescapeTag(key), unescapeTag(value)) {
				return
			}
		} else {
			if !fn(key, value) {
				return
			}
		}

		i++
	}
}

// walkFields walks each field key and value via fn.  If fn returns false, the iteration
// is stopped.  The values are the raw byte slices and not the converted types.
func walkFields(buf []byte, fn func(key, value []byte) bool) error {
	var i int
	var key, val []byte
	for len(buf) > 0 {
		i, key = scanTo(buf, 0, '=')
		if i > len(buf)-2 {
			return fmt.Errorf("invalid value: field-key=%s", key)
		}
		buf = buf[i+1:]
		i, val = scanFieldValue(buf, 0)
		buf = buf[i:]
		if !fn(key, val) {
			break
		}

		// slice off comma
		if len(buf) > 0 {
			buf = buf[1:]
		}
	}
	return nil
}

// parseTags parses buf into the provided destination tags, returning destination
// Tags, which may have a different length and capacity.
func parseTags(buf []byte, dst Tags) Tags {
	if len(buf) == 0 {
		return nil
	}

	n := bytes.Count(buf, []byte(","))
	if cap(dst) < n {
		dst = make(Tags, n)
	} else {
		dst = dst[:n]
	}

	// Ensure existing behaviour when point has no tags and nil slice passed in.
	if dst == nil {
		dst = Tags{}
	}

	// Series keys can contain escaped commas, therefore the number of commas
	// in a series key only gives an estimation of the upper bound on the number
	// of tags.
	var i int
	walkTags(buf, func(key, value []byte) bool {
		dst[i].Key, dst[i].Value = key, value
		i++
		return true
	})
	return dst[:i]
}

// MakeKey creates a key for a set of tags.
func MakeKey(name []byte, tags Tags) []byte {
	return AppendMakeKey(nil, name, tags)
}

// AppendMakeKey appends the key derived from name and tags to dst and returns the extended buffer.
func AppendMakeKey(dst []byte, name []byte, tags Tags) []byte {
	// unescape the name and then re-escape it to avoid double escaping.
	// The key should always be stored in escaped form.
	dst = append(dst, EscapeMeasurement(unescapeMeasurement(name))...)
	dst = tags.AppendHashKey(dst)
	return dst
}

// SetTags replaces the tags for the point.
func (p *point) SetTags(tags Tags) {
	p.key = MakeKey(p.Name(), tags)
	p.cachedTags = tags
}

// AddTag adds or replaces a tag value for a point.
func (p *point) AddTag(key, value string) {
	tags := p.Tags()
	tags = append(tags, Tag{Key: []byte(key), Value: []byte(value)})
	sort.Sort(tags)
	p.cachedTags = tags
	p.key = MakeKey(p.Name(), tags)
}

// Fields returns the fields for the point.
func (p *point) Fields() (Fields, error) {
	if p.cachedFields != nil {
		return p.cachedFields, nil
	}
	cf, err := p.unmarshalBinary()
	if err != nil {
		return nil, err
	}
	p.cachedFields = cf
	return p.cachedFields, nil
}

// SetPrecision will round a time to the specified precision.
func (p *point) SetPrecision(precision string) {
	switch precision {
	case "n":
	case "u":
		p.SetTime(p.Time().Truncate(time.Microsecond))
	case "ms":
		p.SetTime(p.Time().Truncate(time.Millisecond))
	case "s":
		p.SetTime(p.Time().Truncate(time.Second))
	case "m":
		p.SetTime(p.Time().Truncate(time.Minute))
	case "h":
		p.SetTime(p.Time().Truncate(time.Hour))
	}
}

// String returns the string representation of the point.
func (p *point) String() string {
	if p.Time().IsZero() {
		return string(p.Key()) + " " + string(p.fields)
	}
	return string(p.Key()) + " " + string(p.fields) + " " + strconv.FormatInt(p.UnixNano(), 10)
}

// AppendString appends the string representation of the point to buf.
func (p *point) AppendString(buf []byte) []byte {
	buf = append(buf, p.key...)
	buf = append(buf, ' ')
	buf = append(buf, p.fields...)

	if !p.time.IsZero() {
		buf = append(buf, ' ')
		buf = strconv.AppendInt(buf, p.UnixNano(), 10)
	}

	return buf
}

// StringSize returns the length of the string that would be returned by String().
func (p *point) StringSize() int {
	size := len(p.key) + len(p.fields) + 1

	if !p.time.IsZero() {
		digits := 1 // even "0" has one digit
		t := p.UnixNano()
		if t < 0 {
			// account for negative sign, then negate
			digits++
			t = -t
		}
		for t > 9 { // already accounted for one digit
			digits++
			t /= 10
		}
		size += digits + 1 // digits and a space
	}

	return size
}

// MarshalBinary returns a binary representation of the point.
func (p *point) MarshalBinary() ([]byte, error) {
	if len(p.fields) == 0 {
		return nil, ErrPointMustHaveAField
	}

	tb, err := p.time.MarshalBinary()
	if err != nil {
		return nil, err
	}

	b := make([]byte, 8+len(p.key)+len(p.fields)+len(tb))
	i := 0

	binary.BigEndian.PutUint32(b[i:], uint32(len(p.key)))
	i += 4

	i += copy(b[i:], p.key)

	binary.BigEndian.PutUint32(b[i:i+4], uint32(len(p.fields)))
	i += 4

	i += copy(b[i:], p.fields)

	copy(b[i:], tb)
	return b, nil
}

// UnmarshalBinary decodes a binary representation of the point into a point struct.
func (p *point) UnmarshalBinary(b []byte) error {
	var n int

	// Read key length.
	if len(b) < 4 {
		return io.ErrShortBuffer
	}
	n, b = int(binary.BigEndian.Uint32(b[:4])), b[4:]

	// Read key.
	if len(b) < n {
		return io.ErrShortBuffer
	}
	p.key, b = b[:n], b[n:]

	// Read fields length.
	if len(b) < 4 {
		return io.ErrShortBuffer
	}
	n, b = int(binary.BigEndian.Uint32(b[:4])), b[4:]

	// Read fields.
	if len(b) < n {
		return io.ErrShortBuffer
	}
	p.fields, b = b[:n], b[n:]

	// Read timestamp.
	return p.time.UnmarshalBinary(b)
}

// PrecisionString returns a string representation of the point. If there
// is a timestamp associated with the point then it will be specified in the
// given unit.
func (p *point) PrecisionString(precision string) string {
	if p.Time().IsZero() {
		return fmt.Sprintf("%s %s", p.Key(), string(p.fields))
	}
	return fmt.Sprintf("%s %s %d", p.Key(), string(p.fields),
		p.UnixNano()/GetPrecisionMultiplier(precision))
}

// RoundedString returns a string representation of the point. If there
// is a timestamp associated with the point, then it will be rounded to the
// given duration.
func (p *point) RoundedString(d time.Duration) string {
	if p.Time().IsZero() {
		return fmt.Sprintf("%s %s", p.Key(), string(p.fields))
	}
	return fmt.Sprintf("%s %s %d", p.Key(), string(p.fields),
		p.time.Round(d).UnixNano())
}

func (p *point) unmarshalBinary() (Fields, error) {
	iter := p.FieldIterator()
	fields := make(Fields, 8)
	for iter.Next() {
		if len(iter.FieldKey()) == 0 {
			continue
		}
		switch iter.Type() {
		case Float:
			v, err := iter.FloatValue()
			if err != nil {
				return nil, fmt.Errorf("unable to unmarshal field %s: %s", string(iter.FieldKey()), err)
			}
			fields[string(iter.FieldKey())] = v
		case Integer:
			v, err := iter.IntegerValue()
			if err != nil {
				return nil, fmt.Errorf("unable to unmarshal field %s: %s", string(iter.FieldKey()), err)
			}
			fields[string(iter.FieldKey())] = v
		case Unsigned:
			v, err := iter.UnsignedValue()
			if err != nil {
				return nil, fmt.Errorf("unable to unmarshal field %s: %s", string(iter.FieldKey()), err)
			}
			fields[string(iter.FieldKey())] = v
		case String:
			fields[string(iter.FieldKey())] = iter.StringValue()
		case Boolean:
			v, err := iter.BooleanValue()
			if err != nil {
				return nil, fmt.Errorf("unable to unmarshal field %s: %s", string(iter.FieldKey()), err)
			}
			fields[string(iter.FieldKey())] = v
		}
	}
	return fields, nil
}

// HashID returns a non-cryptographic checksum of the point's key.
func (p *point) HashID() uint64 {
	h := NewInlineFNV64a()
	h.Write(p.key)
	sum := h.Sum64()
	return sum
}

// UnixNano returns the timestamp of the point as nanoseconds since Unix epoch.
func (p *point) UnixNano() int64 {
	return p.Time().UnixNano()
}

// Split will attempt to return multiple points with the same timestamp whose
// string representations are no longer than size. Points with a single field or
// a point without a timestamp may exceed the requested size.
func (p *point) Split(size int) []Point {
	if p.time.IsZero() || p.StringSize() <= size {
		return []Point{p}
	}

	// key string, timestamp string, spaces
	size -= len(p.key) + len(strconv.FormatInt(p.time.UnixNano(), 10)) + 2

	var points []Point
	var start, cur int

	for cur < len(p.fields) {
		end, _ := scanTo(p.fields, cur, '=')
		end, _ = scanFieldValue(p.fields, end+1)

		if cur > start && end-start > size {
			points = append(points, &point{
				key:    p.key,
				time:   p.time,
				fields: p.fields[start : cur-1],
			})
			start = cur
		}

		cur = end + 1
	}

	points = append(points, &point{
		key:    p.key,
		time:   p.time,
		fields: p.fields[start:],
	})

	return points
}

// Tag represents a single key/value tag pair.
type Tag struct {
	Key   []byte
	Value []byte
}

// NewTag returns a new Tag.
func NewTag(key, value []byte) Tag {
	return Tag{
		Key:   key,
		Value: value,
	}
}

// Size returns the size of the key and value.
func (t Tag) Size() int { return len(t.Key) + len(t.Value) }

// Clone returns a shallow copy of Tag.
//
// Tags associated with a Point created by ParsePointsWithPrecision will hold references to the byte slice that was parsed.
// Use Clone to create a Tag with new byte slices that do not refer to the argument to ParsePointsWithPrecision.
func (t Tag) Clone() Tag {
	other := Tag{
		Key:   make([]byte, len(t.Key)),
		Value: make([]byte, len(t.Value)),
	}

	copy(other.Key, t.Key)
	copy(other.Value, t.Value)

	return other
}

// String returns the string reprsentation of the tag.
func (t *Tag) String() string {
	var buf bytes.Buffer
	buf.WriteByte('{')
	buf.WriteString(string(t.Key))
	buf.WriteByte(' ')
	buf.WriteString(string(t.Value))
	buf.WriteByte('}')
	return buf.String()
}

// Tags represents a sorted list of tags.
type Tags []Tag

// NewTags returns a new Tags from a map.
func NewTags(m map[string]string) Tags {
	if len(m) == 0 {
		return nil
	}
	a := make(Tags, 0, len(m))
	for k, v := range m {
		a = append(a, NewTag([]byte(k), []byte(v)))
	}
	sort.Sort(a)
	return a
}

// HashKey hashes all of a tag's keys.
func (a Tags) HashKey() []byte {
	return a.AppendHashKey(nil)
}

func (a Tags) needsEscape() bool {
	for i := range a {
		t := &a[i]
		for j := range tagEscapeCodes {
			c := &tagEscapeCodes[j]
			if bytes.IndexByte(t.Key, c.k[0]) != -1 || bytes.IndexByte(t.Value, c.k[0]) != -1 {
				return true
			}
		}
	}
	return false
}

// AppendHashKey appends the result of hashing all of a tag's keys and values to dst and returns the extended buffer.
func (a Tags) AppendHashKey(dst []byte) []byte {
	// Empty maps marshal to empty bytes.
	if len(a) == 0 {
		return dst
	}

	// Type invariant: Tags are sorted

	sz := 0
	var escaped Tags
	if a.needsEscape() {
		var tmp [20]Tag
		if len(a) < len(tmp) {
			escaped = tmp[:len(a)]
		} else {
			escaped = make(Tags, len(a))
		}

		for i := range a {
			t := &a[i]
			nt := &escaped[i]
			nt.Key = escapeTag(t.Key)
			nt.Value = escapeTag(t.Value)
			sz += len(nt.Key) + len(nt.Value)
		}
	} else {
		sz = a.Size()
		escaped = a
	}

	sz += len(escaped) + (len(escaped) * 2) // separators

	// Generate marshaled bytes.
	if cap(dst)-len(dst) < sz {
		nd := make([]byte, len(dst), len(dst)+sz)
		copy(nd, dst)
		dst = nd
	}
	buf := dst[len(dst) : len(dst)+sz]
	idx := 0
	for i := range escaped {
		k := &escaped[i]
		if len(k.Value) == 0 {
			continue
		}
		buf[idx] = ','
		idx++
		copy(buf[idx:], k.Key)
		idx += len(k.Key)
		buf[idx] = '='
		idx++
		copy(buf[idx:], k.Value)
		idx += len(k.Value)
	}
	return dst[:len(dst)+idx]
}

// String returns the string representation of the tags.
func (a Tags) String() string {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i := range a {
		buf.WriteString(a[i].String())
		if i < len(a)-1 {
			buf.WriteByte(' ')
		}
	}
	buf.WriteByte(']')
	return buf.String()
}

// Size returns the number of bytes needed to store all tags. Note, this is
// the number of bytes needed to store all keys and values and does not account
// for data structures or delimiters for example.
func (a Tags) Size() int {
	var total int
	for i := range a {
		total += a[i].Size()
	}
	return total
}

// Clone returns a copy of the slice where the elements are a result of calling `Clone` on the original elements
//
// Tags associated with a Point created by ParsePointsWithPrecision will hold references to the byte slice that was parsed.
// Use Clone to create Tags with new byte slices that do not refer to the argument to ParsePointsWithPrecision.
func (a Tags) Clone() Tags {
	if len(a) == 0 {
		return nil
	}

	others := make(Tags, len(a))
	for i := range a {
		others[i] = a[i].Clone()
	}

	return others
}

func (a Tags) Len() int           { return len(a) }
func (a Tags) Less(i, j int) bool { return bytes.Compare(a[i].Key, a[j].Key) == -1 }
func (a Tags) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }

// Equal returns true if a equals other.
func (a Tags) Equal(other Tags) bool {
	if len(a) != len(other) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i].Key, other[i].Key) || !bytes.Equal(a[i].Value, other[i].Value) {
			return false
		}
	}
	return true
}

// CompareTags returns -1 if a < b, 1 if a > b, and 0 if a == b.
func CompareTags(a, b Tags) int {
	// Compare each key & value until a mismatch.
	for i := 0; i < len(a) && i < len(b); i++ {
		if cmp := bytes.Compare(a[i].Key, b[i].Key); cmp != 0 {
			return cmp
		}
		if cmp := bytes.Compare(a[i].Value, b[i].Value); cmp != 0 {
			return cmp
		}
	}

	// If all tags are equal up to this point then return shorter tagset.
	if len(a) < len(b) {
		return -1
	} else if len(a) > len(b) {
		return 1
	}

	// All tags are equal.
	return 0
}

// Get returns the value for a key.
func (a Tags) Get(key []byte) []byte {
	// OPTIMIZE: Use sort.Search if tagset is large.

	for _, t := range a {
		if bytes.Equal(t.Key, key) {
			return t.Value
		}
	}
	return nil
}

// GetString returns the string value for a string key.
func (a Tags) GetString(key string) string {
	return string(a.Get([]byte(key)))
}

// Set sets the value for a key.
func (a *Tags) Set(key, value []byte) {
	for i, t := range *a {
		if bytes.Equal(t.Key, key) {
			(*a)[i].Value = value
			return
		}
	}
	*a = append(*a, Tag{Key: key, Value: value})
	sort.Sort(*a)
}

// SetString sets the string value for a string key.
func (a *Tags) SetString(key, value string) {
	a.Set([]byte(key), []byte(value))
}

// Map returns a map representation of the tags.
func (a Tags) Map() map[string]string {
	m := make(map[string]string, len(a))
	for _, t := range a {
		m[string(t.Key)] = string(t.Value)
	}
	return m
}

// CopyTags returns a shallow copy of tags.
func CopyTags(a Tags) Tags {
	other := make(Tags, len(a))
	copy(other, a)
	return other
}

// DeepCopyTags returns a deep copy of tags.
func DeepCopyTags(a Tags) Tags {
	// Calculate size of keys/values in bytes.
	var n int
	for _, t := range a {
		n += len(t.Key) + len(t.Value)
	}

	// Build single allocation for all key/values.
	buf := make([]byte, n)

	// Copy tags to new set.
	other := make(Tags, len(a))
	for i, t := range a {
		copy(buf, t.Key)
		other[i].Key, buf = buf[:len(t.Key)], buf[len(t.Key):]

		copy(buf, t.Value)
		other[i].Value, buf = buf[:len(t.Value)], buf[len(t.Value):]
	}

	return other
}

// Fields represents a mapping between a Point's field names and their
// values.
type Fields map[string]interface{}

// FieldIterator retuns a FieldIterator that can be used to traverse the
// fields of a point without constructing the in-memory map.
func (p *point) FieldIterator() FieldIterator {
	p.Reset()
	return p
}

type fieldIterator struct {
	start, end  int
	key, keybuf []byte
	valueBuf    []byte
	fieldType   FieldType
}

// Next indicates whether there any fields remaining.
func (p *point) Next() bool {
	p.it.start = p.it.end
	if p.it.start >= len(p.fields) {
		return false
	}

	p.it.end, p.it.key = scanTo(p.fields, p.it.start, '=')
	if escape.IsEscaped(p.it.key) {
		p.it.keybuf = escape.AppendUnescaped(p.it.keybuf[:0], p.it.key)
		p.it.key = p.it.keybuf
	}

	p.it.end, p.it.valueBuf = scanFieldValue(p.fields, p.it.end+1)
	p.it.end++

	if len(p.it.valueBuf) == 0 {
		p.it.fieldType = Empty
		return true
	}

	c := p.it.valueBuf[0]

	if c == '"' {
		p.it.fieldType = String
		return true
	}

	if strings.IndexByte(`0123456789-.nNiIu`, c) >= 0 {
		if p.it.valueBuf[len(p.it.valueBuf)-1] == 'i' {
			p.it.fieldType = Integer
			p.it.valueBuf = p.it.valueBuf[:len(p.it.valueBuf)-1]
		} else if p.it.valueBuf[len(p.it.valueBuf)-1] == 'u' {
			p.it.fieldType = Unsigned
			p.it.valueBuf = p.it.valueBuf[:len(p.it.valueBuf)-1]
		} else {
			p.it.fieldType = Float
		}
		return true
	}

	// to keep the same behavior that currently exists, default to boolean
	p.it.fieldType = Boolean
	return true
}

// FieldKey returns the key of the current field.
func (p *point) FieldKey() []byte {
	return p.it.key
}

// Type returns the FieldType of the current field.
func (p *point) Type() FieldType {
	return p.it.fieldType
}

// StringValue returns the string value of the current field.
func (p *point) StringValue() string {
	return unescapeStringField(string(p.it.valueBuf[1 : len(p.it.valueBuf)-1]))
}

// IntegerValue returns the integer value of the current field.
func (p *point) IntegerValue() (int64, error) {
	n, err := parseIntBytes(p.it.valueBuf, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("unable to parse integer value %q: %v", p.it.valueBuf, err)
	}
	return n, nil
}

// UnsignedValue returns the unsigned value of the current field.
func (p *point) UnsignedValue() (uint64, error) {
	n, err := parseUintBytes(p.it.valueBuf, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("unable to parse unsigned value %q: %v", p.it.valueBuf, err)
	}
	return n, nil
}

// BooleanValue returns the boolean value of the current field.
func (p *point) BooleanValue() (bool, error) {
	b, err := parseBoolBytes(p.it.valueBuf)
	if err != nil {
		return false, fmt.Errorf("unable to parse bool value %q: %v", p.it.valueBuf, err)
	}
	return b, nil
}

// FloatValue returns the float value of the current field.
func (p *point) FloatValue() (float64, error) {
	f, err := parseFloatBytes(p.it.valueBuf, 64)
	if err != nil {
		return 0, fmt.Errorf("unable to parse floating point value %q: %v", p.it.valueBuf, err)
	}
	return f, nil
}

// Reset resets the iterator to its initial state.
func (p *point) Reset() {
	p.it.fieldType = Empty
	p.it.key = nil
	p.it.valueBuf = nil
	p.it.start = 0
	p.it.end = 0
}

// MarshalBinary encodes all the fields to their proper type and returns the binary
// represenation
// NOTE: uint64 is specifically not supported due to potential overflow when we decode
// again later to an int64
// NOTE2: uint is accepted, and may be 64 bits, and is for some reason accepted...
func (p Fields) MarshalBinary() []byte {
	var b []byte
	keys := make([]string, 0, len(p))

	for k := range p {
		keys = append(keys, k)
	}

	// Not really necessary, can probably be removed.
	sort.Strings(keys)

	for i, k := range keys {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendField(b, k, p[k])
	}

	return b
}

func appendField(b []byte, k string, v interface{}) []byte {
	b = append(b, []byte(escape.String(k))...)
	b = append(b, '=')

	// check popular types first
	switch v := v.(type) {
	case float64:
		b = strconv.AppendFloat(b, v, 'f', -1, 64)
	case int64:
		b = strconv.AppendInt(b, v, 10)
		b = append(b, 'i')
	case string:
		b = append(b, '"')
		b = append(b, []byte(EscapeStringField(v))...)
		b = append(b, '"')
	case bool:
		b = strconv.AppendBool(b, v)
	case int32:
		b = strconv.AppendInt(b, int64(v), 10)
		b = append(b, 'i')
	case int16:
		b = strconv.AppendInt(b, int64(v), 10)
		b = append(b, 'i')
	case int8:
		b = strconv.AppendInt(b, int64(v), 10)
		b = append(b, 'i')
	case int:
		b = strconv.AppendInt(b, int64(v), 10)
		b = append(b, 'i')
	case uint64:
		b = strconv.AppendUint(b, v, 10)
		b = append(b, 'u')
	case uint32:
		b = strconv.AppendInt(b, int64(v), 10)
		b = append(b, 'i')
	case uint16:
		b = strconv.AppendInt(b, int64(v), 10)
		b = append(b, 'i')
	case uint8:
		b = strconv.AppendInt(b, int64(v), 10)
		b = append(b, 'i')
	case uint:
		// TODO: 'uint' should be converted to writing as an unsigned integer,
		// but we cannot since that would break backwards compatibility.
		b = strconv.AppendInt(b, int64(v), 10)
		b = append(b, 'i')
	case float32:
		b = strconv.AppendFloat(b, float64(v), 'f', -1, 32)
	case []byte:
		b = append(b, v...)
	case nil:
		// skip
	default:
		// Can't determine the type, so convert to string
		b = append(b, '"')
		b = append(b, []byte(EscapeStringField(fmt.Sprintf("%v", v)))...)
		b = append(b, '"')

	}

	return b
}

// ValidKeyToken returns true if the token used for measurement, tag key, or tag
// value is a valid unicode string and only contains printable, non-replacement characters.
func ValidKeyToken(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) || r == unicode.ReplacementChar {
			return false
		}
	}
	return true
}

// ValidKeyTokens returns true if the measurement name and all tags are valid.
func ValidKeyTokens(name string, tags Tags) bool {
	if !ValidKeyToken(name) {
		return false
	}
	for _, tag := range tags {
		if !ValidKeyToken(string(tag.Key)) || !ValidKeyToken(string(tag.Value)) {
			return false
		}
	}
	return true
}

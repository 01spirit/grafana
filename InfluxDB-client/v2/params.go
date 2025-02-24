package influxdb_client

import (
	"encoding/json"
	"time"
)

type (
	Identifier string

	StringValue string

	RegexValue string

	NumberValue float64

	IntegerValue int64

	BooleanValue bool

	TimeValue time.Time

	DurationValue time.Duration
)

func (v Identifier) MarshalJSON() ([]byte, error) {
	m := map[string]string{"identifier": string(v)}
	return json.Marshal(m)
}

func (v StringValue) MarshalJSON() ([]byte, error) {
	m := map[string]string{"string": string(v)}
	return json.Marshal(m)
}

func (v RegexValue) MarshalJSON() ([]byte, error) {
	m := map[string]string{"regex": string(v)}
	return json.Marshal(m)
}

func (v NumberValue) MarshalJSON() ([]byte, error) {
	m := map[string]float64{"number": float64(v)}
	return json.Marshal(m)
}

func (v IntegerValue) MarshalJSON() ([]byte, error) {
	m := map[string]int64{"integer": int64(v)}
	return json.Marshal(m)
}

func (v BooleanValue) MarshalJSON() ([]byte, error) {
	m := map[string]bool{"boolean": bool(v)}
	return json.Marshal(m)
}

func (v TimeValue) MarshalJSON() ([]byte, error) {
	t := time.Time(v)
	m := map[string]string{"string": t.Format(time.RFC3339Nano)}
	return json.Marshal(m)
}

func (v DurationValue) MarshalJSON() ([]byte, error) {
	m := map[string]int64{"duration": int64(v)}
	return json.Marshal(m)
}

package models

import (
	"sort"
)

type Row struct {
	Name    string            `json:"name,omitempty"`
	Tags    map[string]string `json:"tags,omitempty"`
	Columns []string          `json:"columns,omitempty"`
	Values  [][]interface{}   `json:"values,omitempty"`
	Partial bool              `json:"partial,omitempty"`
}

func (r *Row) SameSeries(o *Row) bool {
	return r.tagsHash() == o.tagsHash() && r.Name == o.Name
}

func (r *Row) tagsHash() uint64 {
	h := NewInlineFNV64a()
	keys := r.tagsKeys()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte(r.Tags[k]))
	}
	return h.Sum64()
}

func (r *Row) tagsKeys() []string {
	a := make([]string, 0, len(r.Tags))
	for k := range r.Tags {
		a = append(a, k)
	}
	sort.Strings(a)
	return a
}

type Rows []*Row

func (p Rows) Len() int { return len(p) }

func (p Rows) Less(i, j int) bool {
	if p[i].Name != p[j].Name {
		return p[i].Name < p[j].Name
	}

	return p[i].tagsHash() < p[j].tagsHash()
}

func (p Rows) Swap(i, j int) { p[i], p[j] = p[j], p[i] }

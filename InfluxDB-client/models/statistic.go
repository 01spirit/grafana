package models

type Statistic struct {
	Name   string                 `json:"name"`
	Tags   map[string]string      `json:"tags"`
	Values map[string]interface{} `json:"values"`
}

func NewStatistic(name string) Statistic {
	return Statistic{
		Name:   name,
		Tags:   make(map[string]string),
		Values: make(map[string]interface{}),
	}
}

type StatisticTags map[string]string

func (t StatisticTags) Merge(tags map[string]string) map[string]string {
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[k] = v
	}

	for k, v := range t {
		if _, ok := tags[k]; !ok {
			out[k] = v
		}
	}
	return out
}

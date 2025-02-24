package main

import (
	"github.com/grafana/grafana/InfluxDB-client/memcache"
	"log"
)

func main() {
	mc := memcache.New("localhost:11213")

	err := mc.Set(&memcache.Item{Key: "mykey", Value: []byte("myvalue"), Expiration: 60, Time_start: 1314123, Time_end: 53421432123})
	if err != nil {
		log.Fatalf("Error setting value: %v", err)
	}

	itemValues, item, err := mc.Get("mykey mykey1", 10, 20)
	if err == memcache.ErrCacheMiss {
		log.Printf("Key not found in cache")
	} else if err != nil {
		log.Fatalf("Error getting value: %v", err)
	} else {
		log.Printf("Value: %s", item.Value)
	}

	for i := range itemValues {
		print(itemValues[i])
	}
}

package timescaledb_client

import (
	"fmt"
	"strings"
)

func GetStarSegment(metric, partialSegment string) string {

	result := fmt.Sprintf("{(%s.*)}%s", metric, partialSegment)

	return result
}

func GetSingleSegment(metric, partialSegment string, tags []string) []string {
	result := make([]string, 0)

	if len(tags) == 0 {
		tmpRes := fmt.Sprintf("{(%s.*)}%s", metric, partialSegment)
		result = append(result, tmpRes)
	} else {
		for _, tag := range tags {
			tmpRes := fmt.Sprintf("{(%s.%s)}%s", metric, tag, partialSegment)
			result = append(result, tmpRes)
		}
	}

	return result
}

func SplitPartialSegment(semanticSegment string) (string, string, string) {
	firstIdx := strings.Index(semanticSegment, "#")
	tagString := semanticSegment[:firstIdx]
	partialSegment := semanticSegment[firstIdx:]

	secondIdx := strings.Index(partialSegment[1:], "#")
	fields := partialSegment[2:secondIdx]

	mStartIdx := strings.Index(tagString, "(")
	mEndIdx := strings.Index(tagString, ".")
	metric := tagString[mStartIdx+1 : mEndIdx]

	return partialSegment, fields, metric
}

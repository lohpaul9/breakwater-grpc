package breakwater

import (
	"fmt"
	"math"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	errMissingMetadata = status.Errorf(codes.InvalidArgument, "missing metadata")
)

// logger is to mock a sophisticated logging system. To simplify the example, we just print out the content.
func logger(format string, a ...interface{}) {
	fmt.Printf("LOG:\t"+format+"\n", a...)
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func roundedInt(x float64) int64 {
	return int64(math.Round(x))
}

type BWParameters struct {
	bFactor      float64
	aFactor      float64
	SLO          int64
	startCredits int64
}

/*
Default values for breakwater parameters:
a = 0.1%,
b = 2%,
d_t = 40% of SLA,
AQM threshold = 2 * d_t
*/
var BWParametersDefault BWParameters = BWParameters{
	bFactor:      0.02,
	aFactor:      0.001,
	SLO:          160,
	startCredits: 100,
}

package breakwater

import (
	"fmt"
	"math"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	errMissingMetadata = status.Errorf(codes.InvalidArgument, "missing metadata")
)

// logger is to mock a sophisticated logging system. To simplify the example, we just print out the content.
func logger(format string, a ...interface{}) {
	if debug {
		// print to stdout with timestamp
		timestamp := time.Now().Format("2006-01-02T15:04:05.999999999-07:00")
		fmt.Printf("LOG: "+timestamp+"|\t"+format+"\n", a...)
	}
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
	BFactor        float64
	AFactor        float64
	SLO            int64
	InitialCredits int64
	Verbose        bool
}

/*
Default values for breakwater parameters:
a = 0.1%,
b = 2%,
d_t = 40% of SLA,
AQM threshold = 2 * d_t
*/
var BWParametersDefault BWParameters = BWParameters{
	BFactor:        0.02,
	AFactor:        0.001,
	SLO:            160,
	InitialCredits: 1000,
	Verbose:        false,
}

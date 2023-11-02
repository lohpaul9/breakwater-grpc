package breakwater

import (
	"runtime/metrics"
	"sync"
	"time"

	"github.com/google/uuid"
)

const RTT_MICROSECOND = 5000                // RTT in microseconds
const DELAY_THRESHOLD_PERCENT float64 = 0.4 // target is 0.4 of SLA as per Breakwater
const MAX_Q_LENGTH = 50                     // max length of queue
var debug bool = false

/*
DATA STRUCTURES:
1. A global map of all active connections, which stores cIssued, cOC and cDemand
2. A queue of all pending requests, also tracking what time the earliest request is (queue head)
3. cTotal
4. cIssued
*/
type Connection struct {
	issued          int64 // issued credits
	issuedWriteLock chan int64
	demand          int64 // number of requests pending
	demandWriteLock chan int64
	id              uuid.UUID
	lastUpdated     chan time.Time // last time new credits were issued
}

type Breakwater struct {
	clientMap sync.Map // Map of client connections
	// requestMap      sync.Map  // Map of requests for time tracking
	lastUpdateTime  time.Time // last time since an RTT update
	numClients      chan int64
	rttLock         chan int64 // Lock for cTotal, cIssued, lastUpdateTime update
	cTotal          int64      // global pool of credits
	cIssued         chan int64 // total credits currently issued
	aFactor         float64    // aggressive factor for increasing credits
	bFactor         float64    // multiplicative factor for decreasing credits
	SLO             int64      // SLA in microseconds
	thresholdDelay  float64    // threshold delay in microseconds
	prevHist        *metrics.Float64Histogram
	currHist        *metrics.Float64Histogram
	id              uuid.UUID
	pendingOutgoing chan int64 // pending outgoing requests
	noCreditBlocker chan int64 // block requests when no credits
	outgoingCredits chan int64 // outgoing credits
}

// // TODO: Add fields for gRPC contexts
// type request struct {
// 	reqID                  uuid.UUID
// 	timeDeductionsMicrosec int64
// }

func InitBreakwater(param BWParameters) (bw *Breakwater) {
	bFactor, aFactor, SLO, InitialCredits := param.BFactor, param.AFactor, param.SLO, param.InitialCredits
	bw = &Breakwater{
		clientMap:      sync.Map{},
		lastUpdateTime: time.Now(),
		numClients:     make(chan int64, 1),
		rttLock:        make(chan int64, 1),
		cTotal:         InitialCredits,
		cIssued:        make(chan int64, 1),
		bFactor:        bFactor,
		aFactor:        aFactor,
		SLO:            SLO,
		thresholdDelay: float64(SLO) * DELAY_THRESHOLD_PERCENT,
		prevHist:       nil,
		currHist:       nil,
		id:             uuid.New(),
		// Outgoing buffer drops requests if > 50 requests in queue
		pendingOutgoing: make(chan int64, MAX_Q_LENGTH),
		noCreditBlocker: make(chan int64, 1),
		outgoingCredits: make(chan int64, 1),
	}
	debug = param.Verbose
	// unblock blocker
	bw.noCreditBlocker <- 1
	// give 1 credit to start
	bw.outgoingCredits <- 1
	// unblock rttLock
	bw.rttLock <- 1
	// zero credits and delay
	bw.numClients <- 0
	bw.cIssued <- 0

	bw.startTimeoutRoutine(20 * time.Second)
	return
}

func (b *Breakwater) startTimeoutRoutine(duration time.Duration) {
	// Start a timer for the specified duration
	timer := time.NewTimer(duration)

	// Start a separate Goroutine to unblock requests after the timer expires
	go func() {
		<-timer.C
		logger("[Timeout]:	Unblocking all requests. Updated spend credits to %d\n", 99999999)
		// Update credits and unblock other requests
		<-b.outgoingCredits
		b.outgoingCredits <- 99999999
		b.unblockNoCreditBlock()
		// close channerls after all requests are unblocked and sent

		// close(b.noCreditBlocker)
		// close(b.outgoingCredits)
		// close(b.pendingOutgoing)
	}()
}

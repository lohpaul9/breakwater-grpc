package breakwater

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

const RTT_MICROSECOND = 5000                // RTT in microseconds
const DELAY_THRESHOLD_PERCENT float64 = 0.4 // target is 0.4 of SLA as per Breakwater
const MAX_Q_LENGTH = 50                     // max length of queue

/**
TODO:
0. Update greatestDelay in main loop
0. Find a way to update RTT (how often is a RTT)?
1. Make breakwater thread safe
2. Find efficient way for polling map of longest request times
3. What to do for clients when not enough credits? Use a channel for credits
- A channel for requests (once done, remove one from channel)
- For sleeping requests in 'queue', get waken up whenever there is a change in credits (via a channel)
- Store the number of credits in the channel, and decrement increment whenever done
*/

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
	clientMap         sync.Map  // Map of client connections
	requestMap        sync.Map  // Map of requests for time tracking
	lastUpdateTime    time.Time // last time since an RTT update
	numClients        chan int64
	rttLock           chan int64   // Lock for cTotal, cIssued, lastUpdateTime update
	cTotal            int64        // global pool of credits
	cIssued           chan int64   // total credits currently issued
	aFactor           float64      // aggressive factor for increasing credits
	bFactor           float64      // multiplicative factor for decreasing credits
	SLO               int64        // SLA in microseconds
	thresholdDelay    float64      // threshold delay in microseconds
	currGreatestDelay chan float64 // current greatest delay in microseconds
	prevGreatestDelay chan float64 // previous greatest delay in microseconds
	id                uuid.UUID
	pendingOutgoing   chan int64 // pending outgoing requests
	noCreditBlocker   chan int64 // block requests when no credits
	outgoingCredits   chan int64 // outgoing credits
}

// Todo: Add fields for gRPC contexts
type request struct {
	reqID                  uuid.UUID
	timeDeductionsMicrosec int64
}

func InitBreakwater(param BWParameters) (bw *Breakwater) {
	bFactor, aFactor, SLO, startCredits := param.bFactor, param.aFactor, param.SLO, param.startCredits
	bw = &Breakwater{
		clientMap:         sync.Map{},
		lastUpdateTime:    time.Now(),
		numClients:        make(chan int64, 1),
		rttLock:           make(chan int64, 1),
		cTotal:            startCredits,
		cIssued:           make(chan int64, 1),
		bFactor:           bFactor,
		aFactor:           aFactor,
		SLO:               SLO,
		thresholdDelay:    float64(SLO) * DELAY_THRESHOLD_PERCENT,
		currGreatestDelay: make(chan float64, 1),
		prevGreatestDelay: make(chan float64, 1),
		id:                uuid.New(),
		// Outgoing buffer drops requests if > 50 requests in queue
		pendingOutgoing: make(chan int64, MAX_Q_LENGTH),
		noCreditBlocker: make(chan int64, 1),
		outgoingCredits: make(chan int64, 1),
	}
	// unblock blocker
	bw.noCreditBlocker <- 1
	// give 1 credit to start
	bw.outgoingCredits <- 1
	// unblock rttLock
	bw.rttLock <- 1
	// zero credits and delay
	bw.numClients <- 0
	bw.currGreatestDelay <- 0
	bw.prevGreatestDelay <- 0
	bw.cIssued <- 0
	return
}

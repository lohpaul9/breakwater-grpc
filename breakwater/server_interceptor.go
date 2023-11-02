package breakwater

import (
	"context"
	"fmt"
	"math"
	"runtime/metrics"
	"strconv"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

/*
Register a client if it is not already registered
Return the Connection object and a boolean indicating if the client is new
loaded is true if the value was loaded instead of stored
*/
func (b *Breakwater) RegisterClient(id uuid.UUID, demand int64) (Connection, bool) {

	var c *Connection = &Connection{
		issued:          0,
		issuedWriteLock: make(chan int64, 1),
		demand:          demand,
		demandWriteLock: make(chan int64, 1),
		id:              id,
		lastUpdated:     make(chan time.Time, 1),
	}
	c.demandWriteLock <- 1
	c.issuedWriteLock <- 1
	// update to be before the last update time
	c.lastUpdated <- time.Now().Add(-1 * time.Second)

	storedConn, loaded := b.clientMap.LoadOrStore(id, *c)
	if !loaded {
		num := <-b.numClients
		b.numClients <- num + 1
	}

	return storedConn.(Connection), loaded
}

/*
Helper to get current time delay
*/
func (b *Breakwater) getDelay() float64 {
	// get the current histogram
	b.currHist = readHistogram()

	if b.prevHist == nil {
		// If prevHist is nil, there is no previous data to compute latency against
		b.prevHist = b.currHist
		return 0.0
	}

	gapLatency := maximumQueuingDelayus(b.prevHist, b.currHist)
	// Store the current histogram for future reference
	b.prevHist = b.currHist

	return gapLatency
}

// we should be able to avoid the GetHistogramDifference function by using the following function
// Find the maximum bucket between two Float64Histogram distributions
func maximumQueuingDelayus(earlier, later *metrics.Float64Histogram) float64 {
	for i := len(earlier.Counts) - 1; i >= 0; i-- {
		if later.Counts[i] > earlier.Counts[i] {
			return later.Buckets[i] * 1000000
		}
	}
	return 0
}

// this function reads the currHist from metrics
func readHistogram() *metrics.Float64Histogram {
	// Create a sample for metric /sched/latencies:seconds and /sync/mutex/wait/total:seconds
	const queueingDelay = "/sched/latencies:seconds"
	measureMutexWait := false

	// Create a sample for the metric.
	sample := make([]metrics.Sample, 1)
	sample[0].Name = queueingDelay
	if measureMutexWait {
		const mutexWait = "/sync/mutex/wait/total:seconds"
		sample[1].Name = mutexWait
	}

	// Sample the metric.
	metrics.Read(sample)

	// Check if the metric is actually supported.
	// If it's not, the resulting value will always have
	// kind KindBad.
	if sample[0].Value.Kind() == metrics.KindBad {
		panic(fmt.Sprintf("metric %q no longer supported", queueingDelay))
	}

	// get the current histogram
	currHist := sample[0].Value.Float64Histogram()

	return currHist
}

// Helper to calculate A additive Factor
func (b *Breakwater) getAdditiveFactor() int64 {
	numClients := <-b.numClients
	b.numClients <- numClients
	return max(roundedInt(b.aFactor*float64(numClients)), 1)
}

func (b *Breakwater) getMultiplicativeFactor(delay float64) float64 {
	adjustingFactor := 1.0 - b.bFactor*((delay-b.thresholdDelay)/b.thresholdDelay)
	adjustingFactor = math.Max(adjustingFactor, 0.5)
	return adjustingFactor
}

/*
Returns true and locks if rtt is unlocked, false otherwise
*/
func (b *Breakwater) isRTTUnlocked() bool {
	select {
	case <-b.rttLock:
		return true
	default:
		return false
	}
}

/*
Function: Update cOC
Runs once every RTT
1. Check the queueing delay against SLA
2. If queueing delay is within SLA, increase cTotal additively
3. If queueing delay is beyond SLA, decrease cTotal multiplicatively
*/
func (b *Breakwater) getUpdatedTotalCredits() int64 {
	delay := b.getDelay()

	if delay < b.thresholdDelay {
		logger("[Updating credits]: Within SLA")
		addFactor := b.getAdditiveFactor()
		return b.cTotal + addFactor
		// b.cTotal += addFactor
	} else {
		logger("[Updating credits]: Beyond SLA, delay is %f threshold is %f", delay, b.thresholdDelay)
		adjustingFactor := b.getMultiplicativeFactor(delay)
		newTotal := roundedInt(adjustingFactor * float64(b.cTotal))
		// Addresses edge case: credits is 0, but we need to process at least 1 request
		// as credits are calculated lazily
		return max(newTotal, 1)
		// TODO: Is there need to send negative credits here? Breakwater is unclear but likely not
	}
}

/*
Every RTT,

(1) update cTotal and cIssued

(2) reset greatestDelay
*/
func (b *Breakwater) rttUpdate() {
	timeSinceLastUpdate := time.Since(b.lastUpdateTime)
	if timeSinceLastUpdate.Microseconds() > RTT_MICROSECOND {
		if b.isRTTUnlocked() {
			if loadShedding {
				newDelay := b.getDelay() // Assume this function returns the new delay
				b.queueingDelayChan <- DelayOperation{Value: newDelay}
				// log the delay
				logger("[RTT Update]: delay is %f", newDelay)
			}
			prevCTotal := b.cTotal
			b.lastUpdateTime = time.Now()

			// Re-calculate total issued (should not be too expensive as # clients are limited)
			var totalIssued int64 = 0
			b.clientMap.Range(func(key, value interface{}) bool {
				totalIssued += value.(Connection).issued
				return true
			})
			<-b.cIssued
			b.cIssued <- totalIssued
			b.cTotal = b.getUpdatedTotalCredits()

			// // Reset greatest delay
			// <-b.prevGreatestDelay
			// b.prevGreatestDelay <- <-b.currGreatestDelay
			// b.currGreatestDelay <- 0

			logger("[Updating credits]: prev cTotal: %d, new cTotal: %d, cIssued: %d", prevCTotal, b.cTotal, totalIssued)
			b.rttLock <- 1
		}
	}
}

/*
Number of over-committed credits per client
*/
func (b *Breakwater) calculateCreditsToOvercommit() int64 {
	numClients := <-b.numClients
	b.numClients <- numClients
	cIssued := <-b.cIssued
	b.cIssued <- cIssued
	return roundedInt(math.Max(float64(b.cTotal-cIssued)/float64(numClients), 1))
}

/*
Returns the lower of demand + cOvercommit
and cPrevious - 1
*/
func (b *Breakwater) getLowerCreditsIssued(cOvercommit int64, demand int64, cPrevious int64) int64 {
	if (demand + cOvercommit) < 0 {
		logger("WARNING: demand + cOvercommit < 0")
		return 1
	}
	cNew := min(demand+cOvercommit, cPrevious-1)
	return cNew
}

/*
Returns the lower of demand + cOvercommit
and cCurr + cAvail (ie we cannot add more than cAvail)
*/
func (b *Breakwater) getHigherCreditsIssued(cOvercommit int64, demand int64, cPrevious int64) int64 {
	if (demand + cOvercommit) < 0 {
		logger("WARNING: demand + cOvercommit < 0")
		return 1
	}
	cIssued := <-b.cIssued
	b.cIssued <- cIssued

	cAvail := b.cTotal - cIssued
	cNew := min(demand+cOvercommit, cPrevious+cAvail)
	// logger("cAvail: %d, cNew: %d, cOvercommit %d, cTotal %d, cPrevious %d", cAvail, cNew, cOvercommit, b.cTotal, cPrevious)
	return cNew
}

func (b *Breakwater) calculateCreditsToIssue(demand int64, connCPrevious int64) (cNew int64) {
	cOverCommit := b.calculateCreditsToOvercommit()

	cIssued := <-b.cIssued
	b.cIssued <- cIssued

	// Here, b.cIssued is OVERALL issued credits, while c.issued is credits issued to a connection
	if cIssued < b.cTotal {
		// There is still space to issue credits
		logger("[Issuing credits]: Under limit")
		cNew = b.getHigherCreditsIssued(cOverCommit, demand, connCPrevious)
	} else {
		// At credit limit, so we only decrease
		logger("[Issuing credits]: Over limit")
		cNew = b.getLowerCreditsIssued(cOverCommit, demand, connCPrevious)
	}

	return max(cNew, 1)
}

/*
Function: Update credits issued to a connection
Runs once every time a request is issued
1. Retrieve demand from metadata
2. Calculate cOC (the new overcommitment value, which is leftover / numClients, or 1)
3. If cIssued < cTotal:
Ideal to be issued is demandX + cOC, but limited by total available (cTotal - cIssued)
4. If cIssued >= cTotal:
We need to rate limit, so we issue demandX + cOC, OR just cX - 1 (ie we do not grant any new credits)
*/
func (b *Breakwater) updateCreditsToIssue(clientID uuid.UUID, demand int64) (cNew int64) {

	connection, ok := b.clientMap.Load(clientID)
	if !ok {
		logger("WARNING: client not found")
		// throw an error
		return 0
	}
	c := connection.(Connection)

	// Lock the connections issued credits
	<-c.issuedWriteLock

	connection, _ = b.clientMap.Load(clientID)
	c = connection.(Connection)

	connTimeOfLastUpdate := <-c.lastUpdated

	connCPrevious := c.issued
	if connTimeOfLastUpdate.After(b.lastUpdateTime) {
		// It was already updated after the last RTT update
		logger("[Issuing credits]: Auto Decr")
		cNew = max(connCPrevious-1, 1)
	} else {
		// not yet updated after the last RT update, so have to update
		logger("[Issuing credits]: Post RTT")
		cNew = b.calculateCreditsToIssue(demand, connCPrevious)
	}

	logger("[Issuing credits]: Client %s, cPrev: %d, cNew: %d", clientID, connCPrevious, cNew)

	// update conn credits
	c.issued = cNew
	b.clientMap.Store(clientID, c)

	// update overall cIssued
	diff := cNew - connCPrevious
	prevCIssued := <-b.cIssued
	b.cIssued <- prevCIssued + diff
	if (prevCIssued + diff) < 0 {
		logger("WARNING: cIssued < 0")
	}

	c.issuedWriteLock <- 1
	c.lastUpdated <- time.Now()
	return
}

/*
The server side interceptor
It should
1. Manage connections and register requests
2. Check for queueing delays
3. Update credits issued
4. Occassionally update cTotal
*/
func (b *Breakwater) UnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, errMissingMetadata
	}

	demand, err1 := strconv.ParseInt(md["demand"][0], 10, 64)
	clientId, err2 := uuid.Parse(md["id"][0])
	reqId, err3 := uuid.Parse(md["reqid"][0])

	if err1 != nil || err2 != nil || err3 != nil {
		logger("[Received Req]:	Error: malformed metadata")
		return nil, errMissingMetadata
	}

	logger("[Received Req]:	Method: %s, ClientId: %s, ReqId: %s, Demand %d", info.FullMethod, clientId, reqId, demand)

	// Register client if unregistered
	b.RegisterClient(clientId, demand)

	issuedCredits := b.updateCreditsToIssue(clientId, demand)
	logger("[Received Req]:	issued credits is %d", issuedCredits)

	// Piggyback updated credits issued
	// header := metadata.Pairs("credits", strconv.FormatInt(issuedCredits, 10))
	header := metadata.Pairs("credits", strconv.FormatInt(issuedCredits, 10))
	grpc.SendHeader(ctx, header)

	// Start the timer
	// b.requestMap.Store(reqId, request{reqID: reqId, timeDeductionsMicrosec: 0})
	// time_start := time.Now()

	// Call the handler
	logger("[Handling Req]:	Handling req")
	m, err := handler(ctx, req)

	// End the timer
	// time_end := time.Now()
	// elapsed := time_end.Sub(time_start).Microseconds()
	// reqTimer, _ := b.requestMap.Load(reqId)
	// timeDeductions := reqTimer.(request).timeDeductionsMicrosec
	// b.requestMap.Delete(reqId)
	// Account for deductions of outgoing calls
	// delayMicroSeconds := float64(elapsed - timeDeductions)

	if loadShedding {
		responseChan := make(chan float64)
		b.queueingDelayChan <- DelayOperation{Response: responseChan}
		queueingDelay := <-responseChan // This will wait for the response
		logger("[Req handled]: Server-side queuing delay is %f microseconds", queueingDelay)

		if queueingDelay < b.aqmDelay {
			logger("[Load Shedding] not applied, delay within AQM threshold")
		} else {
			logger("[Load Shedding] applied, delay beyond AQM threshold")
			return nil, status.Errorf(codes.ResourceExhausted, "Server-side queuing delay is beyond AQM threshold")
		}
	}

	// Update delay as neccessary
	// currGreatestDelay := <-b.currGreatestDelay
	// b.currGreatestDelay <- math.Max(currGreatestDelay, delayMicroSeconds)

	// Does update once every rtt in separate goroutine
	go b.rttUpdate()

	if err != nil {
		logger("RPC failed with error %v", err)
	}
	return m, err
}

/*
Used as a simple test for client side interceptors
*/
func (b *Breakwater) DummyUnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, errMissingMetadata
	}

	demand, err1 := strconv.ParseInt(md["demand"][0], 10, 64)
	clientId, err2 := uuid.Parse(md["id"][0])
	_, err3 := uuid.Parse(md["reqid"][0])

	if err1 != nil || err2 != nil || err3 != nil {
		logger("[Received Req]:	Error: malformed metadata")
		return nil, errMissingMetadata
	}

	// logger("[Received Req]:	The demand is %d\n", demand)
	logger("[Received Req]:	The clientid is %s\n", clientId)
	// logger("[Received Req]:	reqid is %s\n", reqId)

	// Register client if unregistered
	conn, loaded := b.RegisterClient(clientId, demand)

	// update credits issued
	<-conn.issuedWriteLock
	issuedCredits := conn.issued - 1
	if !loaded {
		issuedCredits = 3
	}
	if issuedCredits == 0 {
		time.Sleep(1 * time.Second)
		issuedCredits = 3
	}
	conn.issued = issuedCredits
	b.clientMap.Store(clientId, conn)
	logger("[Received Req]:	issued credits is %d\n", issuedCredits)

	conn.issuedWriteLock <- 1
	// Piggyback updated credits issued
	header := metadata.Pairs("credits", strconv.FormatInt(issuedCredits, 10))
	grpc.SendHeader(ctx, header)

	m, err := handler(ctx, req)

	if err != nil {
		logger("RPC failed with error %v", err)
	}
	return m, err
}

func (b *Breakwater) PrintOutgoingCredits() {
	o := <-b.outgoingCredits
	logger("Outgoing credits: ", o)
	b.outgoingCredits <- o
}

/*
TODO List of harder problems:
1. Deterministically de-queue client side
2. Measure queuing delay more precisely instead of using processing time
*/

package breakwater

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

/*
Helper to get current demand (not exact due to race conditions, but gives a
fairly precise idea of number of outgoing requests in queue)
*/
func (b *Breakwater) getDemand() (demand int) {
	return len(b.pendingOutgoing)
}

/*
Adds request to the outgoing queue, returns false
and drops request if there are > 50 elements in channel
*/
func (b *Breakwater) queueRequest() bool {
	select {
	case b.pendingOutgoing <- 1:
		return true
	default:
		return false
	}
}

/*
Dequeues request to the outgoing queue,
returns false if queue channel is empty
*/
func (b *Breakwater) dequeueRequest() bool {
	select {
	case <-b.pendingOutgoing:
		return true
	default:
		return false
	}
}

/*
Unblocks blockingCreditQueue
*/
func (b *Breakwater) unblockNoCreditBlock() {
	select {
	case b.noCreditBlocker <- 1:
		return
	default:
		return
	}
}

func (b *Breakwater) UnaryInterceptorClient(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {

	// retrieve price table for downstream clients queueing delay
	// var isDownstream bool = false
	var reqid uuid.UUID
	timeStart := time.Now()
	// var reqTimeData request
	md, ok := metadata.FromIncomingContext(ctx)
	if ok && len(md["reqid"]) > 0 {
		logger("[Before queue]:	is downstream request\n")
		// reqid, _ := uuid.Parse(md["reqid"][0])
		// r, ok := b.requestMap.Load(reqid)
		// isDownstream = true
		// if ok {
		// 	// should always be okay (the reqId should already be stored)
		// 	reqTimeData = r.(request)
		// } else {
		// 	b.requestMap.Store(reqid, request{reqid, 0})
		// }
	} else {
		// This is first upstream client / end user
		reqid = uuid.New()
	}

	// Check if queue is too long
	var added bool = b.queueRequest()
	if !added {
		return status.Errorf(codes.ResourceExhausted, "Client queue too long, request dropped at client %s", b.id.String())
	}

	// A note on non-deterministic channel waiting:
	// While there is no determined order of goroutines waiting,
	// Current implementations use FIFO queues:
	// https://stackoverflow.com/questions/25860633/order-of-goroutine-unblocking-on-single-channel

	for {
		// Unblock if credits are available
		logger("[Waiting in queue]:	Checking if unblock available\n")
		// blocks until credit available
		<-b.noCreditBlocker

		// check that our time spent in queue has not exceeded the aqm threshold
		// if so, we should drop the request
		// time in microseconds
		if useClientTimeExpiration {
			timeTaken := time.Since(timeStart).Microseconds()
			if float64(timeTaken) > b.aqmDelay {
				// drop request
				logger("[Client Req Expired]:	Dropping request due to client side req expiration. Delay (us) was: %d\n", timeTaken)
				b.unblockNoCreditBlock()
				b.dequeueRequest()
				return status.Errorf(codes.ResourceExhausted,
					"Client id %s request expired in queue. Delay (us) was: %d", b.id.String(), timeTaken)
			}
		}

		logger("[Waiting in queue]:	Unblock available, checking if credits are sufficient\n")
		// Check actual number of credits (channel for binary semaphore)
		creditBalance := <-b.outgoingCredits
		if creditBalance > 0 {
			// Decrement credit balance
			creditBalance--
			// Send updated credit balance
			b.outgoingCredits <- creditBalance

			// If there are still credits, unblock other requests
			if creditBalance > 0 {
				b.unblockNoCreditBlock()
			}
			logger("[Waiting in queue]:	Unblocked with credit balance %d\n", creditBalance)
			break
		} else {
			// Else, return to binary semaphore and keep looping
			// Set a minimum credit balance of 0
			b.outgoingCredits <- 0
			// TODO: Consider adding a timeout here
		}
		logger("[Before Req]:	The method name for price table is %s\n")
		// noCreditBlocker will unblock again when another request returns with
		// more credits
	}

	// Get demand
	demand := b.getDemand()
	logger("[Waiting in queue]:	demand is %d\n", demand)
	ctx = metadata.AppendToOutgoingContext(ctx, "demand", strconv.Itoa(demand), "id", b.id.String(), "reqid", reqid.String())

	// After breaking out of request loop, remove request from queue and send request
	// This should never be blocked
	logger("[Waiting in queue]:	Dequeueing and handling request\n")
	b.dequeueRequest()

	var header metadata.MD // variable to store header and trailer
	err := invoker(ctx, method, req, reply, cc, grpc.Header(&header))
	if err != nil {
		// The request failed. This error should be logged and examined.
		return err
	}

	if len(header["credits"]) > 0 {
		cXNew, _ := strconv.ParseInt(header["credits"][0], 10, 64)
		logger("[Received Resp]:	Updated spend credits is %d\n", cXNew)

		// Update credits and unblock other requests
		<-b.outgoingCredits
		b.outgoingCredits <- max(cXNew, 1)
		b.unblockNoCreditBlock()
	} else {
		logger("[Received Resp]:	No spend credits in response\n")
		// If no response, then just put to 1
		outgoingCredits := <-b.outgoingCredits
		b.outgoingCredits <- max(outgoingCredits, 1)
		b.unblockNoCreditBlock()
	}

	// // Update time deductions
	// timeEnd := time.Now()
	// timeElapsed := timeEnd.Sub(timeStart).Microseconds()
	// if isDownstream {
	// 	reqTimeData.timeDeductionsMicrosec += timeElapsed
	// 	// b.requestMap.Store(reqTimeData.reqID, reqTimeData)
	// 	logger("[Received Resp]:	Downstream client - total time deduction %d\n", reqTimeData.timeDeductionsMicrosec)
	// }

	return err
}

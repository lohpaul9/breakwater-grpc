# breakwater-grpc

Breakwater-grpc is a thread-safe implementation for the Breakwater microservice overload control framework, written in Go gRPC for better generalizability. It is designed to converge to stable performance quicker than existing frameworks during demand spikes, as demonstrated in the [breakwater paper](https://www.usenix.org/conference/osdi20/presentation/cho).

## Installation

To use the breakwater-grpc package, you need to have Go installed on your system. You can then install the package using the following command:

```go get -u github.com/Jiali-Xing/breakwater-grpc```

## How to use

Example code demonstrating how to use the breakwater-grpc package is provided in the server1/main.go and client/main.go files. Below is a code snippet that demonstrates setting up a server and a client using the breakwater-grpc package:

```
import (
	"github.com/lohpaul9/breakwater-grpc"
	"google.golang.org/grpc"
)
...
breakwater := bw.InitBreakwater(bw.BWParametersDefault)

// Setup a new gRPC server
s := grpc.NewServer(grpc.UnaryInterceptor(breakwater.UnaryInterceptor), grpc.StreamInterceptor(streamInterceptor))

// Set up a connection to a gRPC server
conn, err := grpc.Dial(*addr, grpc.WithUnaryInterceptor(breakwater.UnaryInterceptorClient), grpc.WithStreamInterceptor(streamInterceptor))
```

# System design and implementation

In the implementation of the Breakwater framework, we wanted to (1) provide a fair basis of comparison between frameworks, and (2) create an implementation that was generalizable and operating system-agnostic, which would thus abstract away the implementation details at the operating system level. This was unlike the original implementation of Breakwater, which was written at using the Shenango above the TCP transport layer, allowing direct access to thread and packet queues. Therefore, the Breakwater framework was re-written at the gRPC interceptor level, which can provide a plug-and-play package for use with their Go microservice software. Interceptors essentially [intercept the execution](https://github.com/grpc/grpc-go/blob/master/examples/features/interceptor/README.md) of each RPC call. 

The core logic of the Breakwater mechanism remains largely unchanged. For each server, with $C_{total}$ demarking the load the server can handle while maintaing its SLO, $C_{total}$ is maintained in the same way as the original implementation - through an additive increase if queuing delay is below some threshold tied to the SLO, and a multiplicative decrease otherwise. Each client also continues to track its total pending request as the client demand, and the system also provides demand speculation with overcommitment as detailed in Breakwater. Our implementation also preserves lazy credit messaging by piggybacking messages through the RPC interceptor.

However, in order to implement Breakwater at the RPC interceptor level instead of at the operating syste level, there had to be certain adaptations to the implementation.

In the original implementation, queueing delay is measured as the maximum of packet queue delay (time between when a packet arrives till when it is processed by a Shenango kernel thread) plus the maximum of thread queueing delay (time between when a thread is created to process a request until it starts executing) in Shenango. This was accessible because the original implementation had access to these queues and also could modify Shenango's runtime library. Queueing delay is then used as the metric to decide whether a server should increase or decrease its load via $C_{total}$. 

However, since the gRPC implementation does not have access to the kernel thread queues in gRPC, we use Go's Runtime metrics to measure [goroutine delay](https://pkg.go.dev/runtime/metrics) instead, which is the time goroutines have spent in the scheduler in a runnable state before actually running. This would be a metric directly analogous to the kernel thread queueing delay in Shenango. 

On the client side, we use Go's channel primitive, where each outgoing request waits on a channel and a single request unblocks for each credit that the client receives. However, Go [does not provide a guarantee](https://tip.golang.org/ref/mem) on the order in which receivers are un-blocked from a channel, which may potentially starve certain requests and result in higher tail latencies, although the mean de-queue time should still be the same. However, we chose to take this approach so that each gRPC interceptor can act independently without the need for a central coordinator to determine the order of request de-queueing. 

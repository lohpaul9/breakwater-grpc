# breakwater-grpc

Breakwater-grpc is a thread-safe implementation for the Breakwater microservice overload control framework, written in Go gRPC for better generalizability. It is designed to converge to stable performance quicker than existing frameworks during demand spikes, as demonstrated in the [breakwater paper](https://www.usenix.org/conference/osdi20/presentation/cho).

## Impementation

The key design features of the Breakwater framework via credit admission were closely followed in this implementation, including Demand Speculation and Overcommitment of credits. The primary difference between the original framework and the gRPC implementation is the measure of delay. In breakwater-grpc, delay is measured on the server-side via total handling time within the server, excluding wait times from downstream calls.

## Installation

To use the breakwater-grpc package, you need to have Go installed on your system. You can then install the package using the following command:

```go get -u github.com/Jiali-Xing/breakwater-grpc```

## How to use

Example code demonstrating how to use the breakwater-grpc package is provided in the server1/main.go and client/main.go files. Below is a code snippet that demonstrates setting up a server and a client using the breakwater-grpc package:

```
import (
	"github.com/Jiali-Xing/breakwater-grpc"
	"google.golang.org/grpc"
)
...
breakwater := bw.InitBreakwater(bw.BWParametersDefault)

// Setup a new gRPC server
s := grpc.NewServer(grpc.UnaryInterceptor(breakwater.UnaryInterceptor), grpc.StreamInterceptor(streamInterceptor))

// Set up a connection to a gRPC server
conn, err := grpc.Dial(*addr, grpc.WithUnaryInterceptor(breakwater.UnaryInterceptorClient), grpc.WithStreamInterceptor(streamInterceptor))
```

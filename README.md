Adapted from 

https://github.com/grpc/grpc-go/tree/master/examples/features/interceptor

# Interceptor

gRPC provides simple APIs to implement and install interceptors on a per
ClientConn/Server basis. Interceptor intercepts the execution of each RPC call.
Users can use interceptors to do logging, authentication/authorization, metrics
collection, and many other functionality that can be shared across RPCs.

## Try it

```
go run server/main.go
```

```
go run client/main.go
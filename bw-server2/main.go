/*
 *
 * Copyright 2018 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

// Binary server is an example server.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"time"

	bw "github.com/lohpaul9/breakwater-grpc/breakwater"

	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/examples/data"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	ecpb "google.golang.org/grpc/examples/features/proto/echo"
	pb "google.golang.org/grpc/examples/features/proto/echo"
)

var (
	// ErrLimitExhausted is returned by the Limiter in case the number of requests overflows the capacity of a Limiter.
	ErrLimitExhausted = errors.New("requests limit exhausted")
)

const fallbackToken = "some-secret-token"

func callUnaryEcho(ctx context.Context, client ecpb.EchoClient, message string) {
	// [critical] to pass ctx to subrequests.
	resp, err := client.UnaryEcho(ctx, &ecpb.EchoRequest{Message: message})
	// The function UnaryEcho above is the RPC stub provided by downstream nodes (server/main.go) to this service to call.
	if err != nil {
		logger("client.UnaryEcho(_) = _, %v: ", err)
	} else {
		logger("UnaryEcho: ", resp.Message)
	}
}

var addr = flag.String("addr", "localhost:50052", "the address to connect to")

var (
	port = flag.Int("port", 50051, "the port to serve on")

	errMissingMetadata = status.Errorf(codes.InvalidArgument, "missing metadata")
)

// logger is to mock a sophisticated logging system. To simplify the example, we just print out the content.
func logger(format string, a ...interface{}) {
	fmt.Printf("LOG:\t"+format+"\n", a...)
}

type server struct {
	pb.UnimplementedEchoServer
	// This line below is the downstream server (an echo client) of the currnt server.
	rgc ecpb.EchoClient
}

func (s *server) UnaryEcho(ctx context.Context, in *pb.EchoRequest) (*pb.EchoResponse, error) {
	// This function is the server-side stub provided by this service to upstream nodes/clients.
	logger("unary echoing message at server 2 %q\n", in.Message)
	// [critical] to pass ctx from upstream to downstream
	// This function is called when the middle tier service behave as a client and dials the downstream nodes.
	callUnaryEcho(ctx, s.rgc, in.Message)
	return &pb.EchoResponse{Message: in.Message}, nil
}

func (s *server) BidirectionalStreamingEcho(stream pb.Echo_BidirectionalStreamingEchoServer) error {
	for {
		in, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			logger("server: error receiving from stream: %v\n", err)
			return err
		}
		logger("bidi echoing message %q\n", in.Message)
		stream.Send(&pb.EchoResponse{Message: in.Message})
	}
}

// wrappedStream wraps around the embedded grpc.ServerStream, and intercepts the RecvMsg and
// SendMsg method call.
type wrappedStream struct {
	grpc.ServerStream
}

func (w *wrappedStream) RecvMsg(m interface{}) error {
	logger("Receive a message (Type: %T) at %s", m, time.Now().Format(time.RFC3339))
	return w.ServerStream.RecvMsg(m)
}

func (w *wrappedStream) SendMsg(m interface{}) error {
	logger("Send a message (Type: %T) at %v", m, time.Now().Format(time.RFC3339))
	return w.ServerStream.SendMsg(m)
}

func newWrappedStream(s grpc.ServerStream) grpc.ServerStream {
	return &wrappedStream{s}
}

func streamInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	// authentication (token verification)
	_, ok := metadata.FromIncomingContext(ss.Context())
	if !ok {
		return errMissingMetadata
	}

	err := handler(srv, newWrappedStream(ss))
	if err != nil {
		logger("RPC failed with error %v", err)
	}
	return err
}

func main() {
	RunBreakwater()
}

func RunBreakwater() {
	flag.Parse()

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		logger("failed to listen: %v", err)
	}

	// Create tls based credential.
	creds, err := credentials.NewServerTLSFromFile(data.Path("x509/server_cert.pem"), data.Path("x509/server_key.pem"))
	if err != nil {
		logger("failed to create credentials: %v", err)
	}

	breakwater := bw.InitBreakwater(bw.BWParametersDefault)

	s := grpc.NewServer(grpc.Creds(creds), grpc.UnaryInterceptor(breakwater.UnaryInterceptor), grpc.StreamInterceptor(streamInterceptor))

	creds_client, err_client := credentials.NewClientTLSFromFile(data.Path("x509/ca_cert.pem"), "x.test.example.com")
	if err_client != nil {
		logger("failed to load credentials: %v", err_client)
	}

	// Set up a connection to the downstream server, but with the client-side interceptor on top of breakWater.
	conn, err := grpc.Dial(*addr, grpc.WithTransportCredentials(creds_client),
		grpc.WithUnaryInterceptor(breakwater.UnaryInterceptorClient))
	if err != nil {
		logger("did not connect: %v", err)
	}

	// Make a echo client rgc and send RPCs.
	// Register EchoServer on the server.
	pb.RegisterEchoServer(s, &server{rgc: ecpb.NewEchoClient(conn)})

	if err := s.Serve(lis); err != nil {
		logger("failed to serve: %v", err)
	}
}

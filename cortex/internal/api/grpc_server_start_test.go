package api

import (
	"context"
	"net"
	"reflect"
	"testing"

	lozav1 "github.com/astraive/loza/gen/go/loza/core"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestGRPCServerHasNoMisleadingStartGRPC guards against reintroducing the
// removed StartGRPC method. That method only logged an address and returned
// nil without ever listening or serving, so callers believing it started a
// server got a silent failure. Serving is now explicitly external via
// RegisterServer + Server.Serve (see cmd/server).
func TestGRPCServerHasNoMisleadingStartGRPC(t *testing.T) {
	t.Parallel()

	_, ok := reflect.TypeOf(&GRPCServer{}).MethodByName("StartGRPC")
	require.False(t, ok, "GRPCServer must not expose a no-op StartGRPC that reports success without serving")
}

// TestGRPCServerServesViaRegisterServerAndExternalServe demonstrates the
// documented path: the service is registered on a grpc.Server and an external
// listener drives Serve. This is the supported replacement for StartGRPC.
func TestGRPCServerServesViaRegisterServerAndExternalServe(t *testing.T) {
	t.Parallel()

	srv := grpc.NewServer()
	(&GRPCServer{}).RegisterServer(srv)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer lis.Close()
	go func() { _ = srv.Serve(lis) }()
	defer srv.GracefulStop()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	resp, err := lozav1.NewCortexServiceClient(conn).Healthz(context.Background(), &lozav1.HealthzRequest{})
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Status)
}

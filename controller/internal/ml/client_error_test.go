package ml

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	pb "github.com/staffops/staffops-anomaly-detection/proto"
)

// erroringMLServer returns an error from every RPC, to exercise the client's
// error-handling branches (metrics increment + error propagation).
type erroringMLServer struct {
	pb.UnimplementedMLDetectorServer
}

func (erroringMLServer) Health(context.Context, *pb.Empty) (*pb.HealthResponse, error) {
	return nil, errors.New("boom")
}

func (erroringMLServer) Forecast(context.Context, *pb.ForecastRequest) (*pb.ForecastResponse, error) {
	return nil, errors.New("boom")
}

func (erroringMLServer) DetectMultivariate(context.Context, *pb.MultivariateRequest) (*pb.MultivariateResponse, error) {
	return nil, errors.New("boom")
}

func newErroringClient(t *testing.T) *Client {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	grpcSrv := grpc.NewServer()
	pb.RegisterMLDetectorServer(grpcSrv, erroringMLServer{})
	go grpcSrv.Serve(lis)
	t.Cleanup(grpcSrv.Stop)

	conn, err := grpc.Dial("passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("bufconn dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return &Client{client: pb.NewMLDetectorClient(conn), conn: conn, timeout: time.Second, enabled: true}
}

// ─── New (enabled) + Close ─────────────────────────────────────────────────────

func TestNew_Enabled_DialsAndCloses(t *testing.T) {
	// grpc.Dial is lazy, so an unreachable endpoint still returns a client
	// without error. This covers the enabled construction path and Close on a
	// real connection.
	c, err := New(config.ML{Enabled: true, Endpoint: "localhost:50999", Timeout: time.Second})
	if err != nil {
		t.Fatalf("enabled New should not error on lazy dial, got: %v", err)
	}
	if !c.Enabled() {
		t.Error("enabled client should report Enabled()=true")
	}
	c.Close() // exercises the conn != nil branch
}

// ─── Health / Forecast / DetectMultivariate error propagation ──────────────────

func TestClient_Health_RPCError(t *testing.T) {
	c := newErroringClient(t)
	if err := c.Health(context.Background()); err == nil {
		t.Error("RPC error from Health should propagate")
	}
}

func TestClient_Health_Disabled_NoOp(t *testing.T) {
	c, _ := New(config.ML{Enabled: false})
	if err := c.Health(context.Background()); err != nil {
		t.Errorf("disabled Health should be a no-op nil, got: %v", err)
	}
}

func TestClient_Forecast_RPCError(t *testing.T) {
	c := newErroringClient(t)
	values := make([]float64, 10)
	timestamps := make([]int64, 10)
	anomaly, err := c.Forecast(context.Background(), "cpu", values, timestamps, 0.9)
	if err == nil {
		t.Error("RPC error from Forecast should propagate")
	}
	if anomaly != nil {
		t.Error("error path should return nil anomaly")
	}
}

func TestClient_DetectMultivariate_RPCError(t *testing.T) {
	c := newErroringClient(t)
	anomaly, err := c.DetectMultivariate(context.Background(), map[string]float64{
		"cpu_ratio":    0.9,
		"memory_ratio": 0.8,
	})
	if err == nil {
		t.Error("RPC error from DetectMultivariate should propagate")
	}
	if anomaly != nil {
		t.Error("error path should return nil anomaly")
	}
}

// ─── Disabled no-op guards ─────────────────────────────────────────────────────

func TestClient_Forecast_Disabled_NoOp(t *testing.T) {
	c, _ := New(config.ML{Enabled: false})
	values := make([]float64, 10)
	anomaly, err := c.Forecast(context.Background(), "cpu", values, make([]int64, 10), 0.9)
	if err != nil || anomaly != nil {
		t.Errorf("disabled Forecast should return nil, nil; got %v, %v", anomaly, err)
	}
}

func TestClient_DetectMultivariate_Disabled_NoOp(t *testing.T) {
	c, _ := New(config.ML{Enabled: false})
	anomaly, err := c.DetectMultivariate(context.Background(), map[string]float64{"a": 1, "b": 2})
	if err != nil || anomaly != nil {
		t.Errorf("disabled DetectMultivariate should return nil, nil; got %v, %v", anomaly, err)
	}
}

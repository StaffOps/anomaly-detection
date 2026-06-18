package ml

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/staffops/staffops-anomaly-detection/proto"
)

const bufSize = 1024 * 1024

// mockMLServer implements pb.MLDetectorServer with configurable responses.
type mockMLServer struct {
	pb.UnimplementedMLDetectorServer
	ready        bool
	isAnomaly    bool
	anomalyScore float64
	willBreach   bool
}

func (s *mockMLServer) Health(_ context.Context, _ *pb.Empty) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Ready: s.ready}, nil
}

func (s *mockMLServer) Forecast(_ context.Context, _ *pb.ForecastRequest) (*pb.ForecastResponse, error) {
	if s.willBreach {
		return &pb.ForecastResponse{
			WillBreachThreshold: true,
			Predicted:           []float64{0.8, 0.85, 0.9},
			Confidence:          0.85,
		}, nil
	}
	return &pb.ForecastResponse{WillBreachThreshold: false}, nil
}

func (s *mockMLServer) DetectMultivariate(_ context.Context, _ *pb.MultivariateRequest) (*pb.MultivariateResponse, error) {
	return &pb.MultivariateResponse{
		IsAnomaly:           s.isAnomaly,
		AnomalyScore:        s.anomalyScore,
		ContributingMetrics: []string{"cpu_ratio", "memory_ratio"},
	}, nil
}

// newBufconnClient creates a *Client backed by an in-process gRPC server.
func newBufconnClient(t *testing.T, srv *mockMLServer) *Client {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	grpcSrv := grpc.NewServer()
	pb.RegisterMLDetectorServer(grpcSrv, srv)
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

	return &Client{
		client:  pb.NewMLDetectorClient(conn),
		conn:    conn,
		timeout: time.Second,
		enabled: true,
	}
}

// ─── Health ───────────────────────────────────────────────────────────────────

func TestClient_Health_Ready(t *testing.T) {
	c := newBufconnClient(t, &mockMLServer{ready: true})
	if err := c.Health(context.Background()); err != nil {
		t.Errorf("ready server should return nil error, got: %v", err)
	}
}

func TestClient_Health_NotReady(t *testing.T) {
	c := newBufconnClient(t, &mockMLServer{ready: false})
	if err := c.Health(context.Background()); err == nil {
		t.Error("not-ready server should return error")
	}
}

// ─── DetectMultivariate ───────────────────────────────────────────────────────

func TestClient_DetectMultivariate_Anomaly(t *testing.T) {
	c := newBufconnClient(t, &mockMLServer{isAnomaly: true, anomalyScore: 0.9})
	// Need ≥2 samples (checked in DetectMultivariate)
	anomaly, err := c.DetectMultivariate(context.Background(), map[string]float64{
		"cpu_ratio":    0.9,
		"memory_ratio": 0.85,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if anomaly == nil {
		t.Fatal("expected anomaly result, got nil")
	}
	if anomaly.Severity != "critical" {
		t.Errorf("score 0.9 should be critical, got %q", anomaly.Severity)
	}
}

func TestClient_DetectMultivariate_NoAnomaly(t *testing.T) {
	c := newBufconnClient(t, &mockMLServer{isAnomaly: false, anomalyScore: 0.3})
	anomaly, err := c.DetectMultivariate(context.Background(), map[string]float64{
		"cpu_ratio":    0.3,
		"memory_ratio": 0.2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if anomaly != nil {
		t.Error("non-anomaly should return nil")
	}
}

func TestClient_DetectMultivariate_TooFewSamples(t *testing.T) {
	c := newBufconnClient(t, &mockMLServer{isAnomaly: true})
	// Only 1 sample — below minimum of 2
	anomaly, err := c.DetectMultivariate(context.Background(), map[string]float64{"cpu": 0.9})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if anomaly != nil {
		t.Error("< 2 samples should return nil (no-op)")
	}
}

// ─── Forecast ─────────────────────────────────────────────────────────────────

func TestClient_Forecast_WillBreach(t *testing.T) {
	c := newBufconnClient(t, &mockMLServer{willBreach: true})
	// Forecast requires ≥10 values
	values := make([]float64, 10)
	timestamps := make([]int64, 10)
	for i := range values {
		values[i] = 0.5 + float64(i)*0.01
		timestamps[i] = int64(i * 30)
	}
	anomaly, err := c.Forecast(context.Background(), "cpu_rate", values, timestamps, 0.9)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if anomaly == nil {
		t.Error("breach forecast should return non-nil anomaly")
	}
}

func TestClient_Forecast_NoBreach(t *testing.T) {
	c := newBufconnClient(t, &mockMLServer{willBreach: false})
	values := make([]float64, 10)
	timestamps := make([]int64, 10)
	anomaly, err := c.Forecast(context.Background(), "cpu_rate", values, timestamps, 0.9)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if anomaly != nil {
		t.Error("no-breach forecast should return nil anomaly")
	}
}

func TestClient_Forecast_TooFewValues(t *testing.T) {
	c := newBufconnClient(t, &mockMLServer{willBreach: true})
	// < 10 values → early return nil, nil
	anomaly, err := c.Forecast(context.Background(), "cpu", []float64{0.5}, []int64{1}, 0.9)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if anomaly != nil {
		t.Error("< 10 values should return nil (no-op)")
	}
}

// ─── DetectFromFeatures ───────────────────────────────────────────────────────

func TestClient_DetectFromFeatures_Anomaly(t *testing.T) {
	c := newBufconnClient(t, &mockMLServer{isAnomaly: true, anomalyScore: 0.85})
	result, err := c.DetectFromFeatures(context.Background(), map[string]float64{
		"cpu_ratio":    0.9,
		"memory_ratio": 0.8,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected detection result, got nil")
	}
	if !result.IsAnomaly {
		t.Error("expected IsAnomaly=true")
	}
}

func TestClient_DetectFromFeatures_NoAnomaly(t *testing.T) {
	c := newBufconnClient(t, &mockMLServer{isAnomaly: false, anomalyScore: 0.2})
	result, err := c.DetectFromFeatures(context.Background(), map[string]float64{
		"cpu_ratio":    0.3,
		"memory_ratio": 0.2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("DetectFromFeatures always returns a result")
	}
	if result.IsAnomaly {
		t.Error("expected IsAnomaly=false")
	}
}

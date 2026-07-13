package readiness

import (
	"context"
	"testing"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/ml"
)

func TestMLChecker_NilClient_ReturnsNil(t *testing.T) {
	checker := MLChecker(nil)
	if err := checker(context.Background()); err != nil {
		t.Errorf("nil client should return nil error, got: %v", err)
	}
}

func TestMLChecker_DisabledClient_ReturnsNil(t *testing.T) {
	// Create a disabled ML client — returns immediately without dialing gRPC
	client, err := ml.New(config.ML{Enabled: false})
	if err != nil {
		t.Fatalf("could not create disabled ML client: %v", err)
	}
	defer client.Close()

	checker := MLChecker(client)
	if checkErr := checker(context.Background()); checkErr != nil {
		t.Errorf("disabled client should return nil error, got: %v", checkErr)
	}
}

func TestMLChecker_EnabledClient_ProbesHealth(t *testing.T) {
	// Enabled client with an unreachable endpoint: grpc.Dial is lazy, so New
	// succeeds, but the Health RPC fails — exercising the enabled branch
	// (client.Health + recordResult) and error propagation. Timeout is zero on
	// purpose: context.WithTimeout(ctx, 0) expires immediately, so gRPC returns
	// DeadlineExceeded without blocking on the network (deterministic, no hang).
	client, err := ml.New(config.ML{Enabled: true, Endpoint: "localhost:50999", Timeout: 0})
	if err != nil {
		t.Fatalf("could not create enabled ML client: %v", err)
	}
	defer client.Close()

	checker := MLChecker(client)
	if checkErr := checker(context.Background()); checkErr == nil {
		t.Error("enabled client with unreachable ML service should return an error")
	}
}

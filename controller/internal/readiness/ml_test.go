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

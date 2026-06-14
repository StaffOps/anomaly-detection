package leader

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRun_ValidationErrors covers the input validation paths in Run(). The
// happy path requires a real K8s API server (out of scope for unit tests);
// integration coverage lives in the cluster deploy validation (P5.2).
func TestRun_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name: "missing namespace",
			cfg: Config{
				Name:          "test-lease",
				LeaseDuration: 15 * time.Second,
				RenewDeadline: 10 * time.Second,
				RetryPeriod:   2 * time.Second,
			},
			wantErr: "Namespace and Name are required",
		},
		{
			name: "missing name",
			cfg: Config{
				Namespace:     "monitoring",
				LeaseDuration: 15 * time.Second,
				RenewDeadline: 10 * time.Second,
				RetryPeriod:   2 * time.Second,
			},
			wantErr: "Namespace and Name are required",
		},
		{
			name: "lease duration not greater than renew deadline",
			cfg: Config{
				Namespace:     "monitoring",
				Name:          "test-lease",
				LeaseDuration: 10 * time.Second,
				RenewDeadline: 10 * time.Second,
				RetryPeriod:   2 * time.Second,
			},
			wantErr: "LeaseDuration",
		},
		{
			name: "lease duration smaller than renew deadline",
			cfg: Config{
				Namespace:     "monitoring",
				Name:          "test-lease",
				LeaseDuration: 5 * time.Second,
				RenewDeadline: 10 * time.Second,
				RetryPeriod:   2 * time.Second,
			},
			wantErr: "LeaseDuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			err := Run(ctx, tt.cfg, Callbacks{})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestDefaultIdentity_FromPodName(t *testing.T) {
	const want = "staffops-ad-controller-0"
	t.Setenv("POD_NAME", want)
	got := defaultIdentity()
	if got != want {
		t.Errorf("defaultIdentity() = %q, want %q", got, want)
	}
}

func TestDefaultIdentity_FallbackToHostname(t *testing.T) {
	// Ensure POD_NAME is unset so hostname path executes.
	t.Setenv("POD_NAME", "")
	os.Unsetenv("POD_NAME")
	got := defaultIdentity()
	if got == "" || got == "unknown" {
		t.Errorf("defaultIdentity() = %q, expected real hostname", got)
	}
	hn, err := os.Hostname()
	if err == nil && got != hn {
		t.Errorf("defaultIdentity() = %q, want hostname %q", got, hn)
	}
}

// TestRun_NoKubeconfig verifies the error path when neither in-cluster config
// nor an explicit kubeconfig path is available — the function should fail
// fast rather than hanging on a broken client.
func TestRun_NoKubeconfig(t *testing.T) {
	// Force in-cluster config attempt to fail by ensuring the env vars
	// expected by rest.InClusterConfig are unset.
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	os.Unsetenv("KUBERNETES_SERVICE_HOST")
	os.Unsetenv("KUBERNETES_SERVICE_PORT")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	cfg := Config{
		Namespace:     "monitoring",
		Name:          "test-lease",
		Identity:      "test-pod",
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
		// Empty Kubeconfig and no in-cluster env → should fail in buildClient.
	}

	err := Run(ctx, cfg, Callbacks{})
	if err == nil {
		t.Fatal("expected error from missing kubeconfig, got nil")
	}
	if !strings.Contains(err.Error(), "build k8s client") {
		t.Errorf("expected k8s client error, got: %v", err)
	}
}

// TestRun_InvalidKubeconfigPath verifies a clean error when the kubeconfig
// path is provided but unreadable.
func TestRun_InvalidKubeconfigPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	cfg := Config{
		Namespace:     "monitoring",
		Name:          "test-lease",
		Identity:      "test-pod",
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
		Kubeconfig:    "/nonexistent/path/kubeconfig.yaml",
	}

	err := Run(ctx, cfg, Callbacks{})
	if err == nil {
		t.Fatal("expected error from invalid kubeconfig path, got nil")
	}
	if !strings.Contains(err.Error(), "build k8s client") {
		t.Errorf("expected k8s client error, got: %v", err)
	}
}

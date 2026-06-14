// Package leader implements K8s Lease-based leader election for the controller.
//
// Single-leader semantics are required because the controller maintains:
//   - Correlation state in memory (workload-grouping window, dedup hits)
//   - In-flight ML calls and enrichment fan-out
//   - The single source of truth for "what's currently being alerted"
//
// Running multiple active controllers without coordination would produce
// duplicate alerts (same anomaly correlated twice) and split correlation
// state (each controller sees only some workers' anomalies).
//
// This package wraps client-go's leaderelection helper:
//   - LeaseLock holds the Lease in the configured namespace
//   - Leader runs callbacks on lease acquisition/loss
//   - Followers stay warm and take over within ~LeaseDuration on leader death
//
// In docker-compose / single-replica environments, leader election can be
// disabled via config — the caller should treat "not enabled" as "always leader".
package leader

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
)

// Callbacks are invoked on leader transitions.
//
// OnStartedLeading runs as a goroutine when this instance acquires the lease.
// The provided context is cancelled when leadership is lost — the callback
// MUST stop all leadership-gated work when the context is done.
//
// OnStoppedLeading runs synchronously when this instance loses the lease.
// It should be quick (deregister, log) — the process can either exit or
// continue trying to re-acquire.
//
// OnNewLeader is called whenever a different identity becomes leader.
// Useful for logging "who's the boss" without taking action.
type Callbacks struct {
	OnStartedLeading func(ctx context.Context)
	OnStoppedLeading func()
	OnNewLeader      func(identity string)
}

// Config holds runtime parameters for leader election.
type Config struct {
	// Namespace where the Lease object lives.
	Namespace string
	// Name of the Lease object (must be unique per controller deployment).
	Name string
	// Identity uniquely identifies this replica. Defaults to POD_NAME env var
	// (set via downward API in K8s) or hostname. Two replicas with the same
	// Identity will fight to renew the same lease — set distinct values.
	Identity string
	// LeaseDuration is how long a non-leader waits before attempting to take
	// over a stale lease. Recommended 15s.
	LeaseDuration time.Duration
	// RenewDeadline is the duration the leader will retry refreshing
	// leadership before giving up. Must be < LeaseDuration. Recommended 10s.
	RenewDeadline time.Duration
	// RetryPeriod is the duration leader candidates wait between actions.
	// Recommended 2s.
	RetryPeriod time.Duration
	// Kubeconfig path; empty = use in-cluster config (when running in K8s).
	Kubeconfig string
}

// Run blocks executing the leader-election loop until ctx is cancelled.
//
// Behavior:
//   - Builds a K8s client (in-cluster if Kubeconfig is empty).
//   - Creates a LeaseLock for {Namespace, Name, Identity}.
//   - Calls leaderelection.RunOrDie which loops forever:
//       - Tries to acquire the lease
//       - On success: invokes OnStartedLeading(leaderCtx); leaderCtx is cancelled when lease lost
//       - On loss: invokes OnStoppedLeading()
//
// Returns when ctx is cancelled or on fatal client setup errors.
//
// Side effects: increments metrics.LeaderTransitions on every transition
// and updates metrics.IsLeader (1 when this instance leads, 0 otherwise).
func Run(ctx context.Context, cfg Config, cb Callbacks) error {
	if cfg.Namespace == "" || cfg.Name == "" {
		return errors.New("leader: Namespace and Name are required")
	}
	if cfg.Identity == "" {
		cfg.Identity = defaultIdentity()
	}
	if cfg.LeaseDuration <= cfg.RenewDeadline {
		return fmt.Errorf("leader: LeaseDuration (%s) must be > RenewDeadline (%s)",
			cfg.LeaseDuration, cfg.RenewDeadline)
	}

	client, err := buildClient(cfg.Kubeconfig)
	if err != nil {
		return fmt.Errorf("leader: build k8s client: %w", err)
	}

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
		},
		Client: client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: cfg.Identity,
		},
	}

	slog.Info("leader: starting leader election",
		"namespace", cfg.Namespace,
		"lease", cfg.Name,
		"identity", cfg.Identity,
		"lease_duration", cfg.LeaseDuration,
		"renew_deadline", cfg.RenewDeadline,
		"retry_period", cfg.RetryPeriod,
	)

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   cfg.LeaseDuration,
		RenewDeadline:   cfg.RenewDeadline,
		RetryPeriod:     cfg.RetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leaderCtx context.Context) {
				metrics.IsLeader.Set(1)
				metrics.LeaderTransitions.Inc()
				slog.Info("leader: acquired leadership", "identity", cfg.Identity)
				if cb.OnStartedLeading != nil {
					cb.OnStartedLeading(leaderCtx)
				}
			},
			OnStoppedLeading: func() {
				metrics.IsLeader.Set(0)
				slog.Warn("leader: lost leadership", "identity", cfg.Identity)
				if cb.OnStoppedLeading != nil {
					cb.OnStoppedLeading()
				}
			},
			OnNewLeader: func(identity string) {
				if identity == cfg.Identity {
					return // we logged on OnStartedLeading
				}
				slog.Info("leader: new leader observed", "leader", identity, "self", cfg.Identity)
				if cb.OnNewLeader != nil {
					cb.OnNewLeader(identity)
				}
			},
		},
	})
	return nil
}

// defaultIdentity returns POD_NAME (set via downward API in K8s) or the
// hostname as a fallback. Must be unique per replica — two pods with the same
// identity will compete to renew the same lease and produce thrashing.
func defaultIdentity() string {
	if name := os.Getenv("POD_NAME"); name != "" {
		return name
	}
	if hn, err := os.Hostname(); err == nil {
		return hn
	}
	return "unknown"
}

// buildClient returns a kubernetes.Interface using either an explicit
// kubeconfig file or the in-cluster service account.
func buildClient(kubeconfig string) (kubernetes.Interface, error) {
	var (
		restCfg *rest.Config
		err     error
	)
	if kubeconfig != "" {
		restCfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		restCfg, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(restCfg)
}

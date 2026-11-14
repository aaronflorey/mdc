package runner

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronflorey/mdc/internal/discover"
)

func targets(names ...string) []discover.Target {
	out := make([]discover.Target, len(names))
	for i, n := range names {
		out[i] = discover.Target{Label: n, ProjectName: n}
	}
	return out
}

func TestRunCollectsOutcomesInOrder(t *testing.T) {
	boom := errors.New("boom")
	outcomes := Run(context.Background(), targets("a", "b", "c"), 0, func(_ context.Context, tg discover.Target) error {
		switch tg.Label {
		case "b":
			return Skip("nothing to do")
		case "c":
			return boom
		}
		return nil
	})

	if outcomes[0].Status != OK || outcomes[0].Target.Label != "a" {
		t.Fatalf("a: %+v", outcomes[0])
	}
	if outcomes[1].Status != Skipped || outcomes[1].Reason != "nothing to do" || outcomes[1].Err != nil {
		t.Fatalf("b: %+v", outcomes[1])
	}
	if outcomes[2].Status != Failed || !errors.Is(outcomes[2].Err, boom) {
		t.Fatalf("c: %+v", outcomes[2])
	}
	if Err(outcomes) == nil {
		t.Fatal("expected error when a target failed")
	}
	if Err(outcomes[:2]) != nil {
		t.Fatal("expected no error without failures")
	}
}

func TestRunBoundsConcurrency(t *testing.T) {
	var running, peak atomic.Int32
	Run(context.Background(), targets("a", "b", "c", "d", "e"), 2, func(context.Context, discover.Target) error {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		return nil
	})
	if got := peak.Load(); got != 2 {
		t.Fatalf("expected peak concurrency 2, got %d", got)
	}
}

func TestRunFailsUnstartedTargetsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	outcomes := Run(ctx, targets("a", "b"), 1, func(ctx context.Context, tg discover.Target) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	})
	for _, o := range outcomes {
		if o.Status != Failed || !errors.Is(o.Err, context.Canceled) {
			t.Fatalf("expected cancelled failure, got %+v", o)
		}
	}
}

package reconcile

import (
	"context"
	"errors"
	"github.com/korallis/letmecook/internal/execwire"
	"github.com/korallis/letmecook/internal/store"
	"testing"
)

func TestStubIsNilSafeAndOtherwiseRefuses(t *testing.T) {
	ctx := context.Background()
	for _, deps := range []Deps{{}, {Store: &store.Store{}}} {
		want := ErrNotImplemented
		if deps.Store == nil {
			want = nil
		}
		_, err := Startup(ctx, deps)
		if !errors.Is(err, want) {
			t.Fatal(err)
		}
		_, err = OnHello(ctx, deps, "", execwire.Hello{})
		if !errors.Is(err, want) {
			t.Fatal(err)
		}
		_, err = Sweep(ctx, deps)
		if !errors.Is(err, want) {
			t.Fatal(err)
		}
		_, err = PlanRetry(ctx, deps, "")
		if !errors.Is(err, want) {
			t.Fatal(err)
		}
	}
}

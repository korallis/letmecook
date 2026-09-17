package httpapi

import (
	"context"
	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/store"
)

type releaseCommand struct {
	commandHeader
	Proof store.Reconciliation `json:"proof"`
}

func init() {
	Register("reconcile", func(d Deps) []Route {
		return []Route{
			readRoute("/api/v1/reconcile", nil, func(ctx context.Context, a Actor, r Request) (any, error) {
				if d.Reconcile == nil {
					return nil, g.Deny("store_unavailable", "reconcile")
				}
				return d.Reconcile.Read(ctx)
			}),
			ownerRoute("POST", "/api/v1/reconcile/{id}/release", ownerMutation(d, "reconcile.release", func(ctx context.Context, a Actor, r Request, in releaseCommand) (any, int, error) {
				view, err := d.Store.AttemptView(ctx, r.Path["id"])
				if err != nil {
					return nil, 0, err
				}
				if in.Proof.DispatchID != view.DispatchID {
					return nil, 0, g.Deny("identity_conflict", "dispatch_id")
				}
				err = d.Store.ReconcileDispatch(ctx, a.Fingerprint, in.Proof)
				return map[string]any{"attempt_id": r.Path["id"], "released": err == nil}, 200, err
			})),
		}
	})
}

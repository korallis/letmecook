package isolation

import (
	"context"
	"errors"
	"testing"
)

func TestUnqualifiedRefuses(t *testing.T) {
	var profile Profile = Unqualified{}
	launcher, err := profile.Prepare(context.Background(), Workspace{})
	if launcher != nil || !errors.Is(err, ErrExecutionUnqualified) || profile.Qualification() != QualificationUnqualified || len(profile.Controls()) != 0 {
		t.Fatal("unqualified profile granted capability")
	}
}

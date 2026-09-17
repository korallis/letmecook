package authority

import "testing"

func TestAttemptAllowanceRetainsRemaindersAndReservations(t *testing.T) {
	cost := int64(13)
	ceiling := Budgets{Requests: 13, Subattempts: 13, Attempts: 3, Retries: 2, ProviderOutputTokens: 13, ProviderCostMicros: &cost, AttemptMS: 120000, TotalMS: 360000, FirstOutputMS: 1000, IdleMS: 1000}
	usedCost := int64(0)
	used := Budgets{ProviderCostMicros: &usedCost}
	for n, want := range []int64{4, 4, 5} {
		got, err := AttemptAllowance(ceiling, used, int64(n), 120000)
		if err != nil || got.Requests != want || got.Subattempts != want || got.ProviderOutputTokens != want || *got.ProviderCostMicros != want || got.Attempts != 1 || got.Retries != 0 || got.TotalMS != 120000 {
			t.Fatalf("attempt %d: %+v %v", n, got, err)
		}
		used.Requests += got.Requests
		used.Subattempts += got.Subattempts
		used.ProviderOutputTokens += got.ProviderOutputTokens
		usedCost += *got.ProviderCostMicros
	}
	if cost != 13 {
		t.Fatal("mutated ceiling")
	}
	if _, err := AttemptAllowance(ceiling, used, 3, 120000); err == nil {
		t.Fatal("ignored attempt ceiling")
	}
	ceiling.Requests = 2
	if _, err := AttemptAllowance(ceiling, Budgets{}, 0, 120000); err == nil {
		t.Fatal("zero allowance admitted")
	}
}

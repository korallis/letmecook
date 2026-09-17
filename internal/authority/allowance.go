package authority

// AttemptAllowance divides still-unreserved additive budgets across the remaining
// permitted attempts. Charges are reservations, never measured usage or refunds.
// Per-request byte bounds and timing bounds are not divided.
func AttemptAllowance(ceiling, reserved Budgets, dispatched, attemptMS int64) (Budgets, error) {
	remaining := min(ceiling.Attempts-dispatched, ceiling.Retries-dispatched+1)
	if remaining <= 0 {
		return Budgets{}, Deny("attempt_ceiling", "task")
	}
	b := ceiling
	if reserved.Requests > b.Requests || reserved.Subattempts > b.Subattempts || reserved.ProviderOutputTokens > b.ProviderOutputTokens {
		return Budgets{}, Deny("budget_exhausted", "task")
	}
	b.Requests = (b.Requests - reserved.Requests) / remaining
	b.Subattempts = (b.Subattempts - reserved.Subattempts) / remaining
	b.ProviderOutputTokens = (b.ProviderOutputTokens - reserved.ProviderOutputTokens) / remaining
	if b.ProviderCostMicros != nil {
		used := int64(0)
		if reserved.ProviderCostMicros != nil {
			used = *reserved.ProviderCostMicros
		}
		if used > *b.ProviderCostMicros {
			return Budgets{}, Deny("budget_exhausted", "task")
		}
		cost := (*b.ProviderCostMicros - used) / remaining
		b.ProviderCostMicros = &cost
	}
	if b.Requests == 0 || b.Subattempts == 0 || ceiling.ProviderOutputTokens > 0 && b.ProviderOutputTokens == 0 {
		return Budgets{}, Deny("budget_exhausted", "task")
	}
	b.Attempts, b.Retries, b.Concurrency = 1, 0, 1
	b.AttemptMS, b.TotalMS = attemptMS, attemptMS
	b.FirstOutputMS, b.IdleMS = min(b.FirstOutputMS, attemptMS), min(b.IdleMS, attemptMS)
	return b, nil
}

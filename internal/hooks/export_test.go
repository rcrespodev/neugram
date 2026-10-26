package hooks

import "time"

// SetBudget swaps the hook budget and returns the restore func.
func SetBudget(d time.Duration) func() {
	prev := hookBudget
	hookBudget = d
	return func() { hookBudget = prev }
}

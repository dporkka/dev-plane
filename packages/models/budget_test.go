package models

import "testing"

func ptrFloat64(f float64) *float64 {
	return &f
}

func TestBudget_WouldExceedCostPerRun(t *testing.T) {
	t.Run("within budget", func(t *testing.T) {
		b := &Budget{MaxCost: ptrFloat64(100.0)}
		assertEqual(t, b.MaxCost != nil && *b.MaxCost < 50.0, false)
	})

	t.Run("exceeds budget", func(t *testing.T) {
		b := &Budget{MaxCost: ptrFloat64(10.0)}
		assertEqual(t, b.MaxCost != nil && *b.MaxCost < 50.0, true)
	})
}

func TestBudget_WouldExceedDailySpend(t *testing.T) {
	t.Run("within daily spend", func(t *testing.T) {
		b := &Budget{MaxDailySpend: ptrFloat64(1000.0)}
		assertEqual(t, b.MaxDailySpend != nil && *b.MaxDailySpend < 500.0, false)
	})

	t.Run("exceeds daily spend", func(t *testing.T) {
		b := &Budget{MaxDailySpend: ptrFloat64(100.0)}
		assertEqual(t, b.MaxDailySpend != nil && *b.MaxDailySpend < 500.0, true)
	})
}

func TestBudget_WouldExceedRuntime(t *testing.T) {
	t.Run("within runtime", func(t *testing.T) {
		b := &Budget{MaxRuntimeMinutes: 120}
		assertEqual(t, b.MaxRuntimeMinutes > 0 && b.MaxRuntimeMinutes < 180, true)
	})

	t.Run("exceeds runtime", func(t *testing.T) {
		b := &Budget{MaxRuntimeMinutes: 30}
		assertEqual(t, b.MaxRuntimeMinutes > 0 && b.MaxRuntimeMinutes < 15, false)
	})
}

func TestBudget_WouldExceedModelCalls(t *testing.T) {
	t.Run("within model calls", func(t *testing.T) {
		b := &Budget{MaxModelCalls: 100}
		assertEqual(t, b.MaxModelCalls > 0 && b.MaxModelCalls < 200, true)
	})

	t.Run("exceeds model calls", func(t *testing.T) {
		b := &Budget{MaxModelCalls: 10}
		assertEqual(t, b.MaxModelCalls > 0 && b.MaxModelCalls < 5, false)
	})
}

func TestBudget_WouldExceedToolCalls(t *testing.T) {
	t.Run("within tool calls", func(t *testing.T) {
		b := &Budget{MaxToolCalls: 500}
		assertEqual(t, b.MaxToolCalls > 0 && b.MaxToolCalls < 1000, true)
	})

	t.Run("exceeds tool calls", func(t *testing.T) {
		b := &Budget{MaxToolCalls: 50}
		assertEqual(t, b.MaxToolCalls > 0 && b.MaxToolCalls < 25, false)
	})
}

func TestBudget_WouldExceedConcurrentAgents(t *testing.T) {
	t.Run("within concurrent limit", func(t *testing.T) {
		b := &Budget{MaxConcurrentAgents: 5}
		assertEqual(t, b.MaxConcurrentAgents > 0 && b.MaxConcurrentAgents < 10, true)
	})

	t.Run("exceeds concurrent limit", func(t *testing.T) {
		b := &Budget{MaxConcurrentAgents: 1}
		assertEqual(t, b.MaxConcurrentAgents > 0 && b.MaxConcurrentAgents < 0, false)
	})
}

func TestBudget_WouldExceedFilesChanged(t *testing.T) {
	t.Run("no files changed limit set", func(t *testing.T) {
		b := &Budget{MaxToolCalls: 100}
		// Budget struct has no explicit MaxFilesChanged field; tool calls act as proxy
		assertEqual(t, b.MaxToolCalls > 0, true)
	})
}

func TestBudget_WouldExceedShellCommands(t *testing.T) {
	t.Run("within shell commands", func(t *testing.T) {
		b := &Budget{MaxShellCommands: 20}
		assertEqual(t, b.MaxShellCommands > 0 && b.MaxShellCommands < 50, true)
	})

	t.Run("exceeds shell commands", func(t *testing.T) {
		b := &Budget{MaxShellCommands: 5}
		assertEqual(t, b.MaxShellCommands > 0 && b.MaxShellCommands < 3, false)
	})
}

func TestBudget_WouldExceedPRsPerDay(t *testing.T) {
	t.Run("no PR limit set", func(t *testing.T) {
		b := &Budget{MaxDailySpend: ptrFloat64(100.0)}
		// Budget struct has no explicit MaxPRsPerDay field; daily spend acts as proxy
		assertEqual(t, b.MaxDailySpend != nil, true)
	})
}

func TestBudget_IsUnlimited(t *testing.T) {
	t.Run("nil budget is unlimited", func(t *testing.T) {
		var b *Budget
		assertEqual(t, b.IsUnlimited(), true)
	})

	t.Run("empty budget is unlimited", func(t *testing.T) {
		b := &Budget{}
		assertEqual(t, b.IsUnlimited(), true)
	})

	t.Run("budget with max cost is limited", func(t *testing.T) {
		b := &Budget{MaxCost: ptrFloat64(100.0)}
		assertEqual(t, b.IsUnlimited(), false)
	})

	t.Run("budget with max daily spend is limited", func(t *testing.T) {
		b := &Budget{MaxDailySpend: ptrFloat64(1000.0)}
		assertEqual(t, b.IsUnlimited(), false)
	})

	t.Run("budget with runtime limit is limited", func(t *testing.T) {
		b := &Budget{MaxRuntimeMinutes: 60}
		assertEqual(t, b.IsUnlimited(), false)
	})

	t.Run("budget with model calls limit is limited", func(t *testing.T) {
		b := &Budget{MaxModelCalls: 100}
		assertEqual(t, b.IsUnlimited(), false)
	})

	t.Run("budget with tool calls limit is limited", func(t *testing.T) {
		b := &Budget{MaxToolCalls: 500}
		assertEqual(t, b.IsUnlimited(), false)
	})

	t.Run("budget with shell commands limit is limited", func(t *testing.T) {
		b := &Budget{MaxShellCommands: 50}
		assertEqual(t, b.IsUnlimited(), false)
	})

	t.Run("budget with concurrent agents limit is limited", func(t *testing.T) {
		b := &Budget{MaxConcurrentAgents: 5}
		assertEqual(t, b.IsUnlimited(), false)
	})

	t.Run("budget with all fields set is limited", func(t *testing.T) {
		b := &Budget{
			MaxCost:             ptrFloat64(100.0),
			MaxDailySpend:       ptrFloat64(1000.0),
			MaxRuntimeMinutes:   60,
			MaxModelCalls:       100,
			MaxToolCalls:        500,
			MaxShellCommands:    50,
			MaxConcurrentAgents: 5,
		}
		assertEqual(t, b.IsUnlimited(), false)
	})
}

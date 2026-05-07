package domain

import (
	"math"
	"time"
)

type TrustBattery struct {
	LastActiveAt         *time.Time `json:"last_active_at"`
	Tier                 TrustTier  `json:"tier"`
	TrustScore           int        `json:"trust_score"`
	ConsecutiveSuccesses int        `json:"consecutive_successes"`
	ConsecutiveErrors    int        `json:"consecutive_errors"`
	DaysInCurrentTier    int        `json:"days_in_current_tier"`
}

func NewTrustBattery() *TrustBattery {
	now := time.Now().UTC()
	return &TrustBattery{
		Tier:                 TrustTierProbation,
		TrustScore:           0,
		ConsecutiveSuccesses: 0,
		ConsecutiveErrors:    0,
		DaysInCurrentTier:    0,
		LastActiveAt:         &now,
	}
}

func (tb *TrustBattery) RecordSuccess() {
	tb.ConsecutiveSuccesses++
	tb.ConsecutiveErrors = 0
	now := time.Now().UTC()
	tb.LastActiveAt = &now

	if tb.Tier == TrustTierProbation && tb.ConsecutiveSuccesses >= 3 {
		tb.tryUpgrade()
	}
}

func (tb *TrustBattery) RecordError() {
	tb.ConsecutiveErrors++
	tb.ConsecutiveSuccesses = 0

	if tb.ConsecutiveErrors >= 3 {
		tb.downgrade()
	}
}

func (tb *TrustBattery) FlagFraud() {
	tb.Tier = TrustTierProbation
	tb.TrustScore = 0
	tb.ConsecutiveSuccesses = 0
	tb.ConsecutiveErrors = 0
	tb.DaysInCurrentTier = 0
}

func (tb *TrustBattery) AdvanceDays(days int) {
	tb.DaysInCurrentTier += days
	now := time.Now().UTC()
	tb.LastActiveAt = &now

	if tb.Tier == TrustTierProbation && tb.DaysInCurrentTier >= 30 && tb.ConsecutiveSuccesses >= 3 {
		tb.tryUpgrade()
	}

	if tb.Tier != TrustTierProbation && (tb.DaysInCurrentTier-days) >= 90 {
		tb.downgrade()
	}
}

func (tb *TrustBattery) tryUpgrade() bool {
	upgradeRequirements := map[TrustTier]struct {
		DaysRequired int
		TxRequired   int
	}{
		TrustTierProbation: {30, 3},
		TrustTierStandard:  {90, 10},
		TrustTierCore:      {180, 0},
		TrustTierStrategic: {math.MaxInt, math.MaxInt},
	}

	req := upgradeRequirements[tb.Tier]

	if tb.DaysInCurrentTier >= req.DaysRequired {
		currentIdx := tb.tierIndex()
		tiers := []TrustTier{TrustTierProbation, TrustTierStandard, TrustTierCore, TrustTierStrategic}
		if currentIdx < len(tiers)-1 {
			tb.Tier = tiers[currentIdx+1]
			tb.DaysInCurrentTier = 0
			tb.ConsecutiveSuccesses = 0
			return true
		}
	}

	return false
}

func (tb *TrustBattery) downgrade() {
	if tb.Tier == TrustTierProbation {
		return
	}

	currentIdx := tb.tierIndex()
	tiers := []TrustTier{TrustTierProbation, TrustTierStandard, TrustTierCore, TrustTierStrategic}
	if currentIdx > 0 {
		tb.Tier = tiers[currentIdx-1]
		tb.ConsecutiveErrors = 0
		tb.DaysInCurrentTier = 0
	}
}

func (tb *TrustBattery) ShouldDowngrade() bool {
	if tb.Tier == TrustTierProbation {
		return false
	}

	if tb.ConsecutiveErrors >= 3 {
		return true
	}

	if tb.LastActiveAt != nil {
		daysInactive := int(time.Since(*tb.LastActiveAt).Hours() / 24)
		if daysInactive >= 90 {
			return true
		}
	}

	return false
}

func (tb *TrustBattery) tierIndex() int {
	tiers := []TrustTier{TrustTierProbation, TrustTierStandard, TrustTierCore, TrustTierStrategic}
	for i, t := range tiers {
		if t == tb.Tier {
			return i
		}
	}
	return 0
}

func (tb *TrustBattery) ToMap() map[string]interface{} {
	result := map[string]interface{}{
		"tier":                  string(tb.Tier),
		"trust_score":           tb.TrustScore,
		"consecutive_successes": tb.ConsecutiveSuccesses,
		"consecutive_errors":    tb.ConsecutiveErrors,
		"days_in_current_tier":  tb.DaysInCurrentTier,
	}

	if tb.LastActiveAt != nil {
		result["last_active_at"] = tb.LastActiveAt.Format(time.RFC3339)
	} else {
		result["last_active_at"] = nil
	}

	return result
}

func TrustBatteryFromMap(data map[string]interface{}) *TrustBattery {
	tb := &TrustBattery{}

	if tier, ok := data["tier"].(string); ok {
		tb.Tier = TrustTier(tier)
	} else {
		tb.Tier = TrustTierProbation
	}

	if v, ok := data["trust_score"].(float64); ok {
		tb.TrustScore = int(v)
	}
	if v, ok := data["consecutive_successes"].(float64); ok {
		tb.ConsecutiveSuccesses = int(v)
	}
	if v, ok := data["consecutive_errors"].(float64); ok {
		tb.ConsecutiveErrors = int(v)
	}
	if v, ok := data["days_in_current_tier"].(float64); ok {
		tb.DaysInCurrentTier = int(v)
	}

	if lastActive, ok := data["last_active_at"].(string); ok && lastActive != "" {
		if t, err := time.Parse(time.RFC3339, lastActive); err == nil {
			tb.LastActiveAt = &t
		}
	}

	return tb
}

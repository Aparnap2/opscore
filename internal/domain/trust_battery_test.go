package domain

import (
	"testing"
	"time"
)

func TestTrustBattery_RecordSuccess(t *testing.T) {
	tests := []struct {
		name        string
		initialTB  *TrustBattery
		wantTier   TrustTier
		wantSuccesses int
		wantErrors int
	}{
		{
			name: "first success",
			initialTB: &TrustBattery{
				Tier:       TrustTierProbation,
				ConsecutiveSuccesses: 0,
				ConsecutiveErrors:   0,
			},
			wantTier:       TrustTierProbation,
			wantSuccesses: 1,
			wantErrors:    0,
		},
		{
			name: "second success",
			initialTB: &TrustBattery{
				Tier:       TrustTierProbation,
				ConsecutiveSuccesses: 1,
				ConsecutiveErrors:   0,
			},
			wantTier:       TrustTierProbation,
			wantSuccesses: 2,
			wantErrors:    0,
		},
		{
			name: "third success triggers upgrade",
			initialTB: &TrustBattery{
				Tier:              TrustTierProbation,
				ConsecutiveSuccesses: 2,
				ConsecutiveErrors:    0,
				DaysInCurrentTier:  30,
			},
			wantTier:       TrustTierStandard,
			wantSuccesses: 0,
			wantErrors:    0,
		},
		{
			name: "STANDARD tier success",
			initialTB: &TrustBattery{
				Tier:       TrustTierStandard,
				ConsecutiveSuccesses: 0,
				ConsecutiveErrors:   0,
			},
			wantTier:       TrustTierStandard,
			wantSuccesses: 1,
			wantErrors:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tb := tt.initialTB
			tb.RecordSuccess()
			if tb.Tier != tt.wantTier {
				t.Errorf("RecordSuccess().Tier = %v, want %v", tb.Tier, tt.wantTier)
			}
			if tb.ConsecutiveSuccesses != tt.wantSuccesses {
				t.Errorf("RecordSuccess().ConsecutiveSuccesses = %v, want %v", tb.ConsecutiveSuccesses, tt.wantSuccesses)
			}
			if tb.ConsecutiveErrors != tt.wantErrors {
				t.Errorf("RecordSuccess().ConsecutiveErrors = %v, want %v", tb.ConsecutiveErrors, tt.wantErrors)
			}
		})
	}
}

func TestTrustBattery_RecordError(t *testing.T) {
	tests := []struct {
		name        string
		initialTB   *TrustBattery
		wantTier    TrustTier
		wantErrors int
	}{
		{
			name: "first error",
			initialTB: &TrustBattery{
				Tier:              TrustTierStandard,
				ConsecutiveSuccesses: 5,
				ConsecutiveErrors:    0,
			},
			wantTier:    TrustTierStandard,
			wantErrors: 1,
		},
		{
			name: "second error",
			initialTB: &TrustBattery{
				Tier:              TrustTierStandard,
				ConsecutiveSuccesses: 5,
				ConsecutiveErrors:    1,
			},
			wantTier:    TrustTierStandard,
			wantErrors: 2,
		},
		{
			name: "third error triggers downgrade",
			initialTB: &TrustBattery{
				Tier:              TrustTierStandard,
				ConsecutiveSuccesses: 5,
				ConsecutiveErrors:    2,
			},
			wantTier:    TrustTierProbation,
			wantErrors: 0,
		},
		{
			name: "PROBATION tier stays at PROBATION",
			initialTB: &TrustBattery{
				Tier:              TrustTierProbation,
				ConsecutiveSuccesses: 0,
				ConsecutiveErrors:    2,
			},
			wantTier:    TrustTierProbation,
			wantErrors: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tb := tt.initialTB
			tb.RecordError()
			if tb.Tier != tt.wantTier {
				t.Errorf("RecordError().Tier = %v, want %v", tb.Tier, tt.wantTier)
			}
			if tb.ConsecutiveErrors != tt.wantErrors {
				t.Errorf("RecordError().ConsecutiveErrors = %v, want %v", tb.ConsecutiveErrors, tt.wantErrors)
			}
		})
	}
}

func TestTrustBattery_FraudFlagged(t *testing.T) {
	tests := []struct {
		name    string
		initialTB *TrustBattery
		wantTier TrustTier
		wantScore int
	}{
		{
			name: "fraud from STANDARD",
			initialTB: &TrustBattery{
				Tier:              TrustTierStandard,
				TrustScore:         50,
				ConsecutiveSuccesses: 10,
				ConsecutiveErrors:  0,
			},
			wantTier:  TrustTierBlocked,
			wantScore: 0,
		},
		{
			name: "fraud from STRATEGIC",
			initialTB: &TrustBattery{
				Tier:              TrustTierStrategic,
				TrustScore:         100,
				ConsecutiveSuccesses: 50,
				ConsecutiveErrors:  0,
			},
			wantTier:  TrustTierBlocked,
			wantScore: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tb := tt.initialTB
			tb.FraudFlagged()
			if tb.Tier != tt.wantTier {
				t.Errorf("FraudFlagged().Tier = %v, want %v", tb.Tier, tt.wantTier)
			}
			if tb.TrustScore != tt.wantScore {
				t.Errorf("FraudFlagged().TrustScore = %v, want %v", tb.TrustScore, tt.wantScore)
			}
			if tb.ConsecutiveSuccesses != 0 {
				t.Errorf("FraudFlagged().ConsecutiveSuccesses = %v, want 0", tb.ConsecutiveSuccesses)
			}
		})
	}
}

func TestTrustBattery_AdvanceDays(t *testing.T) {
	now := time.Now().UTC()
	
	tests := []struct {
		name        string
		initialTB   *TrustBattery
		days        int
		wantTier    TrustTier
		wantDays   int
	}{
		{
			name: "advance 1 day",
			initialTB: &TrustBattery{
				Tier:              TrustTierStandard,
				DaysInCurrentTier: 0,
			},
			days:      1,
			wantTier:  TrustTierStandard,
			wantDays: 1,
		},
		{
			name: "PROBATION upgrade after 30 days and 3 successes",
			initialTB: &TrustBattery{
				Tier:              TrustTierProbation,
				ConsecutiveSuccesses: 3,
				DaysInCurrentTier:   20,
			},
			days:      10,
			wantTier:  TrustTierStandard,
			wantDays: 0,
		},
		{
			name: "inactive downgrade after 90 days",
			initialTB: &TrustBattery{
				Tier:              TrustTierStandard,
				DaysInCurrentTier:   95,
				LastActiveAt:     &now,
			},
			days:      10,
			wantTier:  TrustTierProbation,
			wantDays: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tb := tt.initialTB
			tb.AdvanceDays(tt.days)
			if tb.Tier != tt.wantTier {
				t.Errorf("AdvanceDays(%d).Tier = %v, want %v", tt.days, tb.Tier, tt.wantTier)
			}
			if tb.DaysInCurrentTier != tt.wantDays {
				t.Errorf("AdvanceDays(%d).DaysInCurrentTier = %v, want %v", tt.days, tb.DaysInCurrentTier, tt.wantDays)
			}
		})
	}
}

func TestTrustBattery_ShouldDowngrade(t *testing.T) {
	now := time.Now().UTC()
	oldTime := now.Add(-181 * 24 * time.Hour)
	
	tests := []struct {
		name  string
		tb    *TrustBattery
		want  bool
	}{
		{
			name: "PROBATION never downgrades",
			tb: &TrustBattery{
				Tier: TrustTierProbation,
			},
			want: false,
		},
		{
			name: "3 consecutive errors triggers downgrade",
			tb: &TrustBattery{
				Tier:              TrustTierStandard,
				ConsecutiveErrors: 3,
			},
			want: true,
		},
		{
			name: "180 days inactive triggers downgrade",
			tb: &TrustBattery{
				Tier:         TrustTierStandard,
				LastActiveAt: &oldTime,
			},
			want: true,
		},
		{
			name: "no downgrade needed",
			tb: &TrustBattery{
				Tier:              TrustTierStandard,
				ConsecutiveErrors:  1,
				ConsecutiveSuccesses: 5,
				LastActiveAt:       &now,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.tb.ShouldDowngrade()
			if result != tt.want {
				t.Errorf("ShouldDowngrade() = %v, want %v", result, tt.want)
			}
		})
	}
}

func TestTrustBattery_NewTrustBattery(t *testing.T) {
	tb := NewTrustBattery()
	
	if tb.Tier != TrustTierProbation {
		t.Errorf("NewTrustBattery().Tier = %v, want PROBATION", tb.Tier)
	}
	if tb.TrustScore != 0 {
		t.Errorf("NewTrustBattery().TrustScore = %v, want 0", tb.TrustScore)
	}
	if tb.LastActiveAt == nil {
		t.Error("NewTrustBattery().LastActiveAt should not be nil")
	}
}

func TestTrustBattery_AllChecksPass(t *testing.T) {
	t.Run("from PROBATION to STANDARD", func(t *testing.T) {
		tb := &TrustBattery{Tier: TrustTierProbation, TrustScore: 0}
		tb.AllChecksPass()
		if tb.Tier != TrustTierStandard {
			t.Errorf("AllChecksPass().Tier = %v, want STANDARD", tb.Tier)
		}
		if tb.TrustScore != 31 {
			t.Errorf("AllChecksPass().TrustScore = %v, want 31", tb.TrustScore)
		}
		if tb.DaysInCurrentTier != 0 {
			t.Errorf("AllChecksPass().DaysInCurrentTier = %v, want 0", tb.DaysInCurrentTier)
		}
	})

	t.Run("STANDARD tier not affected", func(t *testing.T) {
		tb := &TrustBattery{Tier: TrustTierStandard, TrustScore: 50}
		tb.AllChecksPass()
		if tb.Tier != TrustTierStandard {
			t.Errorf("AllChecksPass() should not change tier from STANDARD, got %v", tb.Tier)
		}
		if tb.TrustScore != 50 {
			t.Errorf("AllChecksPass() should not change score from STANDARD, got %v", tb.TrustScore)
		}
	})
}

func TestTrustBattery_TransactionThresholdMet(t *testing.T) {
	tests := []struct {
		name   string
		tb     *TrustBattery
		txCount int
		wantTier TrustTier
		wantScore int
	}{
		{
			name:   "STANDARD with 3 txns -> PREFERRED",
			tb:     &TrustBattery{Tier: TrustTierStandard, TrustScore: 31},
			txCount: 3,
			wantTier: TrustTierPreferred,
			wantScore: 61,
		},
		{
			name:   "STANDARD with 2 txns stays STANDARD",
			tb:     &TrustBattery{Tier: TrustTierStandard, TrustScore: 31},
			txCount: 2,
			wantTier: TrustTierStandard,
			wantScore: 31,
		},
		{
			name:   "PREFERRED with 10 txns -> STRATEGIC",
			tb:     &TrustBattery{Tier: TrustTierPreferred, TrustScore: 61},
			txCount: 10,
			wantTier: TrustTierStrategic,
			wantScore: 86,
		},
		{
			name:   "PREFERRED with 5 txns stays PREFERRED",
			tb:     &TrustBattery{Tier: TrustTierPreferred, TrustScore: 61},
			txCount: 5,
			wantTier: TrustTierPreferred,
			wantScore: 61,
		},
		{
			name:   "PROBATION not affected",
			tb:     &TrustBattery{Tier: TrustTierProbation, TrustScore: 0},
			txCount: 100,
			wantTier: TrustTierProbation,
			wantScore: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tb := tt.tb
			tb.TransactionThresholdMet(tt.txCount)
			if tb.Tier != tt.wantTier {
				t.Errorf("TransactionThresholdMet().Tier = %v, want %v", tb.Tier, tt.wantTier)
			}
			if tb.TrustScore != tt.wantScore {
				t.Errorf("TransactionThresholdMet().TrustScore = %v, want %v", tb.TrustScore, tt.wantScore)
			}
		})
	}
}

func TestTrustBattery_DisputeFiled(t *testing.T) {
	tests := []struct {
		name   string
		tb     *TrustBattery
		wantTier TrustTier
		wantScore int
	}{
		{
			name: "PREFERRED -> STANDARD",
			tb: &TrustBattery{Tier: TrustTierPreferred, TrustScore: 61},
			wantTier: TrustTierStandard,
			wantScore: 31,
		},
		{
			name: "STRATEGIC -> PREFERRED",
			tb: &TrustBattery{Tier: TrustTierStrategic, TrustScore: 86},
			wantTier: TrustTierPreferred,
			wantScore: 61,
		},
		{
			name: "STANDARD not affected",
			tb: &TrustBattery{Tier: TrustTierStandard, TrustScore: 31},
			wantTier: TrustTierStandard,
			wantScore: 31,
		},
		{
			name: "PROBATION not affected",
			tb: &TrustBattery{Tier: TrustTierProbation, TrustScore: 0},
			wantTier: TrustTierProbation,
			wantScore: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tb := tt.tb
			tb.DisputeFiled()
			if tb.Tier != tt.wantTier {
				t.Errorf("DisputeFiled().Tier = %v, want %v", tb.Tier, tt.wantTier)
			}
			if tb.TrustScore != tt.wantScore {
				t.Errorf("DisputeFiled().TrustScore = %v, want %v", tb.TrustScore, tt.wantScore)
			}
			if tb.DaysInCurrentTier != 0 {
				t.Errorf("DisputeFiled().DaysInCurrentTier = %v, want 0", tb.DaysInCurrentTier)
			}
		})
	}
}

func TestTrustBattery_InactivityDecay(t *testing.T) {
	tests := []struct {
		name   string
		tb     *TrustBattery
		days   int
		wantTier TrustTier
		wantScore int
	}{
		{
			name: "STRATEGIC with 200 days -> PREFERRED",
			tb: &TrustBattery{Tier: TrustTierStrategic, TrustScore: 86},
			days: 200,
			wantTier: TrustTierPreferred,
			wantScore: 61,
		},
		{
			name: "PREFERRED with 180 days -> STANDARD",
			tb: &TrustBattery{Tier: TrustTierPreferred, TrustScore: 61},
			days: 180,
			wantTier: TrustTierStandard,
			wantScore: 31,
		},
		{
			name: "STRATEGIC with 100 days unchanged",
			tb: &TrustBattery{Tier: TrustTierStrategic, TrustScore: 86},
			days: 100,
			wantTier: TrustTierStrategic,
			wantScore: 86,
		},
		{
			name: "STANDARD with 200 days unchanged",
			tb: &TrustBattery{Tier: TrustTierStandard, TrustScore: 31},
			days: 200,
			wantTier: TrustTierStandard,
			wantScore: 31,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tb := tt.tb
			tb.InactivityDecay(tt.days)
			if tb.Tier != tt.wantTier {
				t.Errorf("InactivityDecay(%d).Tier = %v, want %v", tt.days, tb.Tier, tt.wantTier)
			}
			if tb.TrustScore != tt.wantScore {
				t.Errorf("InactivityDecay(%d).TrustScore = %v, want %v", tt.days, tb.TrustScore, tt.wantScore)
			}
		})
	}
}

func TestScoreBasedTier(t *testing.T) {
	tests := []struct {
		name  string
		score int
		want  TrustTier
	}{
		{"score 0", 0, TrustTierProbation},
		{"score 15", 15, TrustTierProbation},
		{"score 30", 30, TrustTierProbation},
		{"score 31", 31, TrustTierStandard},
		{"score 45", 45, TrustTierStandard},
		{"score 60", 60, TrustTierStandard},
		{"score 61", 61, TrustTierPreferred},
		{"score 70", 70, TrustTierPreferred},
		{"score 85", 85, TrustTierPreferred},
		{"score 86", 86, TrustTierStrategic},
		{"score 100", 100, TrustTierStrategic},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScoreBasedTier(tt.score)
			if got != tt.want {
				t.Errorf("ScoreBasedTier(%d) = %v, want %v", tt.score, got, tt.want)
			}
		})
	}
}

func TestTrustBattery_ToMap(t *testing.T) {
	now := time.Now().UTC()
	tb := &TrustBattery{
		Tier:                 TrustTierStandard,
		TrustScore:           50,
		ConsecutiveSuccesses: 5,
		ConsecutiveErrors:    1,
		DaysInCurrentTier:    30,
		LastActiveAt:         &now,
	}

	m := tb.ToMap()
	if m["tier"] != string(TrustTierStandard) {
		t.Errorf("ToMap() tier = %v, want STANDARD", m["tier"])
	}
	if m["trust_score"] != 50 {
		t.Errorf("ToMap() trust_score = %v, want 50", m["trust_score"])
	}
	if m["consecutive_successes"] != 5 {
		t.Errorf("ToMap() consecutive_successes = %v, want 5", m["consecutive_successes"])
	}
	if m["consecutive_errors"] != 1 {
		t.Errorf("ToMap() consecutive_errors = %v, want 1", m["consecutive_errors"])
	}
	if m["days_in_current_tier"] != 30 {
		t.Errorf("ToMap() days_in_current_tier = %v, want 30", m["days_in_current_tier"])
	}
	if m["last_active_at"] != now.Format(time.RFC3339) {
		t.Errorf("ToMap() last_active_at = %v, want %v", m["last_active_at"], now.Format(time.RFC3339))
	}
}

func TestTrustBattery_ToMap_NilLastActive(t *testing.T) {
	tb := &TrustBattery{
		Tier:       TrustTierProbation,
		TrustScore: 0,
	}
	m := tb.ToMap()
	if m["last_active_at"] != nil {
		t.Errorf("ToMap() last_active_at should be nil, got %v", m["last_active_at"])
	}
}

func TestTrustBattery_FromMap(t *testing.T) {
	now := time.Now().UTC()
	data := map[string]interface{}{
		"tier":                  "STANDARD",
		"trust_score":           float64(50),
		"consecutive_successes": float64(5),
		"consecutive_errors":    float64(1),
		"days_in_current_tier":  float64(30),
		"last_active_at":        now.Format(time.RFC3339),
	}

	tb := TrustBatteryFromMap(data)
	if tb.Tier != TrustTierStandard {
		t.Errorf("TrustBatteryFromMap().Tier = %v, want STANDARD", tb.Tier)
	}
	if tb.TrustScore != 50 {
		t.Errorf("TrustBatteryFromMap().TrustScore = %v, want 50", tb.TrustScore)
	}
	if tb.ConsecutiveSuccesses != 5 {
		t.Errorf("TrustBatteryFromMap().ConsecutiveSuccesses = %v, want 5", tb.ConsecutiveSuccesses)
	}
	if tb.ConsecutiveErrors != 1 {
		t.Errorf("TrustBatteryFromMap().ConsecutiveErrors = %v, want 1", tb.ConsecutiveErrors)
	}
	if tb.DaysInCurrentTier != 30 {
		t.Errorf("TrustBatteryFromMap().DaysInCurrentTier = %v, want 30", tb.DaysInCurrentTier)
	}
	if tb.LastActiveAt == nil || !tb.LastActiveAt.Truncate(time.Second).Equal(now.Truncate(time.Second)) {
		t.Errorf("TrustBatteryFromMap().LastActiveAt = %v, want %v", tb.LastActiveAt, now)
	}
}

func TestTrustBattery_FromMap_Defaults(t *testing.T) {
	data := map[string]interface{}{}
	tb := TrustBatteryFromMap(data)
	if tb.Tier != TrustTierProbation {
		t.Errorf("TrustBatteryFromMap() default Tier = %v, want PROBATION", tb.Tier)
	}
	if tb.TrustScore != 0 {
		t.Errorf("TrustBatteryFromMap() default TrustScore = %v, want 0", tb.TrustScore)
	}
	if tb.LastActiveAt != nil {
		t.Errorf("TrustBatteryFromMap() default LastActiveAt should be nil")
	}
}

func TestTrustBattery_FromMap_EmptyLastActive(t *testing.T) {
	data := map[string]interface{}{
		"last_active_at": "",
	}
	tb := TrustBatteryFromMap(data)
	if tb.LastActiveAt != nil {
		t.Errorf("TrustBatteryFromMap() with empty last_active_at should be nil")
	}
}
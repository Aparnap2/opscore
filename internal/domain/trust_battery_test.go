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

func TestTrustBattery_FlagFraud(t *testing.T) {
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
			wantTier:  TrustTierProbation,
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
			wantTier:  TrustTierProbation,
			wantScore: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tb := tt.initialTB
			tb.FlagFraud()
			if tb.Tier != tt.wantTier {
				t.Errorf("FlagFraud().Tier = %v, want %v", tb.Tier, tt.wantTier)
			}
			if tb.TrustScore != tt.wantScore {
				t.Errorf("FlagFraud().TrustScore = %v, want %v", tb.TrustScore, tt.wantScore)
			}
			if tb.ConsecutiveSuccesses != 0 {
				t.Errorf("FlagFraud().ConsecutiveSuccesses = %v, want 0", tb.ConsecutiveSuccesses)
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
	oldTime := now.Add(-91 * 24 * time.Hour)
	
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
			name: "90 days inactive triggers downgrade",
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
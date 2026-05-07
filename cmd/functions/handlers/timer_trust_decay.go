package handlers

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aparna/opscore/internal/domain"
)

// Trust tier decay rules
var trustDecayRules = map[domain.TrustTier]struct {
	NoActivityDays int
	DropTo         domain.TrustTier
}{
	domain.TrustTierStrategic: {NoActivityDays: 180, DropTo: domain.TrustTierCore},
	domain.TrustTierCore:       {NoActivityDays: 90, DropTo: domain.TrustTierStandard},
	domain.TrustTierStandard:   {NoActivityDays: 60, DropTo: domain.TrustTierProbation},
	domain.TrustTierProbation:  {NoActivityDays: 30, DropTo: domain.TrustTierProbation},
}

// TimerTrustDecayHandler handles Timer trigger for trust battery decay
// Queries vendors, applies tier decay rules
func TimerTrustDecayHandler(ctx context.Context) error {
	log.Println("Starting trust battery decay timer")

	// Get cosmos adapter
	cosmosAdapter, err := getCosmosAdapter(ctx)
	if err != nil {
		log.Printf("Cosmos adapter not available: %v", err)
		return err
	}

	now := time.Now()
	tenantID := "system"
	jobID := fmt.Sprintf("trust-decay-%d", now.Unix())

	// Create job
	job := &domain.Job{
		ID:           jobID,
		TenantID:     tenantID,
		WorkflowType: domain.WorkflowCompliance, // Using compliance workflow for system jobs
		Status:       domain.JobStatusRunning,
		CreatedAt:    now,
		UpdatedAt:    now,
		Input:        map[string]string{"task": "trust_decay"},
	}
	cosmosAdapter.UpsertJob(ctx, job)

	// Get all vendors
	vendors, err := cosmosAdapter.ListVendors(ctx, tenantID)
	if err != nil {
		log.Printf("Failed to list vendors: %v", err)
		job.Status = domain.JobStatusFailed
		job.Error = fmt.Sprintf("Failed to list vendors: %v", err)
		cosmosAdapter.UpsertJob(ctx, job)
		return err
	}

	log.Printf("Processing %d vendors for trust decay", len(vendors))

	var degradedCount, promotedCount int

	// Process each vendor
	for _, vendor := range vendors {
		if vendor.TrustBattery.LastActiveAt == nil {
			// Skip vendors with no activity
			continue
		}

		daysSinceActivity := int(now.Sub(*vendor.TrustBattery.LastActiveAt).Hours() / 24)

		// Check decay rules
		if rule, exists := trustDecayRules[vendor.TrustBattery.Tier]; exists {
			if daysSinceActivity > rule.NoActivityDays && vendor.TrustBattery.Tier != domain.TrustTierProbation {
				// Demote vendor
				oldTier := vendor.TrustBattery.Tier
				vendor.TrustBattery.Tier = rule.DropTo
				vendor.UpdatedAt = now

				// Log audit event
				cosmosAdapter.AppendAuditEvent(ctx, &domain.AuditEvent{
					Actor:       "timer_trust_decay",
					Action:      "TRUST_DECAYED",
					TargetType:  "vendor",
					TargetID:    vendor.ID,
					OldState:    string(oldTier),
					NewState:    string(vendor.TrustBattery.Tier),
					Timestamp:   now,
				})

				// Save vendor
				cosmosAdapter.UpsertVendor(ctx, vendor)
				degradedCount++

				log.Printf("Vendor %s degraded from %s to %s (inactive %d days)",
					vendor.Name, oldTier, vendor.TrustBattery.Tier, daysSinceActivity)
			}
		}

		// Check promotion rules (positive activity increases trust)
		if vendor.TrustBattery.ConsecutiveSuccesses >= 10 && vendor.TrustBattery.Tier == domain.TrustTierProbation {
			oldTier := vendor.TrustBattery.Tier
			vendor.TrustBattery.Tier = domain.TrustTierStandard
			vendor.UpdatedAt = now

			cosmosAdapter.AppendAuditEvent(ctx, &domain.AuditEvent{
				Actor:       "timer_trust_decay",
				Action:      "TRUST_PROMOTED",
				TargetType:  "vendor",
				TargetID:    vendor.ID,
				OldState:    string(oldTier),
				NewState:    string(vendor.TrustBattery.Tier),
				Timestamp:   now,
			})

			cosmosAdapter.UpsertVendor(ctx, vendor)
			promotedCount++

			log.Printf("Vendor %s promoted from %s to %s (10+ successful transactions)",
				vendor.Name, oldTier, vendor.TrustBattery.Tier)
		}
	}

	// Update job status
	job.Status = domain.JobStatusCompleted
	job.UpdatedAt = time.Now()
	job.Output = map[string]int{
		"vendors_processed": len(vendors),
		"degraded":         degradedCount,
		"promoted":        promotedCount,
	}
	cosmosAdapter.UpsertJob(ctx, job)

	// Log audit event
	cosmosAdapter.AppendAuditEvent(ctx, &domain.AuditEvent{
		Actor:       "timer_trust_decay",
		Action:      "DECAY_COMPLETED",
		TargetType:  "job",
		TargetID:    jobID,
		NewState:    "COMPLETED",
		Timestamp:   now,
	})

	log.Printf("Trust decay completed: processed=%d, degraded=%d, promoted=%d",
		len(vendors), degradedCount, promotedCount)

	return nil
}

// TimerTrustDecayHandlerCron provides a cron-compatible timer signature
func TimerTrustDecayHandlerCron(ctx context.Context, timerTriggerFunc interface{}) error {
	return TimerTrustDecayHandler(ctx)
}

// TriggerTrustDecay manually triggers trust decay (for testing/admin)
func TriggerTrustDecay(ctx context.Context) error {
	log.Println("Manual trust decay triggered")
	return TimerTrustDecayHandler(ctx)
}
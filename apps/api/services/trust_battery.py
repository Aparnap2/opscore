from enum import Enum
from datetime import datetime, timedelta
from dataclasses import dataclass, field
from typing import Optional


class TrustTier(str, Enum):
    PROBATION = "PROBATION"
    STANDARD = "STANDARD"
    CORE = "CORE"
    STRATEGIC = "STRATEGIC"


@dataclass
class TrustBattery:
    tier: TrustTier = TrustTier.PROBATION
    trust_score: int = 0
    consecutive_successes: int = 0
    consecutive_errors: int = 0
    days_in_current_tier: int = 0
    last_active_at: Optional[datetime] = field(default_factory=datetime.utcnow)

    def record_success(self) -> None:
        self.consecutive_successes += 1
        self.consecutive_errors = 0
        self.last_active_at = datetime.utcnow()

        if self.tier == TrustTier.PROBATION and self.consecutive_successes >= 3:
            self._try_upgrade()

    def record_error(self) -> None:
        self.consecutive_errors += 1
        self.consecutive_successes = 0

        if self.consecutive_errors >= 3:
            self._downgrade()

    def flag_fraud(self) -> None:
        self.tier = TrustTier.PROBATION
        self.trust_score = 0
        self.consecutive_successes = 0
        self.consecutive_errors = 0
        self.days_in_current_tier = 0

    def advance_days(self, days: int) -> None:
        self.days_in_current_tier += days
        self.last_active_at = datetime.utcnow()

        if self.tier == TrustTier.PROBATION and self.days_in_current_tier >= 30 and self.consecutive_successes >= 3:
            self._try_upgrade()

        if self.tier != TrustTier.PROBATION and (self.days_in_current_tier - days) >= 90:
            self._downgrade()

    def _try_upgrade(self) -> bool:
        upgrade_requirements = {
            TrustTier.PROBATION: (30, 3),
            TrustTier.STANDARD: (90, 10),
            TrustTier.CORE: (180, 0),
            TrustTier.STRATEGIC: (float('inf'), float('inf')),
        }

        days_required, tx_required = upgrade_requirements.get(self.tier, (float('inf'), float('inf')))

        if self.days_in_current_tier >= days_required:
            current_idx = list(TrustTier).index(self.tier)
            if current_idx < len(list(TrustTier)) - 1:
                self.tier = list(TrustTier)[current_idx + 1]
                self.days_in_current_tier = 0
                self.consecutive_successes = 0
                return True

        return False

    def _downgrade(self) -> None:
        if self.tier == TrustTier.PROBATION:
            return

        current_idx = list(TrustTier).index(self.tier)
        if current_idx > 0:
            self.tier = list(TrustTier)[current_idx - 1]
            self.consecutive_errors = 0
            self.days_in_current_tier = 0

    def should_downgrade(self) -> bool:
        if self.tier == TrustTier.PROBATION:
            return False

        if self.consecutive_errors >= 3:
            return True

        if self.last_active_at:
            days_inactive = (datetime.utcnow() - self.last_active_at).days
            if days_inactive >= 90:
                return True

        return False

    def to_dict(self) -> dict:
        return {
            "tier": self.tier.value,
            "trust_score": self.trust_score,
            "consecutive_successes": self.consecutive_successes,
            "consecutive_errors": self.consecutive_errors,
            "days_in_current_tier": self.days_in_current_tier,
            "last_active_at": self.last_active_at.isoformat() if self.last_active_at else None,
        }

    @classmethod
    def from_dict(cls, data: dict) -> "TrustBattery":
        return cls(
            tier=TrustTier(data.get("tier", "PROBATION")),
            trust_score=data.get("trust_score", 0),
            consecutive_successes=data.get("consecutive_successes", 0),
            consecutive_errors=data.get("consecutive_errors", 0),
            days_in_current_tier=data.get("days_in_current_tier", 0),
            last_active_at=datetime.fromisoformat(data["last_active_at"]) if data.get("last_active_at") else None,
        )
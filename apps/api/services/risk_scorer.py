import re
from dataclasses import dataclass
from typing import List, Optional
from rapidfuzz import fuzz


GST_REGEX = r'^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z]{1}[1-9A-Z]{1}Z[0-9A-Z]{1}$'
PAN_REGEX = r'^[A-Z]{5}[0-9]{4}[A-Z]{1}$'
IFSC_REGEX = r'^[A-Z]{4}0[A-Z0-9]{6}$'


@dataclass
class RiskResult:
    score: int
    tier: str
    flags: List[str]


async def compute_vendor_risk(
    vendor: dict,
    existing_vendors: List[dict],
    blacklist: List[str],
    tenant_id: str,
    graphiti_service=None,
) -> RiskResult:
    score = 0
    flags = []

    gst = vendor.get("gst_number", "")
    pan = vendor.get("pan_number", "")
    ifsc = vendor.get("ifsc_code", "")
    bank_account = vendor.get("bank_account", "")
    name = vendor.get("name", "")

    if not re.match(GST_REGEX, gst) if gst else True:
        score += 25
        flags.append("INVALID_GST_FORMAT")

    if not re.match(PAN_REGEX, pan) if pan else True:
        score += 20
        flags.append("INVALID_PAN_FORMAT")

    if ifsc and not re.match(IFSC_REGEX, ifsc):
        score += 15
        flags.append("INVALID_IFSC_FORMAT")

    if gst in blacklist or pan in blacklist:
        score += 50
        flags.append("BLACKLISTED_ENTITY")

    if bank_account:
        for ev in existing_vendors:
            if ev.get("bank_account") == bank_account:
                score += 40
                flags.append(f"DUPLICATE_BANK_ACCOUNT:{ev.get('id', 'unknown')}")
                break

    if name and existing_vendors:
        for ev in existing_vendors:
            existing_name = ev.get("name", "")
            if existing_name:
                similarity = fuzz.token_sort_ratio(name, existing_name)
                if similarity > 85:
                    score += 20
                    flags.append(f"SIMILAR_VENDOR_NAME:{ev.get('id', 'unknown')}:{similarity}%")
                    break

    if graphiti_service and gst:
        try:
            risky_connections = await graphiti_service.find_risky_connections(gst, tenant_id)
            if risky_connections:
                score += 30
                flags.append(f"CONNECTED_TO_RISKY_ENTITY:{len(risky_connections)}")
        except Exception:
            pass

    tier = "LOW" if score < 30 else "MEDIUM" if score < 60 else "HIGH"

    return RiskResult(score=score, tier=tier, flags=flags)
from fastapi import APIRouter, Depends, HTTPException, status
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy import select
from typing import List
from uuid import uuid4
from pydantic import BaseModel, EmailStr

from apps.api.db.session import get_db
from apps.api.db import Vendor, User
from apps.api.services.auth_service import get_current_user, require_role
from apps.api.services.risk_scorer import compute_vendor_risk
from apps.api.services.trust_battery import TrustBattery, TrustTier

router = APIRouter(prefix="/vendors", tags=["vendors"])


class VendorCreateRequest(BaseModel):
    name: str
    gst_number: str
    pan_number: str
    email: EmailStr
    phone: str = None
    address: str = None
    category: str = "Services"
    ifsc_code: str = None
    bank_account: str = None


class VendorResponse(BaseModel):
    id: str
    name: str
    gst_number: str = None
    pan_number: str = None
    category: str = None
    trust_tier: str
    trust_score: int
    is_active: bool
    created_at: str

    class Config:
        from_attributes = True


@router.post("/onboard", response_model=VendorResponse)
async def onboard_vendor(
    request: VendorCreateRequest,
    current_user: User = Depends(require_role("procurement", "admin")),
    db: AsyncSession = Depends(get_db),
):
    vendor_id = str(uuid4())

    result = await db.execute(
        select(Vendor).where(Vendor.tenant_id == current_user.tenant_id)
    )
    existing_vendors = [
        {
            "id": str(v.id),
            "name": v.name,
            "gst_number": v.gst_number,
            "bank_account": v.bank_account,
        }
        for v in result.scalars().all()
    ]

    result = await db.execute(
        select(Vendor).where(Vendor.tenant_id == current_user.tenant_id, Vendor.gst_number == request.gst_number)
    )
    blacklist = [v.gst_number for v in result.scalars().all() if v.gst_number]

    risk_result = await compute_vendor_risk(
        vendor=request.model_dump(),
        existing_vendors=existing_vendors,
        blacklist=blacklist,
        tenant_id=str(current_user.tenant_id),
    )

    vendor = Vendor(
        id=vendor_id,
        tenant_id=current_user.tenant_id,
        name=request.name,
        gst_number=request.gst_number,
        pan_number=request.pan_number,
        email=request.email,
        phone=request.phone,
        address=request.address,
        category=request.category,
        ifsc_code=request.ifsc_code,
        bank_account=request.bank_account,
        trust_tier=TrustTier.PROBATION.value,
        trust_score=100 - risk_result.score,
    )
    db.add(vendor)
    await db.flush()
    await db.refresh(vendor)

    return VendorResponse(
        id=str(vendor.id),
        name=vendor.name,
        gst_number=vendor.gst_number,
        pan_number=vendor.pan_number,
        category=vendor.category,
        trust_tier=vendor.trust_tier,
        trust_score=vendor.trust_score,
        is_active=vendor.is_active,
        created_at=vendor.created_at.isoformat(),
    )


@router.get("", response_model=List[VendorResponse])
async def list_vendors(
    skip: int = 0,
    limit: int = 50,
    current_user: User = Depends(require_role("finance", "procurement", "admin", "viewer")),
    db: AsyncSession = Depends(get_db),
):
    result = await db.execute(
        select(Vendor)
        .where(Vendor.tenant_id == current_user.tenant_id)
        .offset(skip)
        .limit(limit)
    )
    vendors = result.scalars().all()

    return [
        VendorResponse(
            id=str(v.id),
            name=v.name,
            gst_number=v.gst_number,
            pan_number=v.pan_number,
            category=v.category,
            trust_tier=v.trust_tier,
            trust_score=v.trust_score,
            is_active=v.is_active,
            created_at=v.created_at.isoformat(),
        )
        for v in vendors
    ]


@router.get("/{vendor_id}", response_model=VendorResponse)
async def get_vendor(
    vendor_id: str,
    current_user: User = Depends(require_role("finance", "procurement", "admin", "viewer")),
    db: AsyncSession = Depends(get_db),
):
    result = await db.get(Vendor, vendor_id)
    if not result or result.tenant_id != current_user.tenant_id:
        raise HTTPException(status_code=404, detail="Vendor not found")

    return VendorResponse(
        id=str(result.id),
        name=result.name,
        gst_number=result.gst_number,
        pan_number=result.pan_number,
        category=result.category,
        trust_tier=result.trust_tier,
        trust_score=result.trust_score,
        is_active=result.is_active,
        created_at=result.created_at.isoformat(),
    )
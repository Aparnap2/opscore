from fastapi import APIRouter, Depends, HTTPException, status
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy import select
from typing import List
from pydantic import BaseModel

from apps.api.db.session import get_db
from apps.api.db import Regulation, ComplianceGap, User
from apps.api.services.auth_service import get_current_user, require_role
from apps.api.services.compliance_scraper import scraper

router = APIRouter(prefix="/compliance", tags=["compliance"])


class RegulationResponse(BaseModel):
    id: str
    source: str
    title: str
    url: str = None
    summary: str = None
    created_at: str


class ComplianceGapResponse(BaseModel):
    id: str
    regulation_id: str
    gap_description: str
    severity: str = None
    deadline: str = None
    department: str = None
    citation_page: int = None
    status: str
    ragas_score: float = None
    created_at: str


@router.get("/regulations", response_model=List[RegulationResponse])
async def list_regulations(
    source: str = None,
    skip: int = 0,
    limit: int = 50,
    current_user: User = Depends(require_role("compliance", "admin", "viewer")),
    db: AsyncSession = Depends(get_db),
):
    query = select(Regulation).where(Regulation.tenant_id == current_user.tenant_id)
    if source:
        query = query.where(Regulation.source == source)

    result = await db.execute(query.offset(skip).limit(limit))
    regulations = result.scalars().all()

    return [
        RegulationResponse(
            id=str(r.id),
            source=r.source,
            title=r.title,
            url=r.url,
            summary=r.summary,
            created_at=r.created_at.isoformat(),
        )
        for r in regulations
    ]


@router.post("/scrape")
async def trigger_compliance_scrape(
    sources: List[str] = ["sebi", "rbi", " gst"],
    current_user: User = Depends(require_role("compliance", "admin")),
    db: AsyncSession = Depends(get_db),
):
    results = {}

    for source in sources:
        updates = await scraper.scrape_regulatory_updates(source)
        results[source] = updates

        for update in updates:
            regulation = Regulation(
                tenant_id=current_user.tenant_id,
                source=update["source"],
                title=update["title"],
                url=update["url"],
                summary=update.get("title"),
            )
            db.add(regulation)

    await db.flush()

    return {"status": "completed", "sources": results}


@router.get("/gaps", response_model=List[ComplianceGapResponse])
async def list_compliance_gaps(
    severity: str = None,
    status: str = None,
    skip: int = 0,
    limit: int = 50,
    current_user: User = Depends(require_role("compliance", "admin", "viewer")),
    db: AsyncSession = Depends(get_db),
):
    query = select(ComplianceGap).where(ComplianceGap.tenant_id == current_user.tenant_id)
    if severity:
        query = query.where(ComplianceGap.severity == severity)
    if status:
        query = query.where(ComplianceGap.status == status)

    result = await db.execute(query.offset(skip).limit(limit))
    gaps = result.scalars().all()

    return [
        ComplianceGapResponse(
            id=str(g.id),
            regulation_id=str(g.regulation_id),
            gap_description=g.gap_description,
            severity=g.severity,
            deadline=g.deadline.isoformat() if g.deadline else None,
            department=g.department,
            citation_page=g.citation_page,
            status=g.status,
            ragas_score=g.ragas_score,
            created_at=g.created_at.isoformat(),
        )
        for g in gaps
    ]


@router.get("/gaps/{gap_id}", response_model=ComplianceGapResponse)
async def get_compliance_gap(
    gap_id: str,
    current_user: User = Depends(require_role("compliance", "admin", "viewer")),
    db: AsyncSession = Depends(get_db),
):
    result = await db.get(ComplianceGap, gap_id)
    if not result or result.tenant_id != current_user.tenant_id:
        raise HTTPException(status_code=404, detail="Gap not found")

    return ComplianceGapResponse(
        id=str(result.id),
        regulation_id=str(result.regulation_id),
        gap_description=result.gap_description,
        severity=result.severity,
        deadline=result.deadline.isoformat() if result.deadline else None,
        department=result.department,
        citation_page=result.citation_page,
        status=result.status,
        ragas_score=result.ragas_score,
        created_at=result.created_at.isoformat(),
    )
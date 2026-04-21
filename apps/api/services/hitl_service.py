from typing import Optional, List, Dict
from uuid import UUID
from datetime import datetime
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy import select, update
from apps.api.db import HITLQueue
from pydantic import BaseModel


class CreateHITLRequest(BaseModel):
    tenant_id: str
    job_id: Optional[str] = None
    queue_type: str
    payload: dict
    assigned_to: Optional[str] = None


class UpdateHITLRequest(BaseModel):
    status: Optional[str] = None
    notes: Optional[str] = None
    reviewed_at: Optional[datetime] = None
    reviewer_id: Optional[str] = None


class HITLService:
    @staticmethod
    async def create_hitl_item(
        db: AsyncSession,
        request: CreateHITLRequest,
    ) -> HITLQueue:
        item = HITLQueue(
            tenant_id=UUID(request.tenant_id),
            job_id=UUID(request.job_id) if request.job_id else None,
            queue_type=request.queue_type,
            payload=request.payload,
            assigned_to=UUID(request.assigned_to) if request.assigned_to else None,
            status="pending",
        )
        db.add(item)
        await db.flush()
        await db.refresh(item)
        return item

    @staticmethod
    async def get_pending_items(
        db: AsyncSession,
        tenant_id: str,
        queue_type: Optional[str] = None,
        limit: int = 50,
    ) -> List[HITLQueue]:
        query = select(HITLQueue).where(
            HITLQueue.tenant_id == UUID(tenant_id),
            HITLQueue.status == "pending",
        )
        if queue_type:
            query = query.where(HITLQueue.queue_type == queue_type)

        result = await db.execute(query.limit(limit))
        return list(result.scalars().all())

    @staticmethod
    async def approve_item(
        db: AsyncSession,
        item_id: str,
        reviewer_id: str,
        notes: Optional[str] = None,
    ) -> HITLQueue:
        stmt = (
            update(HITLQueue)
            .where(HITLQueue.id == UUID(item_id))
            .values(
                status="approved",
                reviewer_id=UUID(reviewer_id),
                reviewed_at=datetime.utcnow(),
                notes=notes,
            )
        )
        await db.execute(stmt)
        await db.flush()

        result = await db.execute(
            select(HITLQueue).where(HITLQueue.id == UUID(item_id))
        )
        return result.scalar_one()

    @staticmethod
    async def reject_item(
        db: AsyncSession,
        item_id: str,
        reviewer_id: str,
        notes: Optional[str] = None,
    ) -> HITLQueue:
        stmt = (
            update(HITLQueue)
            .where(HITLQueue.id == UUID(item_id))
            .values(
                status="rejected",
                reviewer_id=UUID(reviewer_id),
                reviewed_at=datetime.utcnow(),
                notes=notes,
            )
        )
        await db.execute(stmt)
        await db.flush()

        result = await db.execute(
            select(HITLQueue).where(HITLQueue.id == UUID(item_id))
        )
        return result.scalar_one()

    @staticmethod
    async def get_item(
        db: AsyncSession,
        item_id: str,
    ) -> Optional[HITLQueue]:
        result = await db.execute(
            select(HITLQueue).where(HITLQueue.id == UUID(item_id))
        )
        return result.scalar_one_or_none()


hitl_service = HITLService()
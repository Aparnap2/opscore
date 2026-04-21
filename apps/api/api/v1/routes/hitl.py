from fastapi import APIRouter, Depends, HTTPException, status
from sqlalchemy.ext.asyncio import AsyncSession
from typing import List
from uuid import UUID

from apps.api.db.session import get_db
from apps.api.db import User
from apps.api.services.auth_service import get_current_user, require_role
from apps.api.api.v1.schemas.documents import HITLReviewRequest, HITLQueueResponse
from apps.api.services.hitl_service import hitl_service

router = APIRouter(prefix="/hitl", tags=["hitl"])


@router.get("/queue", response_model=List[HITLQueueResponse])
async def get_hitl_queue(
    queue_type: str = None,
    limit: int = 50,
    current_user: User = Depends(require_role("finance", "compliance", "admin")),
    db: AsyncSession = Depends(get_db),
):
    items = await hitl_service.get_pending_items(
        db=db,
        tenant_id=str(current_user.tenant_id),
        queue_type=queue_type,
        limit=limit,
    )

    return [
        HITLQueueResponse(
            id=str(item.id),
            job_id=str(item.job_id) if item.job_id else "",
            queue_type=item.queue_type,
            payload=item.payload,
            status=item.status,
            assigned_to=str(item.assigned_to) if item.assigned_to else None,
            created_at=item.created_at,
            reviewed_at=item.reviewed_at,
            notes=item.notes,
        )
        for item in items
    ]


@router.post("/queue/{item_id}/approve", response_model=HITLQueueResponse)
async def approve_hitl_item(
    item_id: str,
    request: HITLReviewRequest,
    current_user: User = Depends(require_role("finance", "admin")),
    db: AsyncSession = Depends(get_db),
):
    item = await hitl_service.get_item(db, item_id)
    if not item or str(item.tenant_id) != str(current_user.tenant_id):
        raise HTTPException(status_code=404, detail="Item not found")

    updated_item = await hitl_service.approve_item(
        db=db,
        item_id=item_id,
        reviewer_id=str(current_user.id),
        notes=request.notes,
    )

    return HITLQueueResponse(
        id=str(updated_item.id),
        job_id=str(updated_item.job_id) if updated_item.job_id else "",
        queue_type=updated_item.queue_type,
        payload=updated_item.payload,
        status=updated_item.status,
        assigned_to=str(updated_item.assigned_to) if updated_item.assigned_to else None,
        created_at=updated_item.created_at,
        reviewed_at=updated_item.reviewed_at,
        notes=updated_item.notes,
    )


@router.post("/queue/{item_id}/reject", response_model=HITLQueueResponse)
async def reject_hitl_item(
    item_id: str,
    request: HITLReviewRequest,
    current_user: User = Depends(require_role("finance", "admin")),
    db: AsyncSession = Depends(get_db),
):
    item = await hitl_service.get_item(db, item_id)
    if not item or str(item.tenant_id) != str(current_user.tenant_id):
        raise HTTPException(status_code=404, detail="Item not found")

    updated_item = await hitl_service.reject_item(
        db=db,
        item_id=item_id,
        reviewer_id=str(current_user.id),
        notes=request.notes,
    )

    return HITLQueueResponse(
        id=str(updated_item.id),
        job_id=str(updated_item.job_id) if updated_item.job_id else "",
        queue_type=updated_item.queue_type,
        payload=updated_item.payload,
        status=updated_item.status,
        assigned_to=str(updated_item.assigned_to) if updated_item.assigned_to else None,
        created_at=updated_item.created_at,
        reviewed_at=updated_item.reviewed_at,
        notes=updated_item.notes,
    )
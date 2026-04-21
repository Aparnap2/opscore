from fastapi import APIRouter, Depends, UploadFile, File, HTTPException, status
from fastapi.responses import StreamingResponse
from sqlalchemy.ext.asyncio import AsyncSession
from uuid import uuid4
from typing import List
import logging

from apps.api.db.session import get_db
from apps.api.db import DocumentJob, ExtractedDocument, User
from apps.api.services.auth_service import get_current_user, require_role
from apps.api.api.v1.schemas.documents import (
    DocumentUploadResponse,
    JobStatusResponse,
    HITLReviewRequest,
    HITLQueueResponse,
)
from apps.api.services.hitl_service import hitl_service

router = APIRouter(prefix="/documents", tags=["documents"])
logger = logging.getLogger(__name__)


@router.post("/ingest", response_model=DocumentUploadResponse)
async def ingest_document(
    file: UploadFile = File(...),
    workflow_type: str = "ingestion",
    current_user: User = Depends(require_role("finance", "compliance", "admin")),
    db: AsyncSession = Depends(get_db),
):
    if file.content_type != "application/pdf":
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="Only PDF files are accepted"
        )

    if file.size and file.size > 25 * 1024 * 1024:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="File size must be less than 25MB"
        )

    job_id = str(uuid4())

    file_bytes = await file.read()

    job = DocumentJob(
        id=job_id,
        tenant_id=current_user.tenant_id,
        workflow_type=workflow_type,
        status="pending",
        input_path=f"{current_user.tenant_id}/uploads/{job_id}.pdf",
    )
    db.add(job)
    await db.flush()

    return DocumentUploadResponse(
        job_id=job_id,
        status="pending",
        message="Document uploaded successfully. Processing will begin shortly."
    )


@router.get("/jobs/{job_id}/status", response_model=JobStatusResponse)
async def get_job_status(
    job_id: str,
    current_user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    result = await db.get(DocumentJob, job_id)
    if not result or result.tenant_id != current_user.tenant_id:
        raise HTTPException(status_code=404, detail="Job not found")

    return JobStatusResponse(
        job_id=result.id,
        status=result.status,
        doc_type=result.output_path,
        created_at=result.created_at,
        updated_at=result.updated_at,
    )


@router.get("/jobs", response_model=List[JobStatusResponse])
async def list_documents(
    skip: int = 0,
    limit: int = 50,
    current_user: User = Depends(require_role("finance", "compliance", "admin", "viewer")),
    db: AsyncSession = Depends(get_db),
):
    from sqlalchemy import select

    result = await db.execute(
        select(DocumentJob)
        .where(DocumentJob.tenant_id == current_user.tenant_id)
        .order_by(DocumentJob.created_at.desc())
        .offset(skip)
        .limit(limit)
    )
    jobs = result.scalars().all()

    return [
        JobStatusResponse(
            job_id=job.id,
            status=job.status,
            created_at=job.created_at,
            updated_at=job.updated_at,
        )
        for job in jobs
    ]


@router.get("/extracted/{doc_id}")
async def get_extracted_document(
    doc_id: str,
    current_user: User = Depends(require_role("finance", "compliance", "admin")),
    db: AsyncSession = Depends(get_db),
):
    result = await db.get(ExtractedDocument, doc_id)
    if not result or result.tenant_id != current_user.tenant_id:
        raise HTTPException(status_code=404, detail="Document not found")

    return {
        "id": result.id,
        "doc_type": result.doc_type,
        "raw_extraction": result.raw_extraction,
        "validated_data": result.validated_data,
        "confidence": result.confidence,
        "status": result.status,
        "created_at": result.created_at,
    }
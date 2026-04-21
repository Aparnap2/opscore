from sqlalchemy.ext.asyncio import AsyncSession, create_async_engine, async_sessionmaker
from sqlalchemy.orm import DeclarativeBase
from sqlalchemy import Column, String, Text, Boolean, DateTime, Integer, Float, Date, UniqueConstraint
from sqlalchemy.dialects.postgresql import UUID, JSONB, BIGINT
from datetime import datetime
import uuid


class Base(DeclarativeBase):
    pass


class Tenant(Base):
    __tablename__ = "tenants"

    id = Column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    name = Column(String(255), nullable=False)
    slug = Column(String(100), unique=True, nullable=False)
    plan = Column(String(50), default="starter")
    created_at = Column(DateTime(timezone=True), default=datetime.utcnow)


class User(Base):
    __tablename__ = "users"

    id = Column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    tenant_id = Column(UUID(as_uuid=True), nullable=False)
    email = Column(String(255), nullable=False)
    password_hash = Column(String(255), nullable=False)
    full_name = Column(String(255))
    role = Column(String(50), nullable=False, default="viewer")
    is_active = Column(Boolean, default=True)
    created_at = Column(DateTime(timezone=True), default=datetime.utcnow)
    updated_at = Column(DateTime(timezone=True), default=datetime.utcnow, onupdate=datetime.utcnow)

    __table_args__ = (UniqueConstraint("tenant_id", "email", name="uq_user_tenant_email"),)


class DocumentJob(Base):
    __tablename__ = "document_jobs"

    id = Column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    tenant_id = Column(UUID(as_uuid=True), nullable=False)
    workflow_type = Column(String(50), nullable=False)
    status = Column(String(50), nullable=False, default="pending")
    input_path = Column(String(500))
    output_path = Column(String(500))
    temporal_run_id = Column(String(100))
    created_at = Column(DateTime(timezone=True), default=datetime.utcnow)
    updated_at = Column(DateTime(timezone=True), default=datetime.utcnow, onupdate=datetime.utcnow)


class ExtractedDocument(Base):
    __tablename__ = "extracted_documents"

    id = Column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    tenant_id = Column(UUID(as_uuid=True), nullable=False)
    job_id = Column(UUID(as_uuid=True))
    doc_type = Column(String(50), nullable=False)
    raw_extraction = Column(JSONB)
    validated_data = Column(JSONB)
    confidence = Column(JSONB)
    status = Column(String(50), default="pending_review")
    erp_synced = Column(Boolean, default=False)
    erp_id = Column(String(100))
    created_at = Column(DateTime(timezone=True), default=datetime.utcnow)


class HITLQueue(Base):
    __tablename__ = "hitl_queue"

    id = Column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    tenant_id = Column(UUID(as_uuid=True), nullable=False)
    job_id = Column(UUID(as_uuid=True))
    queue_type = Column(String(50), nullable=False)
    payload = Column(JSONB, nullable=False)
    assigned_to = Column(UUID(as_uuid=True))
    status = Column(String(50), default="pending")
    reviewed_at = Column(DateTime(timezone=True))
    reviewer_id = Column(UUID(as_uuid=True))
    notes = Column(Text)
    created_at = Column(DateTime(timezone=True), default=datetime.utcnow)


class Vendor(Base):
    __tablename__ = "vendors"

    id = Column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    tenant_id = Column(UUID(as_uuid=True), nullable=False)
    name = Column(String(255), nullable=False)
    gst_number = Column(String(20))
    pan_number = Column(String(20))
    email = Column(String(255))
    phone = Column(String(20))
    address = Column(Text)
    category = Column(String(50))
    ifsc_code = Column(String(20))
    bank_account = Column(String(50))
    trust_tier = Column(String(20), default="PROBATION")
    trust_score = Column(Integer, default=0)
    consecutive_errors = Column(Integer, default=0)
    qb_vendor_id = Column(String(100))
    is_active = Column(Boolean, default=True)
    last_active_at = Column(DateTime(timezone=True))
    created_at = Column(DateTime(timezone=True), default=datetime.utcnow)
    updated_at = Column(DateTime(timezone=True), default=datetime.utcnow, onupdate=datetime.utcnow)


class ComplianceGap(Base):
    __tablename__ = "compliance_gaps"

    id = Column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    tenant_id = Column(UUID(as_uuid=True), nullable=False)
    regulation_id = Column(UUID(as_uuid=True), nullable=False)
    gap_description = Column(Text, nullable=False)
    severity = Column(String(20))
    deadline = Column(Date)
    department = Column(String(50))
    owner_id = Column(UUID(as_uuid=True))
    citation_page = Column(Integer)
    citation_para = Column(Text)
    ticket_id = Column(String(100))
    status = Column(String(50), default="open")
    ragas_score = Column(Float)
    created_at = Column(DateTime(timezone=True), default=datetime.utcnow)


class Regulation(Base):
    __tablename__ = "regulations"

    id = Column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    tenant_id = Column(UUID(as_uuid=True), nullable=False)
    source = Column(String(50), nullable=False)
    title = Column(String(500), nullable=False)
    url = Column(String(500))
    document_path = Column(String(500))
    summary = Column(Text)
    created_at = Column(DateTime(timezone=True), default=datetime.utcnow)


class AuditEvent(Base):
    __tablename__ = "audit_events"

    id = Column(BIGINT, primary_key=True)
    tenant_id = Column(UUID(as_uuid=True), nullable=False)
    entity_type = Column(String(50), nullable=False)
    entity_id = Column(UUID(as_uuid=True), nullable=False)
    action = Column(String(50), nullable=False)
    actor_id = Column(UUID(as_uuid=True))
    actor_type = Column(String(20))
    diff = Column(JSONB)
    created_at = Column(DateTime(timezone=True), default=datetime.utcnow)


class BlacklistEntry(Base):
    __tablename__ = "blacklist_entries"

    id = Column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    tenant_id = Column(UUID(as_uuid=True), nullable=False)
    entity_type = Column(String(20), nullable=False)
    entity_value = Column(String(100), nullable=False)
    reason = Column(Text)
    created_by = Column(UUID(as_uuid=True))
    created_at = Column(DateTime(timezone=True), default=datetime.utcnow)

    __table_args__ = (UniqueConstraint("tenant_id", "entity_type", "entity_value", name="uq_blacklist_entry"),)


class ApiKey(Base):
    __tablename__ = "api_keys"

    id = Column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    tenant_id = Column(UUID(as_uuid=True), nullable=False)
    name = Column(String(100), nullable=False)
    key_hash = Column(String(255), nullable=False)
    expires_at = Column(DateTime(timezone=True))
    last_used_at = Column(DateTime(timezone=True))
    is_active = Column(Boolean, default=True)
    created_at = Column(DateTime(timezone=True), default=datetime.utcnow)
from pydantic import BaseModel, Field, field_validator
from typing import Optional, List, Any
from datetime import date, datetime
from decimal import Decimal
from enum import Enum


class DocType(str, Enum):
    INVOICE = "invoice"
    CONTRACT = "contract"
    GST_NOTICE = "gst_notice"
    PURCHASE_ORDER = "purchase_order"


class FieldWithConfidence(BaseModel):
    value: Optional[str] = None
    confidence: float = Field(ge=0.0, le=1.0, default=0.0)


class InvoiceSchema(BaseModel):
    invoice_number: FieldWithConfidence
    vendor_name: FieldWithConfidence
    vendor_gst: FieldWithConfidence
    vendor_address: Optional[FieldWithConfidence] = None
    invoice_date: FieldWithConfidence
    due_date: FieldWithConfidence
    line_items: List[dict] = Field(default_factory=list)
    subtotal: FieldWithConfidence
    cgst_amount: FieldWithConfidence
    sgst_amount: FieldWithConfidence
    igst_amount: FieldWithConfidence
    total_amount: FieldWithConfidence
    bank_details: Optional[FieldWithConfidence] = None
    place_of_supply: Optional[FieldWithConfidence] = None


class GSTNoticeSchema(BaseModel):
    notice_type: FieldWithConfidence
    arn_number: Optional[FieldWithConfidence] = None
    tax_period: Optional[FieldWithConfidence] = None
    demand_amount: FieldWithConfidence
    interest_amount: Optional[FieldWithConfidence] = None
    penalty_amount: Optional[FieldWithConfidence] = None
    response_deadline: FieldWithConfidence
    issuing_office: Optional[FieldWithConfidence] = None
    assessment_year: Optional[FieldWithConfidence] = None
    gstin: Optional[FieldWithConfidence] = None


class ContractSchema(BaseModel):
    contract_number: FieldWithConfidence
    party_name: FieldWithConfidence
    contract_date: FieldWithConfidence
    effective_date: FieldWithConfidence
    expiry_date: Optional[FieldWithConfidence] = None
    contract_value: FieldWithConfidence
    payment_terms: Optional[FieldWithConfidence] = None
    termination_clause: Optional[FieldWithConfidence] = None
    governing_law: Optional[FieldWithConfidence] = None


class POSchema(BaseModel):
    po_number: FieldWithConfidence
    vendor_name: FieldWithConfidence
    po_date: FieldWithConfidence
    delivery_date: Optional[FieldWithConfidence] = None
    line_items: List[dict] = Field(default_factory=list)
    subtotal: FieldWithConfidence
    tax_amount: FieldWithConfidence
    total_amount: FieldWithConfidence
    shipping_address: Optional[FieldWithConfidence] = None
    terms: Optional[FieldWithConfidence] = None


class ExtractionResponse(BaseModel):
    doc_type: str
    raw_extraction: dict
    validated_data: Optional[dict] = None
    confidence: dict
    status: str
    needs_hitl: bool = False
    hitl_reason: Optional[str] = None

    class Config:
        from_attributes = True


class DocumentUploadRequest(BaseModel):
    workflow_type: str = "ingestion"


class DocumentUploadResponse(BaseModel):
    job_id: str
    status: str
    message: str


class JobStatusResponse(BaseModel):
    job_id: str
    status: str
    doc_type: Optional[str] = None
    confidence: Optional[dict] = None
    extracted_data: Optional[dict] = None
    needs_hitl: bool = False
    hitl_reason: Optional[str] = None
    created_at: datetime
    updated_at: datetime


class HITLReviewRequest(BaseModel):
    approved: bool
    notes: Optional[str] = None
    corrected_data: Optional[dict] = None


class HITLQueueResponse(BaseModel):
    id: str
    job_id: str
    queue_type: str
    payload: dict
    status: str
    assigned_to: Optional[str] = None
    created_at: datetime
    reviewed_at: Optional[datetime] = None
    notes: Optional[str] = None


DOC_TYPE_SCHEMAS = {
    "invoice": InvoiceSchema,
    "gst_notice": GSTNoticeSchema,
    "contract": ContractSchema,
    "purchase_order": POSchema,
}
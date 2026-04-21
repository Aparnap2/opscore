from datetime import timedelta
from typing import TypedDict
import logging

logger = logging.getLogger(__name__)

try:
    from temporalio import workflow
    from temporalio.common import RetryPolicy
    from datetime import timedelta

    TEMPORAL_AVAILABLE = True
except ImportError:
    TEMPORAL_AVAILABLE = False
    logger.warning("Temporal not available")


class DocumentIngestionInput(TypedDict):
    job_id: str
    tenant_id: str
    file_path: str
    file_content: bytes


@workflow.defn
class DocumentIngestionWorkflow:
    def __init__(self):
        self._approval_received = False
        self._reviewer_id = None

    @workflow.run
    async def run(self, input_data: DocumentIngestionInput) -> dict:
        if not TEMPORAL_AVAILABLE:
            return {"status": "error", "error": "Temporal not available"}

        extraction_result = await workflow.execute_activity(
            extract_document_activity,
            input_data,
            start_to_close_timeout=timedelta(minutes=5),
            retry_policy=RetryPolicy(
                maximum_attempts=3,
                initial_interval=timedelta(seconds=5),
                backoff_coefficient=2.0,
            ),
        )

        if extraction_result.get("needs_hitl"):
            await workflow.wait_condition(
                lambda: self._approval_received,
                timeout=timedelta(hours=24)
            )

            if not self._approval_received:
                return {"status": "rejected", "reviewer_id": self._reviewer_id}

        sync_result = await workflow.execute_activity(
            sync_to_erp_activity,
            {
                "extraction_result": extraction_result,
                "tenant_id": input_data["tenant_id"],
            },
            start_to_close_timeout=timedelta(minutes=2),
        )

        return {
            "status": "completed",
            "erp_id": sync_result.get("erp_id"),
            "reviewer_id": self._reviewer_id,
        }

    @workflow.signal
    def approval_signal(self, approved: bool, reviewer_id: str):
        self._approval_received = approved
        self._reviewer_id = reviewer_id


async def extract_document_activity(input_data: DocumentIngestionInput) -> dict:
    from apps.api.services.docling_extractor import extractor
    from apps.api.agents.extraction_agent import extraction_graph

    file_path = input_data["file_path"]

    raw_result = await extractor.extract(file_path)
    raw_text = raw_result.get("markdown", "")

    initial_state = {
        "job_id": input_data["job_id"],
        "tenant_id": input_data["tenant_id"],
        "raw_text": raw_text,
        "doc_type": "unknown",
        "extraction_attempt": 0,
        "extracted_data": None,
        "confidence_scores": {},
        "needs_hitl": False,
        "hitl_reason": None,
        "langfuse_trace_id": f"doc_{input_data['job_id']}",
    }

    try:
        result = await extraction_graph.ainvoke(initial_state)
        return result
    except Exception as e:
        logger.error(f"Extraction failed: {e}")
        return {"needs_hitl": True, "hitl_reason": f"Extraction error: {str(e)}"}


async def sync_to_erp_activity(input_data: dict) -> dict:
    from apps.api.integrations.quickbooks_mcp import quickbooks_mcp

    extraction_result = input_data.get("extraction_result", {})
    tenant_id = input_data.get("tenant_id")

    try:
        vendor_name = extraction_result.get("extracted_data", {}).get("vendor_name", {}).get("value", "")
        
        qb_vendor_id = await quickbooks_mcp.create_vendor({"name": vendor_name})

        if not qb_vendor_id:
            return {"erp_id": None, "error": "Failed to create vendor"}

        bill_id = await quickbooks_mcp.create_bill(
            extraction_result.get("extracted_data", {}),
            qb_vendor_id
        )

        return {"erp_id": bill_id or qb_vendor_id}

    except Exception as e:
        logger.error(f"ERP sync failed: {e}")
        return {"erp_id": None, "error": str(e)}
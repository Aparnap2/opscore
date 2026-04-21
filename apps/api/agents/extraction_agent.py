from typing import TypedDict, Annotated, Optional, Type
import operator
from langgraph.graph import StateGraph, END
from pydantic import BaseModel

from apps.api.agents.document_classifier import classify_document
from apps.api.services.llm_client import llm_client
from apps.api.api.v1.schemas.documents import DOC_TYPE_SCHEMAS


class ExtractionState(TypedDict):
    job_id: str
    tenant_id: str
    raw_text: str
    doc_type: str
    extraction_attempt: int
    extracted_data: Optional[dict]
    confidence_scores: dict
    needs_hitl: bool
    hitl_reason: Optional[str]
    langfuse_trace_id: str


async def node_classify(state: ExtractionState) -> ExtractionState:
    doc_type = classify_document(state["raw_text"])
    return {**state, "doc_type": doc_type}


async def node_extract(state: ExtractionState) -> ExtractionState:
    schema_class = DOC_TYPE_SCHEMAS.get(state["doc_type"])
    if not schema_class:
        return {**state, "needs_hitl": True, "hitl_reason": "Unknown document type"}

    result = await llm_client.structured_output(
        prompt=f"Extract {state['doc_type']} data from:\n{state['raw_text'][:3000]}",
        output_schema=schema_class,
        trace_id=state["langfuse_trace_id"]
    )

    extracted_data = result.model_dump() if hasattr(result, 'model_dump') else result.dict()
    confidence_scores = {}
    for key, value in extracted_data.items():
        if isinstance(value, dict) and "confidence" in value:
            confidence_scores[key] = value["confidence"]
        else:
            confidence_scores[key] = 1.0

    return {
        **state,
        "extracted_data": extracted_data,
        "confidence_scores": confidence_scores,
        "extraction_attempt": state.get("extraction_attempt", 0) + 1
    }


def validation_gate(state: ExtractionState) -> str:
    scores = state.get("confidence_scores", {})
    if not scores:
        return "route_to_hitl"

    avg_confidence = sum(scores.values()) / len(scores) if scores else 0.0
    extracted_data = state.get("extracted_data", {})

    if avg_confidence < 0.80:
        return "route_to_hitl"

    if extracted_data:
        total = extracted_data.get("total_amount", {})
        if isinstance(total, dict):
            try:
                amount = float(total.get("value", 0))
                if amount > 100000:
                    return "route_to_hitl"
            except (ValueError, TypeError):
                pass

    return "proceed_to_erp"


def node_hitl(state: ExtractionState) -> ExtractionState:
    scores = state.get("confidence_scores", {})
    avg_confidence = sum(scores.values()) / len(scores) if scores else 0.0

    reasons = []
    if avg_confidence < 0.80:
        reasons.append(f"Low confidence: {avg_confidence:.2f}")

    extracted_data = state.get("extracted_data", {})
    if extracted_data:
        total = extracted_data.get("total_amount", {})
        if isinstance(total, dict):
            try:
                amount = float(total.get("value", 0))
                if amount > 100000:
                    reasons.append(f"High value: ₹{amount:,.0f}")
            except (ValueError, TypeError):
                pass

    return {
        **state,
        "needs_hitl": True,
        "hitl_reason": "; ".join(reasons) if reasons else "Manual review required"
    }


async def node_erp_sync(state: ExtractionState) -> ExtractionState:
    return {**state, "needs_hitl": False, "hitl_reason": None}


def build_extraction_graph():
    workflow = StateGraph(ExtractionState)

    workflow.add_node("classify", node_classify)
    workflow.add_node("extract", node_extract)
    workflow.add_node("route_to_hitl", node_hitl)
    workflow.add_node("proceed_to_erp", node_erp_sync)

    workflow.set_entry_point("classify")
    workflow.add_edge("classify", "extract")
    workflow.add_conditional_edges("extract", validation_gate, {
        "route_to_hitl": "route_to_hitl",
        "proceed_to_erp": "proceed_to_erp"
    })
    workflow.add_edge("route_to_hitl", END)
    workflow.add_edge("proceed_to_erp", END)

    return workflow.compile()


extraction_graph = build_extraction_graph()
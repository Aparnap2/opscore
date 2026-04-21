from typing import TypedDict, Optional, List, Dict
import logging
from langgraph.graph import StateGraph, END
from pydantic import BaseModel
from enum import Enum

logger = logging.getLogger(__name__)


class GapSeverity(str, Enum):
    LOW = "LOW"
    MEDIUM = "MEDIUM"
    HIGH = "HIGH"
    CRITICAL = "CRITICAL"


class ComplianceGap(BaseModel):
    gap: str
    severity: GapSeverity
    regulation_section: str
    regulation_page: int
    required_action: str
    deadline: Optional[str] = None
    needs_review: bool = False


class ComplianceState(TypedDict):
    regulation_id: str
    tenant_id: str
    regulation_summary: str
    regulation_chunks: List[dict]
    company_policy_chunks: List[dict]
    gaps: List[Dict]
    citations: List[Dict]
    ragas_score: Optional[float]
    langfuse_trace_id: str


class ComplianceAgent:
    def __init__(self, qdrant_client=None, llm_client=None):
        self.qdrant_client = qdrant_client
        self.llm_client = llm_client

    async def hybrid_search(self, query: str, tenant_id: str, limit: int = 20) -> List[Dict]:
        if not self.qdrant_client:
            return []

        try:
            results = await self.qdrant_client.search_hybrid(
                collection=f"compliance_{tenant_id}",
                query_text=query,
                limit=limit,
                with_payload=True,
            )
            return [
                {
                    "text": r.get("text", ""),
                    "page": r.get("page", 0),
                    "section": r.get("section", ""),
                    "source": r.get("source", ""),
                }
                for r in results
            ]
        except Exception as e:
            logger.error(f"Hybrid search failed: {e}")
            return []

    async def analyze_gaps(
        self, regulation_chunks: List[Dict], policy_chunks: List[Dict]
    ) -> List[Dict]:
        if not self.llm_client:
            return []

        regulation_text = "\n\n---\n\n".join(
            f"[Page {c.get('page', '?')}] {c.get('text', '')}"
            for c in regulation_chunks[:10]
        )

        policy_text = "\n\n---\n\n".join(
            f"[Page {c.get('page', '?')}] {c.get('text', '')}" for c in policy_chunks[:10]
        )

        system = """You are a compliance expert. Analyze new regulatory requirements against company policy to identify gaps. 
For each gap provide JSON: {"gap": "...", "severity": "LOW|MEDIUM|HIGH|CRITICAL", "regulation_section": "...", "regulation_page": N, "required_action": "...", "deadline": "..."}"""

        user = f"""New Regulation:
{regulation_text}

Company Current Policy:
{policy_text}

Identify gaps. Return JSON array."""

        try:
            response = await self.llm_client.chat(
                system=system,
                user=user,
                response_format="json_array",
            )

            import json
            gaps = json.loads(response)
            return gaps if isinstance(gaps, list) else []

        except Exception as e:
            logger.error(f"Gap analysis failed: {e}")
            return []

    async def evaluate_faithfulness(
        self, gaps: List[Dict], regulation_chunks: List[Dict]
    ) -> float:
        try:
            from ragas import evaluate, EvaluationDataset
            from ragas.metrics import Faithfulness

            if not gaps:
                return 1.0

            eval_samples = [
                {
                    "user_input": g.get("gap", ""),
                    "response": g.get("required_action", ""),
                    "retrieved_contexts": [
                        c.get("text", "")
                        for c in regulation_chunks
                        if c.get("page") == g.get("regulation_page")
                    ],
                }
                for g in gaps
                if g.get("regulation_page")
            ]

            if not eval_samples:
                return 1.0

            dataset = EvaluationDataset.from_list(eval_samples)
            result = evaluate(dataset=dataset, metrics=[Faithfulness()])
            return result.get("faithfulness", 1.0)

        except Exception as e:
            logger.warning(f"RAGAS evaluation failed: {e}")
            return 1.0

    async def run_analysis(
        self,
        regulation_id: str,
        tenant_id: str,
        regulation_summary: str,
        regulation_chunks: List[Dict],
        langfuse_trace_id: str,
    ) -> Dict:
        policy_chunks = await self.hybrid_search(regulation_summary, tenant_id)

        gaps = await self.analyze_gaps(regulation_chunks, policy_chunks)

        ragas_score = await self.evaluate_faithfulness(gaps, regulation_chunks)

        for gap in gaps:
            gap["needs_review"] = ragas_score < 0.85

        citations = [
            {
                "page": g.get("regulation_page"),
                "section": g.get("regulation_section"),
            }
            for g in gaps
            if g.get("regulation_page")
        ]

        return {
            "gaps": gaps,
            "citations": citations,
            "ragas_score": ragas_score,
            "policy_chunks_found": len(policy_chunks),
        }


compliance_agent = ComplianceAgent()
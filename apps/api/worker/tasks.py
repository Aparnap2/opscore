"""ARQ Task Functions for OpsCore."""
import logging
from typing import Any, Dict, Optional

from arq import ctx

logger = logging.getLogger(__name__)


async def process_document_job(ctx: Dict[str, Any], job_id: str, tenant_id: str, file_path: str) -> Dict[str, Any]:
    """Process document ingestion job."""
    try:
        logger.info(f"Processing document job {job_id} for tenant {tenant_id}: {file_path}")

        from apps.api.services.docling_extractor import extractor
        from apps.api.agents.extraction_agent import extraction_graph

        raw_result = await extractor.extract(file_path)
        raw_text = raw_result.get("markdown", "")

        initial_state = {
            "job_id": job_id,
            "tenant_id": tenant_id,
            "raw_text": raw_text,
            "doc_type": "unknown",
            "extraction_attempt": 0,
            "extracted_data": None,
            "confidence_scores": {},
            "needs_hitl": False,
            "hitl_reason": None,
            "langfuse_trace_id": f"doc_{job_id}",
        }

        try:
            result = await extraction_graph.ainvoke(initial_state)

            if result.get("needs_hitl"):
                return {
                    "status": "pending_review",
                    "job_id": job_id,
                    "tenant_id": tenant_id,
                    "hitl_reason": result.get("hitl_reason"),
                    "data": result.get("extracted_data"),
                }

            from apps.api.integrations.quickbooks_mcp import quickbooks_mcp

            vendor_name = result.get("extracted_data", {}).get("vendor_name", {}).get("value", "")
            qb_vendor_id = None

            if vendor_name:
                try:
                    qb_vendor_id = await quickbooks_mcp.create_vendor({"name": vendor_name})
                except Exception as qb_error:
                    logger.warning(f"QB vendor creation failed: {qb_error}")

            bill_id = None
            if qb_vendor_id:
                try:
                    bill_id = await quickbooks_mcp.create_bill(
                        result.get("extracted_data", {}),
                        qb_vendor_id
                    )
                except Exception as qb_error:
                    logger.warning(f"QB bill creation failed: {qb_error}")

            return {
                "status": "completed",
                "job_id": job_id,
                "tenant_id": tenant_id,
                "erp_id": bill_id or qb_vendor_id,
                "extracted_data": result.get("extracted_data"),
            }

        except Exception as e:
            logger.error(f"Extraction failed for job {job_id}: {e}")
            return {
                "status": "error",
                "job_id": job_id,
                "tenant_id": tenant_id,
                "error": str(e),
            }

    except Exception as e:
        logger.error(f"Document job {job_id} failed: {e}")
        return {
            "status": "error",
            "job_id": job_id,
            "tenant_id": tenant_id,
            "error": str(e),
        }


async def run_compliance_scrape(ctx: Dict[str, Any], source: str, tenant_id: str) -> Dict[str, Any]:
    """Run compliance monitoring scrape."""
    try:
        logger.info(f"Running compliance scrape for source: {source}, tenant: {tenant_id}")

        from apps.api.services.compliance_scraper import scraper
        from apps.api.services.graphiti_service import graphiti_service

        results = await scraper.scrape_regulatory_updates(source)

        if results and graphiti_service:
            try:
                for item in results:
                    await graphiti_service.add_entity(
                        entity_type="regulatory_update",
                        name=item.get("title", ""),
                        properties={
                            "source": item.get("source"),
                            "url": item.get("url"),
                            "scraped_at": item.get("scraped_at"),
                        },
                        tenant_id=tenant_id,
                    )
            except Exception as graphiti_error:
                logger.warning(f"Graphiti sync failed: {graphiti_error}")

        return {
            "status": "completed",
            "source": source,
            "tenant_id": tenant_id,
            "items_found": len(results),
            "results": results,
        }

    except Exception as e:
        logger.error(f"Compliance scrape failed for {source}: {e}")
        return {
            "status": "error",
            "source": source,
            "tenant_id": tenant_id,
            "error": str(e),
        }


async def run_vendor_risk_assessment(
    ctx: Dict[str, Any],
    vendor_id: str,
    tenant_id: str,
    vendor_data: Dict[str, Any],
) -> Dict[str, Any]:
    """Run vendor risk assessment."""
    try:
        logger.info(f"Running vendor risk assessment for vendor {vendor_id}, tenant {tenant_id}")

        from apps.api.services.risk_scorer import compute_vendor_risk
        from apps.api.services.graphiti_service import graphiti_service

        blacklist: list[str] = []
        existing_vendors: list[dict] = []

        risk_result = await compute_vendor_risk(
            vendor=vendor_data,
            existing_vendors=existing_vendors,
            blacklist=blacklist,
            tenant_id=tenant_id,
            graphiti_service=graphiti_service,
        )

        return {
            "status": "completed",
            "vendor_id": vendor_id,
            "tenant_id": tenant_id,
            "risk_score": risk_result.score,
            "risk_tier": risk_result.tier,
            "risk_flags": risk_result.flags,
        }

    except Exception as e:
        logger.error(f"Vendor risk assessment failed for {vendor_id}: {e}")
        return {
            "status": "error",
            "vendor_id": vendor_id,
            "tenant_id": tenant_id,
            "error": str(e),
        }


async def sync_trust_battery(ctx: Dict[str, Any], vendor_id: str, action: str) -> Dict[str, Any]:
    """Sync trust battery maintenance."""
    try:
        logger.info(f"Syncing trust battery for vendor {vendor_id}, action: {action}")

        from apps.api.services.trust_battery import TrustBattery, TrustTier
        from apps.api.db.session import AsyncSessionLocal

        async with AsyncSessionLocal() as db:
            from sqlalchemy import select
            from sqlalchemy.dialects.postgresql import JSONB

            stmt = select("trust_battery_data").select_from("vendors").where("id = :vendor_id")
            result = await db.execute(stmt, {"vendor_id": vendor_id})
            row = result.scalar_one_or_none()

            battery_data = row if row else {}
            battery = TrustBattery.from_dict(battery_data) if battery_data else TrustBattery()

            if action == "success":
                battery.record_success()
            elif action == "error":
                battery.record_error()
            elif action == "fraud":
                battery.flag_fraud()
            elif action == "advance_days":
                battery.advance_days(1)

            update_stmt = (
                "UPDATE vendors SET trust_battery_data = :data, updated_at = NOW() WHERE id = :vendor_id"
            )
            await db.execute(
                update_stmt,
                {"data": battery.to_dict(), "vendor_id": vendor_id},
            )
            await db.commit()

            return {
                "status": "completed",
                "vendor_id": vendor_id,
                "action": action,
                "trust_battery": battery.to_dict(),
            }

    except Exception as e:
        logger.error(f"Trust battery sync failed for {vendor_id}: {e}")
        return {
            "status": "error",
            "vendor_id": vendor_id,
            "action": action,
            "error": str(e),
        }

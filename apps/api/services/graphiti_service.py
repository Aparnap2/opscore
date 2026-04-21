from typing import Optional, List, Dict
import logging

logger = logging.getLogger(__name__)

try:
    from graphiti_core import Graphiti
    from graphiti_core.nodes import EpisodeType

    GRAPHITI_AVAILABLE = True
except ImportError:
    GRAPHITI_AVAILABLE = False
    logger.warning("Graphiti not available")


class GraphitiService:
    def __init__(self, neo4j_uri: str, neo4j_user: str, neo4j_password: str):
        if not GRAPHITI_AVAILABLE:
            self.client = None
            return

        try:
            self.client = Graphiti(
                neo4j_uri=neo4j_uri,
                neo4j_user=neo4j_user,
                neo4j_password=neo4j_password,
            )
        except Exception as e:
            logger.error(f"Failed to initialize Graphiti: {e}")
            self.client = None

    async def ingest_vendor_entity(self, vendor: dict, tenant_id: str) -> bool:
        if not self.client:
            return False

        try:
            await self.client.add_episode(
                name=f"vendor_onboard_{vendor.get('id', 'unknown')}",
                episode_body=f"""
                    Vendor: {vendor.get('name')}
                    GST: {vendor.get('gst_number')}
                    PAN: {vendor.get('pan_number')}
                    Category: {vendor.get('category')}
                    Bank IFSC: {vendor.get('ifsc_code')}
                    Risk Score: {vendor.get('risk_score', 0)}
                    Onboarded: {vendor.get('created_at')}
                """,
                source=EpisodeType.text,
                source_description="vendor_onboarding",
                group_id=tenant_id,
            )
            return True
        except Exception as e:
            logger.error(f"Failed to ingest vendor entity: {e}")
            return False

    async def find_similar_vendors(
        self, vendor_name: str, tenant_id: str, num_results: int = 5
    ) -> List[Dict]:
        if not self.client:
            return []

        try:
            results = await self.client.search(
                query=f"vendor named {vendor_name}",
                group_ids=[tenant_id],
                num_results=num_results,
            )
            return [
                {
                    "name": r.get("name"),
                    "entity_id": r.get("entity_id"),
                    "score": r.get("score", 0),
                }
                for r in results
            ]
        except Exception as e:
            logger.error(f"Failed to find similar vendors: {e}")
            return []

    async def find_risky_connections(
        self, gst_number: str, tenant_id: str
    ) -> List[Dict]:
        if not self.client:
            return []

        try:
            results = await self.client.search(
                query=f"risky vendor connected to {gst_number}",
                group_ids=[tenant_id],
                num_results=10,
            )
            return [
                {
                    "name": r.get("name"),
                    "entity_id": r.get("entity_id"),
                    "risk_level": r.get("risk_level", "unknown"),
                }
                for r in results
            ]
        except Exception as e:
            logger.error(f"Failed to find risky connections: {e}")
            return []

    async def add_compliance_entity(
        self, regulation: dict, tenant_id: str
    ) -> bool:
        if not self.client:
            return False

        try:
            await self.client.add_episode(
                name=f"regulation_{regulation.get('id', 'unknown')}",
                episode_body=f"""
                    Regulation Source: {regulation.get('source')}
                    Title: {regulation.get('title')}
                    URL: {regulation.get('url')}
                    Summary: {regulation.get('summary', '')}
                """,
                source=EpisodeType.text,
                source_description="compliance_regulation",
                group_id=tenant_id,
            )
            return True
        except Exception as e:
            logger.error(f"Failed to add compliance entity: {e}")
            return False


from apps.api.config import settings

graphiti_service = GraphitiService(
    neo4j_uri=settings.NEO4J_URI,
    neo4j_user=settings.NEO4J_USER,
    neo4j_password=settings.NEO4J_PASSWORD,
)
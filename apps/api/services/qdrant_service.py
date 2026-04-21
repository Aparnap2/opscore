from typing import List, Dict, Optional
import logging

logger = logging.getLogger(__name__)

try:
    from qdrant_client import QdrantClient
    from qdrant_client.models import Distance, VectorParams, Filter, FieldCondition, MatchText

    QDRANT_AVAILABLE = True
except ImportError:
    QDRANT_AVAILABLE = False
    logger.warning("Qdrant not available")


class QdrantService:
    def __init__(self, url: str, collection_prefix: str = "opscore_"):
        if not QDRANT_AVAILABLE:
            self.client = None
            return

        try:
            self.client = QdrantClient(url=url)
        except Exception as e:
            logger.error(f"Failed to connect to Qdrant: {e}")
            self.client = None

        self.collection_prefix = collection_prefix

    async def create_collection(
        self, collection_name: str, vector_size: int = 1536
    ) -> bool:
        if not self.client:
            return False

        full_name = f"{self.collection_prefix}{collection_name}"
        try:
            self.client.create_collection(
                collection_name=full_name,
                vectors_config=VectorParams(size=vector_size, distance=Distance.COSINE),
            )
            return True
        except Exception as e:
            logger.error(f"Failed to create collection {full_name}: {e}")
            return False

    async def upsert_documents(
        self,
        collection_name: str,
        documents: List[Dict],
        vectors: List[List[float]],
    ) -> bool:
        if not self.client:
            return False

        full_name = f"{self.collection_prefix}{collection_name}"

        try:
            from qdrant_client.models import PointStruct

            points = [
                PointStruct(
                    id=doc.get("id", i),
                    vector=vectors[i],
                    payload=doc,
                )
                for i, doc in enumerate(documents)
            ]

            self.client.upsert(collection_name=full_name, points=points)
            return True
        except Exception as e:
            logger.error(f"Failed to upsert documents: {e}")
            return False

    async def search_hybrid(
        self,
        collection_name: str,
        query_text: str,
        query_vector: Optional[List[float]] = None,
        limit: int = 10,
        with_payload: bool = True,
    ) -> List[Dict]:
        if not self.client:
            return []

        full_name = f"{self.collection_prefix}{collection_name}"

        try:
            if query_vector:
                results = self.client.search(
                    collection_name=full_name,
                    query_vector=query_vector,
                    limit=limit,
                    with_payload=with_payload,
                )
            else:
                results = self.client.scroll(
                    collection_name=full_name,
                    limit=limit,
                    with_payload=with_payload,
                )[0]

            return [
                {
                    "id": r.id,
                    "score": r.score,
                    **r.payload,
                }
                for r in results
            ]
        except Exception as e:
            logger.error(f"Search failed: {e}")
            return []

    async def delete_collection(self, collection_name: str) -> bool:
        if not self.client:
            return False

        full_name = f"{self.collection_prefix}{collection_name}"
        try:
            self.client.delete_collection(collection_name=full_name)
            return True
        except Exception as e:
            logger.error(f"Failed to delete collection: {e}")
            return False


from apps.api.config import settings

qdrant_service = QdrantService(
    url=settings.QDRANT_URL,
    collection_prefix=settings.QDRANT_COLLECTION_PREFIX,
)
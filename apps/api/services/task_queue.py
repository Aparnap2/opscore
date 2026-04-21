"""ARQ Client for OpsCore - Task Queue Service."""
import asyncio
import logging
from typing import Any, Dict, Optional

from arq.connections import RedisSettings
from arq.constants import default_queue_name

from apps.api.config import settings

logger = logging.getLogger(__name__)


def get_redis_settings() -> RedisSettings:
    """Parse Redis URL to RedisSettings."""
    redis_url = settings.REDIS_URL
    host = redis_url.split(":")[1].replace("//", "") if ":" in redis_url else "localhost"
    port = int(redis_url.split(":")[-1].split("/")[0]) if ":" in redis_url else 6379
    db = int(redis_url.split("/")[-1]) if "/" in redis_url else 0
    return RedisSettings(host=host, port=port, database=db)


class TaskQueueService:
    """ARQ-based task queue service for OpsCore."""

    def __init__(self):
        self._redis_settings: Optional[RedisSettings] = None
        self._job_cache: Dict[str, Dict[str, Any]] = {}

    @property
    def redis_settings(self) -> RedisSettings:
        if self._redis_settings is None:
            self._redis_settings = get_redis_settings()
        return self._redis_settings

    async def _get_pool(self):
        """Get or create Redis connection pool."""
        if not hasattr(self, "_pool"):
            from arq.connections import RedisPool
            self._pool = RedisPool(self.redis_settings)
            await self._pool.connect()
        return self._pool

    async def enqueue_document_job(
        self,
        job_id: str,
        tenant_id: str,
        file_path: str,
    ) -> str:
        """Enqueue a document ingestion job."""
        try:
            from apps.api.worker.tasks import process_document_job

            pool = await self._get_pool()
            job = await pool.enqueue_job(
                "process_document_job",
                job_id,
                tenant_id,
                file_path,
                _queue=default_queue_name,
            )
            logger.info(f"Enqueued document job {job_id}: {job.job_id}")
            return job.job_id

        except Exception as e:
            logger.error(f"Failed to enqueue document job {job_id}: {e}")
            raise

    async def enqueue_compliance_scrape(
        self,
        source: str,
        tenant_id: str,
    ) -> str:
        """Enqueue a compliance scrape job."""
        try:
            from apps.api.worker.tasks import run_compliance_scrape

            pool = await self._get_pool()
            job = await pool.enqueue_job(
                "run_compliance_scrape",
                source,
                tenant_id,
                _queue=default_queue_name,
            )
            logger.info(f"Enqueued compliance scrape for {source}: {job.job_id}")
            return job.job_id

        except Exception as e:
            logger.error(f"Failed to enqueue compliance scrape: {e}")
            raise

    async def enqueue_vendor_risk_assessment(
        self,
        vendor_id: str,
        tenant_id: str,
        vendor_data: Dict[str, Any],
    ) -> str:
        """Enqueue a vendor risk assessment job."""
        try:
            from apps.api.worker.tasks import run_vendor_risk_assessment

            pool = await self._get_pool()
            job = await pool.enqueue_job(
                "run_vendor_risk_assessment",
                vendor_id,
                tenant_id,
                vendor_data,
                _queue=default_queue_name,
            )
            logger.info(f"Enqueued vendor risk assessment for {vendor_id}: {job.job_id}")
            return job.job_id

        except Exception as e:
            logger.error(f"Failed to enqueue vendor risk assessment: {e}")
            raise

    async def get_job_status(self, job_id: str) -> Dict[str, Any]:
        """Get job status and result."""
        try:
            pool = await self._get_pool()
            job = await pool.job(job_id)

            if job is None:
                return {"status": "not_found", "job_id": job_id}

            status = job.status
            result = None

            if status == "complete":
                try:
                    result = await job.result()
                except Exception:
                    pass

            return {
                "status": status,
                "job_id": job_id,
                "result": result,
                "enqueue_time": job.enqueue_time.isoformat() if job.enqueue_time else None,
                "start_time": job.start_time.isoformat() if job.start_time else None,
                "finish_time": job.finish_time.isoformat() if job.finish_time else None,
            }

        except Exception as e:
            logger.error(f"Failed to get job status for {job_id}: {e}")
            return {"status": "error", "job_id": job_id, "error": str(e)}

    async def close(self):
        """Close the Redis pool."""
        if hasattr(self, "_pool"):
            await self._pool.disconnect()
            delattr(self, "_pool")


task_queue = TaskQueueService()

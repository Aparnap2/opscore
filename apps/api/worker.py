"""ARQ Worker for OpsCore - replaces Temporal for local-first deployment."""
import asyncio
import logging
from arq import Worker, run_worker
from arq.connections import RedisSettings
from apps.api.config import settings

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
)
logger = logging.getLogger(__name__)


class WorkerSettings:
    functions = []
    redis_settings = RedisSettings(
        host=settings.REDIS_URL.split(":")[1].replace("//", ""),
        port=int(settings.REDIS_URL.split(":")[-1].split("/")[0])
        if ":"
        in settings.REDIS_URL
        else 6379,
        database=int(settings.REDIS_URL.split("/")[-1]) if "/" in settings.REDIS_URL else 0,
    )
    max_jobs = 10
    keep_job_result = 3600 * 24
    job_timeout = 300


async def main():
    from apps.api.worker.tasks import (
        process_document_job,
        run_compliance_scrape,
        run_vendor_risk_assessment,
        sync_trust_battery,
    )

    logger.info("Starting ARQ Worker...")

    WorkerSettings.functions = [
        process_document_job,
        run_compliance_scrape,
        run_vendor_risk_assessment,
        sync_trust_battery,
    ]

    worker = Worker(
        functions=WorkerSettings.functions,
        redis_settings=WorkerSettings.redis_settings,
        max_jobs=WorkerSettings.max_jobs,
        keep_job_result=WorkerSettings.keep_job_result,
        job_timeout=WorkerSettings.job_timeout,
    )

    await worker.run()


if __name__ == "__main__":
    asyncio.run(main())
from typing import Optional, Any, Callable
from functools import wraps
import logging
import os

logger = logging.getLogger(__name__)

try:
    from langfuse import Langfuse

    LANGFUSE_AVAILABLE = True
except ImportError:
    LANGFUSE_AVAILABLE = False
    logger.warning("Langfuse not available")


class LangfuseClient:
    def __init__(self, public_key: str, secret_key: str, host: str):
        if not LANGFUSE_AVAILABLE or not public_key or not secret_key:
            self.client = None
            return

        try:
            self.client = Langfuse(
                public_key=public_key,
                secret_key=secret_key,
                host=host,
            )
        except Exception as e:
            logger.error(f"Failed to initialize Langfuse: {e}")
            self.client = None

    def trace(self, name: str, metadata: dict = None):
        if not self.client:
            return None
        return self.client.trace(name=name, metadata=metadata or {})

    def generation(
        self,
        trace_id: str,
        model: str,
        usage: dict,
        metadata: dict = None,
    ):
        if not self.client:
            return None

        try:
            return self.client.generation(
                trace_id=trace_id,
                model=model,
                usage=usage,
                metadata=metadata or {},
            )
        except Exception as e:
            logger.error(f"Failed to log generation: {e}")
            return None

    def get_trace_url(self, trace_id: str) -> Optional[str]:
        if not self.client:
            return None
        try:
            return f"{self.client.host}/trace/{trace_id}"
        except Exception:
            return None


def trace_llm_call(workflow: str, step: str):
    def decorator(func: Callable):
        @wraps(func)
        async def wrapper(*args, **kwargs):
            trace_id = kwargs.get("trace_id")
            if not trace_id:
                return await func(*args, **kwargs)

            langfuse_client = get_langfuse_client()
            if not langfuse_client.client:
                return await func(*args, **kwargs)

            trace = langfuse_client.trace(
                name=f"{workflow}:{step}",
                metadata={"tenant_id": kwargs.get("tenant_id")}
            )

            span = trace.span(name=step) if trace else None

            try:
                result = await func(*args, **kwargs)
                if span:
                    span.end(
                        output=str(result)[:500],
                        level="DEFAULT",
                        status_message="success"
                    )
                return result
            except Exception as e:
                if span:
                    span.end(level="ERROR", status_message=str(e))
                raise

        return wrapper
    return decorator


from apps.api.config import settings

_langfuse_client = None


def get_langfuse_client() -> LangfuseClient:
    global _langfuse_client
    if _langfuse_client is None:
        _langfuse_client = LangfuseClient(
            public_key=settings.LANGFUSE_PUBLIC_KEY,
            secret_key=settings.LANGFUSE_SECRET_KEY,
            host=settings.LANGFUSE_HOST,
        )
    return _langfuse_client
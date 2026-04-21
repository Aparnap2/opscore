from pydantic_settings import BaseSettings
from typing import Optional


class Settings(BaseSettings):
    APP_NAME: str = "OpsCore"
    APP_VERSION: str = "0.1.0"

    DATABASE_URL: str = "postgresql+asyncpg://opscore:opscore@localhost:5432/opscore"

    REDIS_URL: str = "redis://localhost:6379/0"

    QDRANT_URL: str = "http://localhost:6333"
    QDRANT_COLLECTION_PREFIX: str = "opscore_"

    NEO4J_URI: str = "bolt://localhost:7687"
    NEO4J_USER: str = "neo4j"
    NEO4J_PASSWORD: str = "neo4j"

    TEMPORAL_ADDRESS: str = "localhost:7233"
    TEMPORAL_NAMESPACE: str = "default"

    MINIO_ENDPOINT: str = "localhost:9000"
    MINIO_ACCESS_KEY: str = "minio"
    MINIO_SECRET_KEY: str = "minio"
    MINIO_BUCKET: str = "opscore-documents"

    LANGFUSE_PUBLIC_KEY: str = ""
    LANGFUSE_SECRET_KEY: str = ""
    LANGFUSE_HOST: str = "http://localhost:3000"

    SLACK_BOT_TOKEN: str = ""
    SLACK_SIGNING_SECRET: str = ""

    JWT_SECRET: str = "change-me-in-production"
    JWT_ALGORITHM: str = "HS256"
    JWT_ACCESS_TOKEN_EXPIRE_MINUTES: int = 15
    JWT_REFRESH_TOKEN_EXPIRE_DAYS: int = 7

    LLM_MODEL: str = "gpt-4o-mini"
    LLM_API_KEY: str = ""
    LLM_BASE_URL: str = "https://api.openai.com/v1"

    QUICKBOOKS_MCP_URL: str = ""
    QUICKBOOKS_CLIENT_ID: str = ""
    QUICKBOOKS_CLIENT_SECRET: str = ""
    QUICKBOOKS_REALM_ID: str = ""

    GRAFANA_URL: str = "http://localhost:3001"
    GRAFANA_API_KEY: str = ""

    RATE_LIMIT_PER_MINUTE: int = 100

    CRAWL4AI_MAX_DEPTH: int = 3
    CRAWL4AI_CONCURRENT_TASKS: int = 5

    DOCLING_CACHE_DIR: str = "/tmp/docling"

    class Config:
        env_file = ".env"
        case_sensitive = True


settings = Settings()

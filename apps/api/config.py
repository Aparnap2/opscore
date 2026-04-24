from pydantic_settings import BaseSettings, SettingsConfigDict
from typing import Optional


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_file=".env",
        case_sensitive=True,
        extra="ignore"
    )

    APP_NAME: str = "OpsCore"
    APP_VERSION: str = "0.1.0"

    DATABASE_URL: str = "postgresql+asyncpg://admin:password@localhost:5433/opscore"

    REDIS_URL: str = "redis://localhost:6380/0"

    NEO4J_URI: str = "bolt://localhost:7688"
    NEO4J_USER: str = "neo4j"
    NEO4J_PASSWORD: str = "password"

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

    QB_BASE_URL: str = "http://localhost:3001/mock/qb"
    SLACK_BASE_URL: str = "http://localhost:3001/mock/slack"
    LINEAR_BASE_URL: str = "http://localhost:3001/mock/linear"

    RATE_LIMIT_PER_MINUTE: int = 100

    CRAWL4AI_MAX_DEPTH: int = 3
    CRAWL4AI_CONCURRENT_TASKS: int = 5

    DOCLING_CACHE_DIR: str = "/tmp/docling"


settings = Settings()

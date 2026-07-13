import os

OPSCORE_API_URL = os.getenv("OPSCORE_API_URL", "http://localhost:8080")
OPSCORE_TENANT_ID = os.getenv("OPSCORE_TENANT_ID", "default")
LANGFUSE_PROJECT_URL = os.getenv("LANGFUSE_PROJECT_URL", "https://cloud.langfuse.com")

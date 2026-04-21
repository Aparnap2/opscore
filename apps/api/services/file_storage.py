"""Local file storage - replaces MinIO for portfolio deployment.
Stores PDFs in ./storage/tenants/{tenant_id}/ directory.
"""
from pathlib import Path
from typing import Optional
import logging
from uuid import uuid4

logger = logging.getLogger(__name__)

STORAGE_ROOT = Path("./storage")


class FileStorage:
    def __init__(self, storage_root: str = "./storage"):
        self.storage_root = Path(storage_root)
        self.storage_root.mkdir(parents=True, exist_ok=True)

    def _get_tenant_path(self, tenant_id: str) -> Path:
        path = self.storage_root / "tenants" / tenant_id
        path.mkdir(parents=True, exist_ok=True)
        return path

    async def upload(
        self,
        tenant_id: str,
        file_bytes: bytes,
        filename: str,
        workflow_type: str = "documents",
    ) -> str:
        tenant_path = self._get_tenant_path(tenant_id)
        workflow_path = tenant_path / workflow_type
        workflow_path.mkdir(parents=True, exist_ok=True)

        file_id = f"{uuid4()}{Path(filename).suffix}"
        file_path = workflow_path / file_id

        with open(file_path, "wb") as f:
            f.write(file_bytes)

        key = f"{tenant_id}/{workflow_type}/{file_id}"
        logger.info(f"Uploaded file: {key}")
        return key

    async def download(self, storage_key: str) -> Optional[bytes]:
        file_path = self.storage_root / "tenants" / storage_key
        if not file_path.exists():
            return None

        with open(file_path, "rb") as f:
            return f.read()

    async def delete(self, storage_key: str) -> bool:
        file_path = self.storage_root / "tenants" / storage_key
        if not file_path.exists():
            return False

        file_path.unlink()
        return True

    def get_path(self, storage_key: str) -> str:
        return str(self.storage_root / "tenants" / storage_key)


file_storage = FileStorage()

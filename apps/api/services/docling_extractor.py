import asyncio
import hashlib
from typing import Optional
from pathlib import Path
import logging

logger = logging.getLogger(__name__)

try:
    from docling.document_converter import DocumentConverter, InputFormat, PdfFormatOption
    from docling.datamodel.pipeline_options import PdfPipelineOptions, RapidOcrOptions
    from docling.backend.pytorch_backend import PyTorchDocumentBackend

    DOCLING_AVAILABLE = True
except ImportError:
    DOCLING_AVAILABLE = False
    logger.warning("Docling not available, using fallback OCR")


class DoclingExtractor:
    def __init__(self):
        if DOCLING_AVAILABLE:
            pipeline_opts = PdfPipelineOptions(do_ocr=True)
            self.converter = DocumentConverter(
                format_options={
                    InputFormat.PDF: PdfFormatOption(
                        pipeline_options=pipeline_opts
                    )
                }
            )
        else:
            self.converter = None

    async def extract(self, file_path: str) -> dict:
        if not DOCLING_AVAILABLE:
            return await self._fallback_extract(file_path)

        try:
            result = await asyncio.to_thread(
                self.converter.convert,
                source=file_path
            )
            doc = result.document
            return {
                "markdown": doc.export_to_markdown(),
                "tables": [t.export_to_dataframe().to_dict() for t in doc.tables] if doc.tables else [],
                "text_blocks": [b.text for b in doc.texts] if doc.texts else [],
                "metadata": {
                    "page_count": len(doc.pages),
                    "has_tables": len(doc.tables) > 0,
                    "title": doc.metadata.get("title") if doc.metadata else None,
                }
            }
        except Exception as e:
            logger.error(f"Docling extraction failed: {e}")
            return await self._fallback_extract(file_path)

    async def _fallback_extract(self, file_path: str) -> dict:
        try:
            from rapidocr_onnxruntime import RapidOCR

            ocr_engine = RapidOCR()
            result, elapsed = ocr_engine(file_path)

            text_blocks = []
            tables = []

            if result:
                for line in result:
                    text_blocks.append(line[1])

            return {
                "markdown": "\n".join(text_blocks),
                "tables": tables,
                "text_blocks": text_blocks,
                "metadata": {
                    "page_count": 1,
                    "has_tables": False,
                    "ocr_fallback": True,
                }
            }
        except Exception as e:
            logger.error(f"Fallback OCR also failed: {e}")
            return {
                "markdown": "",
                "tables": [],
                "text_blocks": [],
                "metadata": {"error": str(e), "page_count": 0, "has_tables": False}
            }

    async def extract_from_bytes(self, file_bytes: bytes, filename: str) -> dict:
        import tempfile

        with tempfile.NamedTemporaryFile(delete=False, suffix=Path(filename).suffix) as tmp:
            tmp.write(file_bytes)
            tmp_path = tmp.name

        try:
            return await self.extract(tmp_path)
        finally:
            Path(tmp_path).unlink(missing_ok=True)

    def compute_file_hash(self, file_path: str) -> str:
        h = hashlib.sha256()
        with open(file_path, "rb") as f:
            for chunk in iter(lambda: f.read(8192), b""):
                h.update(chunk)
        return h.hexdigest()


extractor = DoclingExtractor()

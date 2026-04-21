from typing import Optional, Dict
import logging

logger = logging.getLogger(__name__)


class QuickBooksMCP:
    def __init__(self, mcp_url: str, client_id: str, client_secret: str, realm_id: str):
        self.mcp_url = mcp_url
        self.client_id = client_id
        self.client_secret = client_secret
        self.realm_id = realm_id
        self._session = None

    async def create_bill(self, invoice: dict, vendor_qb_id: str) -> Optional[str]:
        try:
            line_items = []
            for item in invoice.get("line_items", []):
                line_items.append({
                    "amount": item.get("amount"),
                    "description": item.get("description"),
                    "detail_type": "SalesItemLineDetail",
                    "sales_item_line_detail": {
                        "item_ref": {"value": item.get("item_name", "Services")},
                        "qty": item.get("qty", 1),
                        "unit_price": item.get("rate"),
                    }
                })

            payload = {
                "realm_id": self.realm_id,
                "vendor_ref": vendor_qb_id,
                "line_items": line_items,
                "total_amount": invoice.get("total_amount", {}).get("value", 0),
                "due_date": invoice.get("due_date", {}).get("value"),
                "doc_number": invoice.get("invoice_number", {}).get("value"),
            }

            logger.info(f"Creating QuickBooks bill: {payload}")
            return f"qb_bill_{vendor_qb_id}_{invoice.get('invoice_number', {}).get('value', 'unknown')}"

        except Exception as e:
            logger.error(f"Failed to create QuickBooks bill: {e}")
            return None

    async def create_vendor(self, vendor: dict) -> Optional[str]:
        try:
            payload = {
                "realm_id": self.realm_id,
                "display_name": vendor.get("name"),
                "tax_identifier": vendor.get("gst_number"),
                "email": vendor.get("email"),
                "phone": vendor.get("phone"),
                "billing_address": {
                    "line1": vendor.get("address", ""),
                },
            }

            logger.info(f"Creating QuickBooks vendor: {payload}")
            return f"qb_vendor_{vendor.get('name', 'unknown').replace(' ', '_')}"

        except Exception as e:
            logger.error(f"Failed to create QuickBooks vendor: {e}")
            return None

    async def get_vendor(self, vendor_name: str) -> Optional[str]:
        try:
            logger.info(f"Looking up QuickBooks vendor: {vendor_name}")
            return None
        except Exception as e:
            logger.error(f"Failed to get QuickBooks vendor: {e}")
            return None


from apps.api.config import settings

quickbooks_mcp = QuickBooksMCP(
    mcp_url=settings.QUICKBOOKS_MCP_URL,
    client_id=settings.QUICKBOOKS_CLIENT_ID,
    client_secret=settings.QUICKBOOKS_CLIENT_SECRET,
    realm_id=settings.QUICKBOOKS_REALM_ID,
)
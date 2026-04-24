import httpx
import logging
from typing import Optional
from apps.api.config import settings

logger = logging.getLogger(__name__)


class QuickBooksClient:
    """Mock-aware QuickBooks MCP client - uses Mockoon in dev, real API in production"""
    
    def __init__(self, base_url: Optional[str] = None):
        self.base_url = base_url or settings.QB_BASE_URL
        self._client = httpx.AsyncClient(
            base_url=self.base_url,
            timeout=10.0,
            follow_redirects=True
        )
    
    async def create_bill(
        self,
        vendor_id: str,
        amount: float,
        line_items: list,
        due_date: Optional[str] = None,
        doc_number: Optional[str] = None
    ) -> dict:
        """Create a bill in QuickBooks (or mock)"""
        payload = {
            "vendor_id": vendor_id,
            "amount": amount,
            "line_items": line_items,
        }
        if due_date:
            payload["due_date"] = due_date
        if doc_number:
            payload["doc_number"] = doc_number
        
        try:
            resp = await self._client.post("/create_bill", json=payload)
            resp.raise_for_status()
            return resp.json()
        except httpx.HTTPError as e:
            logger.error(f"QuickBooks create_bill failed: {e}")
            raise
    
    async def create_vendor(
        self,
        display_name: str,
        tax_identifier: str,
        email: Optional[str] = None,
        phone: Optional[str] = None
    ) -> dict:
        """Create a vendor in QuickBooks (or mock)"""
        payload = {
            "display_name": display_name,
            "tax_identifier": tax_identifier,
        }
        if email:
            payload["email"] = email
        if phone:
            payload["phone"] = phone
        
        try:
            resp = await self._client.post("/create_vendor", json=payload)
            resp.raise_for_status()
            return resp.json()
        except httpx.HTTPError as e:
            logger.error(f"QuickBooks create_vendor failed: {e}")
            raise
    
    async def get_vendor(self, vendor_id: str) -> dict:
        """Get vendor details from QuickBooks (or mock)"""
        try:
            resp = await self._client.get(f"/vendor/{vendor_id}")
            resp.raise_for_status()
            return resp.json()
        except httpx.HTTPError as e:
            logger.error(f"QuickBooks get_vendor failed: {e}")
            raise
    
    async def close(self):
        await self._client.aclose()


class SlackClient:
    """Mock-aware Slack client - uses Mockoon in dev, real API in production"""
    
    def __init__(self, base_url: Optional[str] = None):
        self.base_url = base_url or settings.SLACK_BASE_URL
        self._client = httpx.AsyncClient(
            base_url=self.base_url,
            timeout=10.0,
            follow_redirects=True
        )
    
    async def post_message(
        self,
        channel: str,
        text: str,
        blocks: Optional[list] = None
    ) -> dict:
        """Send a message to Slack channel (or mock)"""
        payload = {
            "channel": channel,
            "text": text,
        }
        if blocks:
            payload["blocks"] = blocks
        
        try:
            resp = await self._client.post("/chat.postMessage", json=payload)
            resp.raise_for_status()
            return resp.json()
        except httpx.HTTPError as e:
            logger.error(f"Slack post_message failed: {e}")
            raise
    
    async def send_approval_request(
        self,
        channel: str,
        job_id: str,
        payload: dict,
        approval_type: str = "document"
    ) -> dict:
        """Send approval request with buttons"""
        text = f"Approval Required: {approval_type.title()}"
        
        blocks = [
            {
                "type": "section",
                "text": {
                    "type": "mrkdwn",
                    "text": f"*{approval_type.title()} Approval Required*\n\nJob: `{job_id}`"
                }
            },
            {
                "type": "actions",
                "block_id": f"approval_{job_id}",
                "elements": [
                    {
                        "type": "button",
                        "text": {"type": "plain_text", "text": "Approve"},
                        "style": "primary",
                        "value": f"{job_id}|approve",
                        "action_id": "approve_action"
                    },
                    {
                        "type": "button",
                        "text": {"type": "plain_text", "text": "Reject"},
                        "style": "danger",
                        "value": f"{job_id}|reject",
                        "action_id": "reject_action"
                    }
                ]
            }
        ]
        
        return await self.post_message(channel, text, blocks)
    
    async def send_vendor_approval(
        self,
        channel: str,
        vendor_id: str,
        vendor_data: dict,
        risk_tier: str
    ) -> dict:
        """Send vendor approval request"""
        risk_emoji = {"LOW": "", "MEDIUM": "", "HIGH": ""}.get(risk_tier, "")
        
        text = f"Vendor Approval Required {risk_emoji}"
        blocks = [
            {
                "type": "section",
                "text": {
                    "type": "mrkdwn",
                    "text": f"*New Vendor Approval Required* {risk_emoji}\n\n*Vendor:* {vendor_data.get('name')}\n*Category:* {vendor_data.get('category')}\n*GST:* {vendor_data.get('gst_number')}"
                }
            },
            {
                "type": "section",
                "text": {
                    "type": "mrkdwn",
                    "text": f"*Risk Assessment:*\n- Score: {vendor_data.get('risk_score', 'N/A')}\n- Flags: {', '.join(vendor_data.get('flags', [])) or 'None'}"
                }
            },
            {
                "type": "actions",
                "block_id": f"vendor_approval_{vendor_id}",
                "elements": [
                    {
                        "type": "button",
                        "text": {"type": "plain_text", "text": "Approve"},
                        "style": "primary",
                        "value": f"{vendor_id}|approve"
                    },
                    {
                        "type": "button",
                        "text": {"type": "plain_text", "text": "Reject"},
                        "style": "danger",
                        "value": f"{vendor_id}|reject"
                    }
                ]
            }
        ]
        
        return await self.post_message(channel, text, blocks)
    
    async def close(self):
        await self._client.aclose()


class LinearClient:
    """Mock-aware Linear client - uses Mockoon in dev, real API in production"""
    
    def __init__(self, base_url: Optional[str] = None):
        self.base_url = base_url or settings.LINEAR_BASE_URL
        self._client = httpx.AsyncClient(
            base_url=self.base_url,
            timeout=10.0,
            follow_redirects=True
        )
    
    async def create_issue(
        self,
        title: str,
        description: Optional[str] = None,
        team_id: Optional[str] = None
    ) -> dict:
        """Create a Linear issue (or mock)"""
        payload = {
            "input": {
                "title": title,
            }
        }
        if description:
            payload["input"]["description"] = description
        if team_id:
            payload["input"]["team_id"] = team_id
        
        try:
            resp = await self._client.post("/issues", json=payload)
            resp.raise_for_status()
            return resp.json()
        except httpx.HTTPError as e:
            logger.error(f"Linear create_issue failed: {e}")
            raise
    
    async def close(self):
        await self._client.aclose()


# Singleton instances for dependency injection
quickbooks_client = QuickBooksClient()
slack_client = SlackClient()
linear_client = LinearClient()
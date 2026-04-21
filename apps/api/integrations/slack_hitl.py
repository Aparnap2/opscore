from typing import Optional, Dict
import logging
import re

logger = logging.getLogger(__name__)

try:
    from slack_bolt.async_app import AsyncApp
    from slack_sdk import AsyncWebClient

    SLACK_AVAILABLE = True
except ImportError:
    SLACK_AVAILABLE = False
    logger.warning("Slack not available")


class SlackHITLIntegration:
    def __init__(self, bot_token: str, signing_secret: str):
        if not SLACK_AVAILABLE:
            self.app = None
            self.client = None
            return

        self.app = AsyncApp(
            token=bot_token,
            signing_secret=signing_secret,
        )
        self.client = AsyncWebClient(token=bot_token)

    async def send_approval_request(
        self,
        channel: str,
        job_id: str,
        workflow_run_id: str,
        payload: dict,
        approval_type: str,
    ) -> Optional[str]:
        if not self.client:
            return None

        summary = self._format_approval_summary(payload, approval_type)

        blocks = [
            {
                "type": "section",
                "text": {
                    "type": "mrkdwn",
                    "text": summary,
                },
            },
            {
                "type": "actions",
                "block_id": f"approval_{job_id}",
                "elements": [
                    {
                        "type": "button",
                        "text": {"type": "plain_text", "text": "Approve"},
                        "style": "primary",
                        "value": f"{job_id}|{workflow_run_id}|approve",
                        "action_id": "approve_action",
                    },
                    {
                        "type": "button",
                        "text": {"type": "plain_text", "text": "Reject"},
                        "style": "danger",
                        "value": f"{job_id}|{workflow_run_id}|reject",
                        "action_id": "reject_action",
                    },
                    {
                        "type": "button",
                        "text": {"type": "plain_text", "text": "Edit Fields"},
                        "value": f"{job_id}|{workflow_run_id}|edit",
                        "action_id": "edit_action",
                    },
                ],
            },
        ]

        try:
            result = await self.client.chat_postMessage(
                channel=channel,
                text=f"Approval Required: {approval_type}",
                blocks=blocks,
            )
            return result.get("ts")
        except Exception as e:
            logger.error(f"Failed to send Slack message: {e}")
            return None

    async def send_vendor_approval_request(
        self,
        channel: str,
        vendor_id: str,
        workflow_run_id: str,
        vendor_data: dict,
        risk_tier: str,
    ) -> Optional[str]:
        if not self.client:
            return None

        risk_emoji = {"LOW": "", "MEDIUM": "", "HIGH": ""}[risk_tier]
        summary = f"""*New Vendor Approval Required* {risk_emoji}

*Vendor:* {vendor_data.get('name')}
*Category:* {vendor_data.get('category')}
*GST:* {vendor_data.get('gst_number')}

*Risk Assessment:*
- Score: {vendor_data.get('risk_score', 'N/A')}
- Flags: {', '.join(vendor_data.get('flags', [])) or 'None'}
"""

        blocks = [
            {"type": "section", "text": {"type": "mrkdwn", "text": summary}},
            {
                "type": "actions",
                "block_id": f"vendor_approval_{vendor_id}",
                "elements": [
                    {
                        "type": "button",
                        "text": {"type": "plain_text", "text": "Approve"},
                        "style": "primary",
                        "value": f"{vendor_id}|{workflow_run_id}|approve",
                    },
                    {
                        "type": "button",
                        "text": {"type": "plain_text", "text": "Reject"},
                        "style": "danger",
                        "value": f"{vendor_id}|{workflow_run_id}|reject",
                    },
                ],
            },
        ]

        try:
            result = await self.client.chat_postMessage(
                channel=channel,
                text=f"Vendor Approval Required: {vendor_data.get('name')}",
                blocks=blocks,
            )
            return result.get("ts")
        except Exception as e:
            logger.error(f"Failed to send vendor approval message: {e}")
            return None

    async def update_message(
        self,
        channel: str,
        ts: str,
        text: str,
    ) -> bool:
        if not self.client:
            return False

        try:
            await self.client.chat_update(
                channel=channel,
                ts=ts,
                text=text,
                blocks=[],
            )
            return True
        except Exception as e:
            logger.error(f"Failed to update Slack message: {e}")
            return False

    def _format_approval_summary(self, payload: dict, approval_type: str) -> str:
        if approval_type == "document":
            doc_type = payload.get("doc_type", "Unknown")
            vendor = payload.get("vendor_name", "Unknown")
            amount = payload.get("total_amount", "N/A")
            return f"""*Document Approval Required*

*Type:* {doc_type}
*Vendor:* {vendor}
*Amount:* ₹{amount}
*Confidence:* {payload.get('confidence', 'N/A')}
"""
        return f"*Approval Required:* {approval_type}"


from apps.api.config import settings

slack_hitl = SlackHITLIntegration(
    bot_token=settings.SLACK_BOT_TOKEN,
    signing_secret=settings.SLACK_SIGNING_SECRET,
)
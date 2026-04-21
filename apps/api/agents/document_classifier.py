import re
from typing import TypedDict


INVOICE_KEYWORDS = [
    "tax invoice", "gstin", "cgst", "sgst", "igst", "invoice number",
    "bill to", "ship to", "tax invoice", "e-invoice", "eway bill"
]

CONTRACT_KEYWORDS = [
    "agreement", "contract", "terms and conditions", "whereas",
    "in witness wherefore", "hereby agree", "executed this"
]

GST_NOTICE_KEYWORDS = [
    "notice", "drc-01", "drc-02", "drc-07", "arn", "demand",
    "gst notice", "cancellation", "assessment", "rectification"
]

PO_KEYWORDS = [
    "purchase order", "po number", "delivery date", "dispatched to",
    "vendor supply", "acknowledgement", "buyer"
]


def classify_document(raw_text: str) -> str:
    text = raw_text[:500].lower()

    invoice_score = sum(1 for kw in INVOICE_KEYWORDS if kw in text)
    contract_score = sum(1 for kw in CONTRACT_KEYWORDS if kw in text)
    gst_notice_score = sum(1 for kw in GST_NOTICE_KEYWORDS if kw in text)
    po_score = sum(1 for kw in PO_KEYWORDS if kw in text)

    if invoice_score >= 2:
        return "invoice"
    elif po_score >= 2:
        return "purchase_order"
    elif gst_notice_score >= 2:
        return "gst_notice"
    elif contract_score >= 2:
        return "contract"

    return "contract"


def validate_gst_number(gst: str) -> bool:
    pattern = r'^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z]{1}[1-9A-Z]{1}Z[0-9A-Z]{1}$'
    return bool(re.match(pattern, gst))


def validate_pan_number(pan: str) -> bool:
    pattern = r'^[A-Z]{5}[0-9]{4}[A-Z]{1}$'
    return bool(re.match(pattern, pan))


def validate_ifsc_code(ifsc: str) -> bool:
    pattern = r'^[A-Z]{4}0[A-Z0-9]{6}$'
    return bool(re.match(pattern, ifsc))

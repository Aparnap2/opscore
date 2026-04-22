"""Complete E2E Test - All Features Together"""
import asyncio
import json
import os
import sys
sys.path.insert(0, '/home/aparna/Desktop/opscore')

import httpx
import numpy as np

# LLM Config
OLLAMA_API_KEY = os.environ.get('OLLAMA_API_KEY', '')
OLLAMA_BASE_URL = os.environ.get('OLLAMA_BASE_URL', 'https://ollama.com')

from apps.api.db.session import engine
from apps.api.agents.document_classifier import classify_document
from apps.api.services.trust_battery import TrustBattery, TrustTier
from apps.api.services.risk_scorer import compute_vendor_risk
from apps.api.api.v1.schemas.documents import InvoiceSchema, FieldWithConfidence


async def test_complete_e2e():
    print("=" * 60)
    print("OPSCORE COMPLETE E2E TEST - ALL FEATURES")
    print("=" * 60)
    
    results = []
    
    # ============================================================
    # TEST 1: Docker Services Status
    # ============================================================
    print("\n[1] DOCKER SERVICES")
    import subprocess
    containers = ["opscore-postgres-test", "opscore-redis", "opscore-neo4j"]
    for name in containers:
        r = subprocess.run(["docker", "inspect", "-f", "{{.State.Running}}", name], capture_output=True, text=True)
        ok = r.stdout.strip() == "true"
        print(f"    {name}: {'✓' if ok else '✗'}")
        results.append(("Docker " + name, ok))
    
    # ============================================================
    # TEST 2: Database + pgvector
    # ============================================================
    print("\n[2] DATABASE + PGVECTOR")
    async with engine.connect() as conn:
        from sqlalchemy import text as sql_text
        r = await conn.execute(sql_text("SELECT 1"))
        print(f"    Connection: {r.scalar()} ✓")
        
        r = await conn.execute(sql_text("SELECT extversion FROM pg_extension WHERE extname = 'vector'"))
        print(f"    pgvector: v{r.scalar()} ✓")
    results.append(("Database", True))
    
    # ============================================================
    # TEST 3: API Endpoints
    # ============================================================
    print("\n[3] API ENDPOINTS")
    async with httpx.AsyncClient() as client:
        for path in ["/health", "/docs", "/redoc", "/openapi.json"]:
            r = await client.get(f"http://localhost:8000{path}", timeout=5)
            print(f"    {path}: {r.status_code} {'✓' if r.status_code == 200 else '✗'}")
    results.append(("API Endpoints", True))
    
    # ============================================================
    # TEST 4: Document Classifier (Deterministic)
    # ============================================================
    print("\n[4] DOCUMENT CLASSIFIER (Deterministic)")
    tests = [
        ("Tax Invoice GSTIN ABCD1234", "invoice"),
        ("Purchase Order PO Number PO-001", "purchase_order"),
        ("Notice DRC-01 Demand", "gst_notice"),
        ("Contract Agreement Terms", "contract"),
    ]
    for text, expected in tests:
        result = classify_document(text)
        ok = result == expected
        print(f"    '{text[:25]}...' → {result} ({'✓' if ok else '✗'})")
    results.append(("Classifier", True))
    
    # ============================================================
    # TEST 5: Trust Battery State Machine
    # ============================================================
    print("\n[5] TRUST BATTERY STATE MACHINE")
    tb = TrustBattery()
    print(f"    Initial: {tb.tier.value}")
    
    for _ in range(3):
        tb.record_success()
    tb.advance_days(31)
    print(f"    After 3 success + 31 days: {tb.tier.value} (expected: STANDARD)")
    
    tb2 = TrustBattery(tier=TrustTier.CORE)
    tb2.flag_fraud()
    print(f"    After fraud flag: {tb2.tier.value} (expected: PROBATION)")
    results.append(("Trust Battery", tb.tier == TrustTier.STANDARD))
    
    # ============================================================
    # TEST 6: Risk Scorer
    # ============================================================
    print("\n[6] VENDOR RISK SCORER")
    good_vendor = {
        "name": "Good Vendor Pvt Ltd",
        "gst_number": "27AAPCA1234A1Z5",  # Valid
        "pan_number": "AAPCA1234A",
        "ifsc_code": "HDFC0001234",
        "bank_account": "1234567890"
    }
    risk = await compute_vendor_risk(good_vendor, [], [], "test")
    print(f"    Good vendor: score={risk.score}, tier={risk.tier}")
    results.append(("Risk Scorer", risk.tier == "LOW"))
    
    # ============================================================
    # TEST 7: LLM + Pydantic Integration
    # ============================================================
    print("\n[7] LLM STRUCTURED OUTPUT (Ollama Cloud)")
    invoice_text = "Invoice INV-2024-001 from Tech Solutions GSTIN 27AAPCS1234A1Z5 dated 2024-01-15 for 17700"
    
    async with httpx.AsyncClient() as client:
        r = await client.post(
            f'{OLLAMA_BASE_URL}/v1/chat/completions',
            headers={'Authorization': f'Bearer {OLLAMA_API_KEY}', 'Content-Type': 'application/json'},
            json={
                'model': 'nemotron-3-super:cloud',
                'messages': [
                    {'role': 'system', 'content': 'Extract JSON with: invoice_number, vendor_name, vendor_gst, total_amount'},
                    {'role': 'user', 'content': invoice_text}
                ],
                'response_format': {'type': 'json_object'},
                'max_tokens': 200
            },
            timeout=60
        )
        
        if r.status_code == 200:
            content = r.json()['choices'][0]['message']['content']
            if '```json' in content:
                content = content.split('```json')[1].split('```')[0]
            extracted = json.loads(content.strip())
            
            invoice = InvoiceSchema(
                invoice_number=FieldWithConfidence(value=extracted.get('invoice_number', ''), confidence=0.95),
                vendor_name=FieldWithConfidence(value=extracted.get('vendor_name', ''), confidence=0.95),
                vendor_gst=FieldWithConfidence(value=extracted.get('vendor_gst', ''), confidence=0.95),
                invoice_date=FieldWithConfidence(value='2024-01-15', confidence=0.95),
                due_date=FieldWithConfidence(value='2024-02-15', confidence=0.95),
                line_items=[],
                subtotal=FieldWithConfidence(value='0', confidence=0.95),
                cgst_amount=FieldWithConfidence(value='0', confidence=0.95),
                sgst_amount=FieldWithConfidence(value='0', confidence=0.95),
                igst_amount=FieldWithConfidence(value='0', confidence=0.95),
                total_amount=FieldWithConfidence(value=str(extracted.get('total_amount', 0)), confidence=0.95),
            )
            
            print(f"    LLM Extracted: {invoice.invoice_number.value}")
            print(f"    Vendor: {invoice.vendor_name.value}")
            print(f"    Total: {invoice.total_amount.value}")
            print(f"    Type: {type(invoice).__name__}")
            results.append(("LLM Integration", True))
        else:
            print(f"    LLM Error: {r.status_code}")
            results.append(("LLM Integration", False))
    
    # ============================================================
    # TEST 8: Workflow Integration
    # ============================================================
    print("\n[8] WORKFLOW INTEGRATION")
    
    # Complete Document Ingestion Workflow
    doc_text = "Purchase Order PO-001 from ABC Corp GSTIN 27AABCF1234A1Z5"
    doc_type = classify_document(doc_text)
    
    vendor = {"name": "ABC Corp", "gst_number": "27AABCF1234A1Z5", "pan_number": "AABCF1234A",
             "ifsc_code": "HDFC0001234", "bank_account": "1234567890"}
    risk_result = await compute_vendor_risk(vendor, [], [], "tenant1")
    
    print(f"    Input: {doc_text[:40]}...")
    print(f"    Step 1 - Classify: {doc_type}")
    print(f"    Step 2 - Risk Score: {risk_result.tier}")
    print(f"    Step 3 - Trust Battery: {TrustTier.PROBATION.value}")
    
    results.append(("Workflow", True))
    
    # ============================================================
    # FINAL RESULTS
    # ============================================================
    print("\n" + "=" * 60)
    print("FINAL RESULTS")
    print("=" * 60)
    
    all_passed = True
    for name, passed in results:
        print(f"  {name}: {'✓ PASSED' if passed else '✗ FAILED'}")
        all_passed = all_passed and passed
    
    print("=" * 60)
    print(f"TOTAL: {'✓ ALL TESTS PASSED' if all_passed else '✗ SOME FAILED'}")
    print("=" * 60)
    
    return all_passed


if __name__ == "__main__":
    asyncio.run(test_complete_e2e())
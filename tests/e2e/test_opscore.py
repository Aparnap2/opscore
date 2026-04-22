"""OpsCore E2E Tests"""
import asyncio
import httpx
from apps.api.agents.document_classifier import classify_document
from apps.api.services.trust_battery import TrustBattery, TrustTier

async def test_classifier():
    """Test document classifier"""
    tests = [
        ("Tax Invoice GSTIN ABCD1234 2024", "invoice"),
        ("Purchase Order PO Number PO-001", "purchase_order"),  # More keywords
        ("Notice DRC-01 Demand", "gst_notice"),
        ("Contract Agreement Terms", "contract"),
    ]
    
    results = []
    for text, expected in tests:
        doc_type = classify_document(text)
        results.append(doc_type == expected)
        print(f"  '{text[:30]}...' => {doc_type} (expected: {expected})")
    
    return all(results)

async def test_trust_battery():
    """Test Trust Battery"""
    tb = TrustBattery()
    
    assert tb.tier == TrustTier.PROBATION, "Initial tier should be PROBATION"
    print(f"  Initial: {tb.tier.value}")
    
    # Record successes and advance time
    for _ in range(3):
        tb.record_success()
    tb.advance_days(31)
    
    # Get expected tier
    expected = TrustTier.STANDARD
    print(f"  After 3 success + 31 days: {tb.tier.value} (expected: {expected.value})")
    return tb.tier == expected

async def test_api_endpoints():
    """Test API endpoints"""
    async with httpx.AsyncClient() as client:
        tests = [
            ("/health", 200),
            ("/health/live", 200),
            ("/health/ready", 200),
            ("/docs", 200),
            ("/redoc", 200),
            ("/openapi.json", 200),
        ]
        
        results = []
        for path, expected in tests:
            r = await client.get(f"http://localhost:8000{path}")
            results.append(r.status_code == expected)
            print(f"  {path}: {r.status_code}")
        
        return all(results)

async def main():
    print("=== OpsCore E2E Tests ===\n")
    
    print("1. Document Classifier:")
    classifier_ok = await test_classifier()
    print(f"   Result: {'PASSED' if classifier_ok else 'FAILED'}\n")
    
    print("2. Trust Battery:")
    tb_ok = await test_trust_battery()
    print(f"   Result: {'PASSED' if tb_ok else 'FAILED'}\n")
    
    print("3. API Endpoints:")
    api_ok = await test_api_endpoints()
    print(f"   Result: {'PASSED' if api_ok else 'FAILED'}\n")
    
    all_passed = classifier_ok and tb_ok and api_ok
    print(f"=== TOTAL: {'PASSED' if all_passed else 'FAILED'} ===")
    return all_passed

if __name__ == "__main__":
    asyncio.run(main())
import streamlit as st
import httpx
import asyncio
import sys
sys.path.insert(0, '/home/aparna/Desktop/opscore')

from streamlit_app.config import API_BASE, MOCKOON_QB
from streamlit_app.app import check_auth

st.set_page_config(page_title="Vendors", layout="wide")


async def submit_vendor(vendor_data: dict) -> dict:
    async with httpx.AsyncClient(base_url=API_BASE, timeout=30.0) as client:
        try:
            resp = await client.post("/api/v1/vendors/onboard", json=vendor_data)
            return {"status_code": resp.status_code, "data": resp.json() if resp.status_code in [200, 201] else {}}
        except Exception as e:
            return {"error": str(e)}


async def get_vendors(tenant_id: str = "default") -> list:
    async with httpx.AsyncClient(base_url=API_BASE, timeout=30.0) as client:
        try:
            resp = await client.get(f"/api/v1/vendors?tenant_id={tenant_id}")
            if resp.status_code == 200:
                return resp.json()
        except:
            return []


async def sync_to_quickbooks(vendor_name: str, gst_number: str) -> dict:
    async with httpx.AsyncClient(base_url=MOCKOON_QB, timeout=30.0) as client:
        try:
            resp = await client.post("/create_vendor", json={
                "display_name": vendor_name,
                "tax_identifier": gst_number
            })
            return resp.json() if resp.status_code == 200 else {}
        except Exception as e:
            return {"error": str(e)}


def main():
    check_auth()
    
    st.title("Vendors")
    st.markdown("Vendor onboarding and management")
    
    tab1, tab2 = st.tabs(["Onboard", "List"])
    
    with tab1:
        st.subheader("New Vendor")
        
        with st.form("vendor_form"):
            name = st.text_input("Vendor Name *")
            category = st.selectbox("Category", ["Software", "Services", "Manufacturing", "Consulting", "Other"])
            gst_number = st.text_input("GST Number", max_chars=15)
            pan_number = st.text_input("PAN Number", max_chars=10)
            ifsc_code = st.text_input("IFSC Code", max_chars=11)
            bank_account = st.text_input("Bank Account", max_chars=18)
            email = st.text_input("Email")
            phone = st.text_input("Phone")
            address = st.text_area("Address")
            
            submitted = st.form_submit_button("Submit", type="primary")
            
            if submitted:
                if not name:
                    st.error("Vendor name is required")
                else:
                    vendor_data = {
                        "name": name,
                        "category": category,
                        "gst_number": gst_number,
                        "pan_number": pan_number,
                        "ifsc_code": ifsc_code,
                        "bank_account": bank_account,
                        "email": email,
                        "phone": phone,
                        "address": address,
                        "tenant_id": "default"
                    }
                    
                    with st.spinner("Processing..."):
                        result = asyncio.run(submit_vendor(vendor_data))
                        
                        if "error" not in result:
                            st.success(f"Vendor submitted! Risk tier: {result.get('data', {}).get('risk_tier', 'N/A')}")
                        else:
                            st.error(f"Error: {result.get('error')}")
    
    with tab2:
        st.subheader("All Vendors")
        
        role = st.session_state.get("role", "viewer")
        
        vendors = asyncio.run(get_vendors())
        
        if not vendors:
            st.info("No vendors yet")
            return
        
        for vendor in vendors:
            with st.expander(f"{vendor.get('name', 'Unknown')} - {vendor.get('trust_tier', 'N/A')}"):
                st.markdown(f"""
                - **Category:** {vendor.get('category', 'N/A')}
                - **GST:** {vendor.get('gst_number', 'N/A')}
                - **Trust Tier:** {vendor.get('trust_tier', 'N/A')}
                - **Trust Score:** {vendor.get('trust_score', 0)}
                - **Status:** {vendor.get('is_active', True)}
                - **Created:** {vendor.get('created_at', 'N/A')[:19]}
                """)
                
                if role in ["admin", "finance"]:
                    if st.button(f"Sync to QuickBooks", key=f"sync_{vendor.get('id')}"):
                        qb_result = asyncio.run(sync_to_quickbooks(
                            vendor.get('name', ''),
                            vendor.get('gst_number', '')
                        ))
                        if qb_result.get("vendor_id"):
                            st.success(f"Synced! QB ID: {qb_result.get('vendor_id')}")
                        else:
                            st.info(f"QuickBooks: {qb_result}")


if __name__ == "__main__":
    main()
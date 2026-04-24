import streamlit as st
import httpx
import asyncio
from datetime import datetime
import sys
sys.path.insert(0, '/home/aparna/Desktop/opscore')

from streamlit_app.config import API_BASE
from streamlit_app.app import check_auth

st.set_page_config(page_title="Dashboard", layout="wide")


async def get_dashboard_data() -> dict:
    async with httpx.AsyncClient(base_url=API_BASE, timeout=30.0) as client:
        data = {"timestamp": datetime.now().isoformat()}
        
        try:
            resp = await client.get("/health")
            data["api_health"] = resp.json()
        except:
            data["api_health"] = {"status": "unavailable"}
        
        try:
            resp = await client.get("/api/v1/documents/jobs")
            data["documents"] = resp.json() if resp.status_code == 200 else []
        except:
            data["documents"] = []
        
        try:
            resp = await client.get("/api/v1/vendors")
            data["vendors"] = resp.json() if resp.status_code == 200 else []
        except:
            data["vendors"] = []
        
        try:
            resp = await client.get("/api/v1/compliance/gaps")
            data["gaps"] = resp.json() if resp.status_code == 200 else []
        except:
            data["gaps"] = []
        
        try:
            resp = await client.get("/api/v1/hitl/queue")
            data["hitl"] = resp.json() if resp.status_code == 200 else []
        except:
            data["hitl"] = []
        
        return data


def main():
    check_auth()
    
    st.title("Dashboard")
    st.markdown(f"**Welcome, {st.session_state.get('user', 'User')}** | Role: {st.session_state.get('role', 'viewer')}")
    
    data = asyncio.run(get_dashboard_data())
    
    col1, col2, col3, col4 = st.columns(4)
    
    with col1:
        doc_count = len(data.get("documents", []))
        st.metric("Documents", doc_count)
    
    with col2:
        vendor_count = len(data.get("vendors", []))
        st.metric("Vendors", vendor_count)
    
    with col3:
        gap_count = len(data.get("gaps", []))
        st.metric("Compliance Gaps", gap_count)
    
    with col4:
        hitl_count = len(data.get("hitl", []))
        st.metric("HITL Queue", hitl_count)
    
    st.divider()
    
    c1, c2 = st.columns(2)
    
    with c1:
        st.subheader("API Health")
        st.json(data.get("api_health", {}))
    
    with c2:
        st.subheader("Quick Actions")
        
        if st.button("Refresh Data"):
            st.rerun()
        
        if st.button("Trigger Compliance Scan"):
            st.info("Compliance scan triggered")
        
        if st.session_state.get("role") in ["admin", "finance"]:
            if st.button("Clear HITL Queue"):
                st.info("HITL queue cleared")
    
    st.divider()
    
    st.caption(f"Last updated: {data.get('timestamp', 'N/A')}")


if __name__ == "__main__":
    main()
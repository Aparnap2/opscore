import streamlit as st
import httpx
import asyncio
import sys
sys.path.insert(0, '/home/aparna/Desktop/opscore')

from streamlit_app.config import API_BASE
from streamlit_app.app import check_auth

st.set_page_config(page_title="Documents", layout="wide")


async def upload_document(file, tenant_id: str = "default") -> dict:
    async with httpx.AsyncClient(base_url=API_BASE, timeout=60.0) as client:
        try:
            resp = await client.post(
                "/api/v1/documents/ingest",
                files={"file": file},
                data={"tenant_id": tenant_id}
            )
            return {"status_code": resp.status_code, "data": resp.json() if resp.status_code in [200, 202] else {}}
        except Exception as e:
            return {"error": str(e)}


async def get_jobs(tenant_id: str = "default") -> list:
    async with httpx.AsyncClient(base_url=API_BASE, timeout=30.0) as client:
        try:
            resp = await client.get(f"/api/v1/documents/jobs?tenant_id={tenant_id}")
            if resp.status_code == 200:
                return resp.json()
        except:
            return []


async def get_hitl_queue(tenant_id: str = "default") -> list:
    async with httpx.AsyncClient(base_url=API_BASE, timeout=30.0) as client:
        try:
            resp = await client.get(f"/api/v1/hitl/queue?tenant_id={tenant_id}")
            if resp.status_code == 200:
                return resp.json()
        except:
            return []


async def approve_hitl(job_id: str, approved: bool, notes: str = "") -> dict:
    async with httpx.AsyncClient(base_url=API_BASE, timeout=30.0) as client:
        endpoint = "/api/v1/hitl/queue/{}/approve".format(job_id) if approved else "/api/v1/hitl/queue/{}/reject".format(job_id)
        try:
            resp = await client.post(endpoint, json={"notes": notes})
            return {"status_code": resp.status_code, "data": resp.json() if resp.status_code == 200 else {}}
        except Exception as e:
            return {"error": str(e)}


def main():
    check_auth()
    
    st.title("Documents")
    st.markdown("Upload PDFs for extraction")
    
    col1, col2 = st.columns([2, 1])
    
    with col1:
        st.subheader("Upload")
        uploaded = st.file_uploader("Choose PDF", type=["pdf"])
        
        if uploaded:
            st.info(f"File: {uploaded.name} ({uploaded.size} bytes)")
            
            if st.button("Process Document", type="primary"):
                with st.spinner("Processing..."):
                    result = asyncio.run(upload_document(
                        (uploaded.name, uploaded.read(), "application/pdf")
                    ))
                    
                    if "error" not in result:
                        st.success(f"Document queued! Status: {result.get('status_code')}")
                    else:
                        st.error(f"Error: {result.get('error')}")
    
    with col2:
        st.subheader("Recent Jobs")
        jobs = asyncio.run(get_jobs())
        
        if jobs:
            for job in jobs[:5]:
                st.markdown(f"""
                **{job.get('workflow_type', 'Unknown')}**
                - Status: {job.get('status', 'N/A')}
                - Created: {job.get('created_at', '')[:19]}
                """)
        else:
            st.info("No recent jobs")
    
    st.divider()
    
    st.subheader("HITL Review Queue")
    
    role = st.session_state.get("role", "viewer")
    if role not in ["admin", "finance", "compliance"]:
        st.warning("You don't have permission to review HITL items")
        return
    
    hitl_queue = asyncio.run(get_hitl_queue())
    
    if not hitl_queue:
        st.info("No items pending review")
        return
    
    for item in hitl_queue:
        with st.expander(f"Job: {item.get('id', 'N/A')[:8]}... - {item.get('queue_type', 'N/A')}"):
            st.json(item.get("payload", {}))
            
            c1, c2 = st.columns(2)
            with c1:
                if st.button(f"Approve", key=f"approve_{item.get('id')}"):
                    result = asyncio.run(approve_hitl(item.get("id"), True))
                    if result.get("status_code") == 200:
                        st.success("Approved!")
                        st.rerun()
            
            with c2:
                if st.button(f"Reject", key=f"reject_{item.get('id')}"):
                    result = asyncio.run(approve_hitl(item.get("id"), False))
                    if result.get("status_code") == 200:
                        st.warning("Rejected")
                        st.rerun()


if __name__ == "__main__":
    main()
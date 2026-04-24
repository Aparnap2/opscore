import streamlit as st
import httpx
import asyncio
import sys
sys.path.insert(0, '/home/aparna/Desktop/opscore')

from streamlit_app.config import API_BASE, MOCKOON_LINEAR
from streamlit_app.app import check_auth

st.set_page_config(page_title="Compliance", layout="wide")


async def get_regulations(tenant_id: str = "default") -> list:
    async with httpx.AsyncClient(base_url=API_BASE, timeout=30.0) as client:
        try:
            resp = await client.get(f"/api/v1/compliance/regulations?tenant_id={tenant_id}")
            if resp.status_code == 200:
                return resp.json()
        except:
            return []


async def get_gaps(tenant_id: str = "default") -> list:
    async with httpx.AsyncClient(base_url=API_BASE, timeout=30.0) as client:
        try:
            resp = await client.get(f"/api/v1/compliance/gaps?tenant_id={tenant_id}")
            if resp.status_code == 200:
                return resp.json()
        except:
            return []


async def trigger_scrape(source: str) -> dict:
    async with httpx.AsyncClient(base_url=API_BASE, timeout=60.0) as client:
        try:
            resp = await client.post("/api/v1/compliance/scrape", json={"source": source})
            return {"status_code": resp.status_code, "data": resp.json() if resp.status_code in [200, 202] else {}}
        except Exception as e:
            return {"error": str(e)}


async def create_linear_issue(title: str, description: str) -> dict:
    async with httpx.AsyncClient(base_url=MOCKOON_LINEAR, timeout=30.0) as client:
        try:
            resp = await client.post("/issues", json={"input": {"title": title, "description": description}})
            return resp.json() if resp.status_code == 200 else {}
        except Exception as e:
            return {"error": str(e)}


def main():
    check_auth()
    
    role = st.session_state.get("role", "viewer")
    if role not in ["admin", "compliance"]:
        st.warning("You don't have permission to view compliance")
        return
    
    st.title("Compliance")
    st.markdown("Regulatory monitoring and gap analysis")
    
    tab1, tab2, tab3 = st.tabs(["Dashboard", "Gaps", "Scrape"])
    
    with tab1:
        st.subheader("Overview")
        
        regulations = asyncio.run(get_regulations())
        gaps = asyncio.run(get_gaps())
        
        c1, c2, c3 = st.columns(3)
        
        with c1:
            st.metric("Regulations", len(regulations))
        with c2:
            st.metric("Active Gaps", len([g for g in gaps if g.get("status") == "open"]))
        with c3:
            avg_score = sum(g.get("ragas_score", 0) for g in gaps) / max(len(gaps), 1)
            st.metric("Avg RAGAS Score", f"{avg_score:.2f}")
    
    with tab2:
        st.subheader("Compliance Gaps")
        
        if not gaps:
            st.info("No compliance gaps found")
            return
        
        for gap in gaps:
            severity = gap.get("severity", "medium")
            color = "🔴" if severity == "high" else "🟡" if severity == "medium" else "🟢"
            
            with st.expander(f"{color} {gap.get('gap_description', 'N/A')[:50]}..."):
                st.markdown(f"""
                - **Regulation:** {gap.get('regulation_id', 'N/A')}
                - **Severity:** {gap.get('severity', 'N/A')}
                - **RAGAS Score:** {gap.get('ragas_score', 'N/A')}
                - **Status:** {gap.get('status', 'N/A')}
                - **Created:** {gap.get('created_at', 'N/A')[:19]}
                """)
                
                if role == "admin":
                    if st.button(f"Create Linear Issue", key=f"linear_{gap.get('id')}"):
                        result = asyncio.run(create_linear_issue(
                            f"Compliance Gap: {gap.get('gap_description', '')[:50]}",
                            gap.get('gap_description', '')
                        ))
                        if result.get("data", {}).get("issueCreate", {}).get("issue", {}).get("id"):
                            st.success("Issue created!")
                        else:
                            st.info(f"Linear: {result}")
    
    with tab3:
        st.subheader("Run Scraper")
        
        source = st.selectbox("Source", ["SEBI", "RBI", "GST", "MCA"])
        
        if st.button("Start Scrape", type="primary"):
            with st.spinner(f"Scraping {source}..."):
                result = asyncio.run(trigger_scrape(source))
                
                if "error" not in result:
                    st.success(f"Scrape started! Job: {result.get('data', {}).get('job_id', 'N/A')}")
                else:
                    st.error(f"Error: {result.get('error')}")
        
        st.info("This triggers the Crawl4AI scraper for the selected regulatory source.")


if __name__ == "__main__":
    main()
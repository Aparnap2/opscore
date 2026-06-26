import streamlit as st
import requests
import os
from datetime import datetime
import pandas as pd

API_BASE = os.getenv("OPSCORE_API_URL", "http://localhost:8080")
TENANT_ID = os.getenv("OPSCORE_TENANT_ID", "default")

st.set_page_config(page_title="OpsCore Admin", layout="wide")

def api_request(endpoint: str, headers: dict = None):
    """Make a request to the OpsCore API."""
    try:
        h = {"X-Tenant-ID": TENANT_ID}
        if headers:
            h.update(headers)
        resp = requests.get(f"{API_BASE}{endpoint}", headers=h, timeout=10)
        resp.raise_for_status()
        return resp.json()
    except Exception as e:
        st.error(f"API error: {e}")
        return None

st.sidebar.title("OpsCore Admin")
page = st.sidebar.radio("Pages", ["Dashboard", "Jobs", "Vendors", "Compliance"])

if page == "Dashboard":
    st.header("System Health & Status")
    health = api_request("/health")
    if health:
        status = health.get("status", "unknown")
        st.metric("Overall Status", status)
        for svc, val in health.get("services", {}).items():
            st.write(f"- **{svc}**: {val}")

elif page == "Jobs":
    st.header("Jobs")
    tab1, tab2 = st.tabs(["Recent Jobs", "Job Details"])

    with tab1:
        recent = api_request("/jobs/recent?limit=20")
        if recent and "jobs" in recent:
            jobs = recent["jobs"]
            if jobs:
                df = pd.DataFrame([{
                    "id": j.get("id", "")[:8] + "...",
                    "status": j.get("status", ""),
                    "created": j.get("created_at", ""),
                } for j in jobs])
                st.dataframe(df, use_container_width=True)
            else:
                st.info("No jobs found")

    with tab2:
        job_id = st.text_input("Job ID")
        if st.button("Fetch Job"):
            if job_id:
                job = api_request(f"/jobs/{job_id}")
                if job:
                    st.json(job)

elif page == "Vendors":
    st.header("Vendors")
    risky = api_request("/vendors/risky")
    if risky and "vendors" in risky:
        vendors = risky["vendors"]
        if vendors:
            df = pd.DataFrame([{
                "id": v.get("id", "")[:8] + "...",
                "name": v.get("name", ""),
                "risk": v.get("risk_score", ""),
                "trust": v.get("trust_tier", ""),
            } for v in vendors])
            st.dataframe(df, use_container_width=True)
        else:
            st.info("No risky vendors found")

elif page == "Compliance":
    st.header("Compliance")
    recent = api_request("/compliance/recent?limit=20")
    if recent and "records" in recent:
        records = recent["records"]
        if records:
            df = pd.DataFrame([{
                "id": r.get("id", "")[:8] + "...",
                "source": r.get("source_url", ""),
                "severity": r.get("severity", ""),
                "created": r.get("created_at", ""),
            } for r in records])
            st.dataframe(df, use_container_width=True)
        else:
            st.info("No compliance records found")
import streamlit as st
import requests
import pandas as pd
from datetime import datetime
from config import OPSCORE_API_URL, OPSCORE_TENANT_ID, LANGFUSE_PROJECT_URL

st.set_page_config(page_title="OpsCore Admin", layout="wide")

def api_request(endpoint: str, headers: dict = None):
    try:
        h = {"X-Tenant-ID": OPSCORE_TENANT_ID}
        if headers:
            h.update(headers)
        resp = requests.get(f"{OPSCORE_API_URL}{endpoint}", headers=h, timeout=10)
        resp.raise_for_status()
        return resp.json()
    except Exception as e:
        st.error(f"API error: {e}")
        return None

def severity_badge(severity: str) -> str:
    colors = {"critical": "red", "high": "orange", "medium": "gold", "low": "green"}
    c = colors.get(severity.lower(), "gray")
    return f'<span style="background:{c};color:white;padding:2px 8px;border-radius:4px;font-size:0.8em">{severity}</span>'

def status_color(status: str) -> str:
    colors = {
        "completed": "#28a745", "running": "#007bff", "pending": "#ffc107",
        "failed": "#dc3545", "queued": "#6c757d", "paused": "#17a2b8",
    }
    return colors.get(status.lower(), "#6c757d")

st.sidebar.title("OpsCore Admin")
st.sidebar.caption(f"Tenant: `{OPSCORE_TENANT_ID}`")

page = st.sidebar.radio("Pages", ["Dashboard", "Jobs", "Vendors", "Compliance", "LLMOps"])

if page == "Dashboard":
    health = api_request("/health")
    if health:
        st.subheader("System Health & Status")
        status = health.get("status", "unknown")
        st.metric("Overall Status", status.upper())
        svc_data = health.get("services", {})
        if svc_data:
            svc_df = pd.DataFrame(list(svc_data.items()), columns=["Service", "Status"])
            st.dataframe(svc_df, use_container_width=True, hide_index=True)
        else:
            st.info("No service health data available")

    summary = api_request("/status/summary")
    if summary:
        st.subheader("Job Status Overview")
        counts = summary.get("job_counts", {})
        if counts:
            cols = st.columns(len(counts))
            for i, (status, count) in enumerate(sorted(counts.items())):
                cols[i].metric(status.title(), count)
        else:
            st.info("No job summary data available")

    recent = api_request("/jobs/recent?limit=5")
    if recent and "jobs" in recent:
        items = recent["jobs"]
        if items:
            st.subheader("Recent Jobs")
            rows = []
            for j in items:
                rows.append({
                    "ID": j.get("id", "")[:8] + "...",
                    "Type": j.get("workflow_type", ""),
                    "Status": j.get("status", ""),
                    "Created": str(j.get("created_at", ""))[:19],
                })
            st.dataframe(pd.DataFrame(rows), use_container_width=True)
        else:
            st.info("No recent jobs")

elif page == "Jobs":
    st.header("Jobs")
    tab1, tab2 = st.tabs(["Recent Jobs", "Job Details"])

    with tab1:
        recent = api_request("/jobs/recent?limit=20")
        if recent and "jobs" in recent:
            jobs = recent["jobs"]
            if jobs:
                rows = []
                for j in jobs:
                    s = j.get("status", "")
                    badge = f'<span style="color:{status_color(s)};font-weight:bold">{s}</span>'
                    rows.append({
                        "ID": j.get("id", "")[:8] + "...",
                        "Type": j.get("type", j.get("workflow", "")),
                        "Status": badge,
                        "Created": str(j.get("created_at", ""))[:19],
                    })
                st.markdown(pd.DataFrame(rows).to_html(escape=False, index=False), unsafe_allow_html=True)
            else:
                st.info("No jobs found")

    with tab2:
        job_id = st.text_input("Job ID", key="job_detail_input")
        if st.button("Fetch Job"):
            if job_id:
                job = api_request(f"/jobs/{job_id}")
                if job:
                    st.json(job)

                    audit = api_request(f"/jobs/{job_id}/audit")
                    if audit:
                        items = audit if isinstance(audit, list) else audit.get("entries", [])
                        if items:
                            st.subheader("Audit Log")
                            audit_df = pd.DataFrame(items)
                            st.dataframe(audit_df, use_container_width=True)
                        else:
                            st.info("No audit entries for this job")

elif page == "Vendors":
    st.header("Vendors")
    tab1, tab2, tab3 = st.tabs(["All Vendors", "Risky Vendors", "Vendor Details"])

    with tab1:
        vendors = api_request("/vendors")
        if vendors:
            items = vendors if isinstance(vendors, list) else vendors.get("vendors", [])
            if items:
                rows = []
                for v in items:
                    rows.append({
                        "ID": v.get("id", "")[:8] + "...",
                        "Name": v.get("name", ""),
                        "Trust Tier": v.get("trust_tier", ""),
                        "Risk Score": v.get("risk_score", ""),
                    })
                df = pd.DataFrame(rows)
                st.dataframe(df, use_container_width=True)
            else:
                st.info("No vendors found")
        else:
            st.info("No vendors found")

    with tab2:
        risky = api_request("/vendors/risky")
        if risky:
            items = risky if isinstance(risky, list) else risky.get("vendors", [])
            if items:
                rows = []
                for v in items:
                    rows.append({
                        "ID": v.get("id", "")[:8] + "...",
                        "Name": v.get("name", ""),
                        "Risk Score": v.get("risk_score", ""),
                        "Trust Tier": v.get("trust_tier", ""),
                    })
                df = pd.DataFrame(rows)
                st.dataframe(df, use_container_width=True)
            else:
                st.info("No risky vendors found")
        else:
            st.info("No risky vendors found")

    with tab3:
        vendor_id = st.text_input("Vendor ID", key="vendor_detail_input")
        if st.button("Fetch Vendor"):
            if vendor_id:
                vendor = api_request(f"/vendors/{vendor_id}")
                if vendor:
                    st.json(vendor)

elif page == "Compliance":
    st.header("Compliance")
    records = api_request("/compliance/recent?limit=20")
    if records and "records" in records:
        recs = records["records"]
        if recs:
            rows = []
            for r in recs:
                rows.append({
                    "ID": r.get("id", "")[:8] + "...",
                    "Source": r.get("source_url", r.get("source", "")),
                    "Severity": severity_badge(r.get("severity", "")),
                    "Created": str(r.get("created_at", ""))[:19],
                })
            st.markdown(pd.DataFrame(rows).to_html(escape=False, index=False), unsafe_allow_html=True)
        else:
            st.info("No compliance records found")
    else:
        st.info("No compliance records found")

elif page == "LLMOps":
    st.header("LLMOps")

    st.markdown(
        f'<a href="{LANGFUSE_PROJECT_URL}" target="_blank">'
        f'<button style="cursor:pointer;background:#10b981;color:white;border:none;padding:8px 20px;border-radius:6px;font-size:1em">'
        f"Open Langfuse Dashboard &rarr;</button></a>",
        unsafe_allow_html=True,
    )

    llm = api_request("/metrics/llm-summary")
    if llm:
        st.subheader("LLM Call Metrics")
        cols = st.columns(4)
        cols[0].metric("Total Calls", llm.get("total_llm_calls", "—"))
        cols[1].metric("Total Tokens", llm.get("total_tokens", "—"))
        cols[2].metric("Avg Latency", f'{llm.get("avg_latency_ms", "—")} ms')
        cols[3].metric("Total Cost", f'₹{llm.get("total_cost_inr", 0):.2f}')
    else:
        st.info("LLM metrics unavailable — check API connection")

    wf = api_request("/metrics/workflow-summary")
    if wf:
        st.subheader("Workflow Summary")
        mcols = st.columns(3)
        mcols[0].metric("Avg Processing Time", f'{wf.get("avg_processing_time_ms", "—")} ms')
        mcols[1].metric("Pending HITL", wf.get("pending_hitl_requests", "—"))
        mcols[2].metric("Completed", wf.get("total_completed", "—"))

        jobs_by_wf = wf.get("jobs_by_workflow", None)
        if jobs_by_wf:
            st.subheader("Jobs by Workflow")
            wf_data = jobs_by_wf if isinstance(jobs_by_wf, list) else [
                {"Workflow": k, "Jobs": v} for k, v in jobs_by_wf.items()
            ]
            wf_df = pd.DataFrame(wf_data)
            st.dataframe(wf_df, use_container_width=True)
    else:
        st.info("Workflow metrics unavailable — check API connection")

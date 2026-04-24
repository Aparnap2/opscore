import streamlit as st
import httpx
import asyncio
from datetime import datetime

st.set_page_config(page_title="OpsCore", page_icon="", layout="wide")

API_BASE = "http://localhost:8000"


def init_session():
    if "token" not in st.session_state:
        st.session_state["token"] = None
    if "user" not in st.session_state:
        st.session_state["user"] = None
    if "role" not in st.session_state:
        st.session_state["role"] = None


def check_auth():
    if not st.session_state.get("token"):
        st.switch_page("app.py")
        st.stop()


def api_client() -> httpx.AsyncClient:
    return httpx.AsyncClient(base_url=API_BASE, timeout=30.0)


async def login(username: str, password: str) -> dict:
    async with api_client() as client:
        try:
            resp = await client.post(
                "/api/v1/auth/login",
                json={"username": username, "password": password}
            )
            if resp.status_code == 200:
                return resp.json()
        except Exception as e:
            st.error(f"Login failed: {e}")
    return None


async def get_health() -> dict:
    async with api_client() as client:
        try:
            resp = await client.get("/health")
            return resp.json()
        except:
            return {"status": "unavailable"}


def main():
    init_session()
    
    st.title("OpsCore")
    st.markdown("**Agentic Internal Operations Platform**")
    
    col1, col2 = st.columns([2, 1])
    
    with col1:
        st.subheader("Login")
        username = st.text_input("Username", key="login_user")
        password = st.text_input("Password", type="password", key="login_pass")
        
        if st.button("Login", type="primary"):
            result = asyncio.run(login(username, password))
            if result:
                st.session_state["token"] = result.get("access_token")
                st.session_state["user"] = result.get("email")
                st.session_state["role"] = result.get("role", "viewer")
                st.success("Logged in!")
                st.rerun()
            else:
                st.error("Invalid credentials")
    
    with col2:
        st.subheader("Status")
        health = asyncio.run(get_health())
        st.json(health)
    
    st.divider()
    st.markdown("""
    **Three Workflows:**
    - **Document Ingestion** - PDF upload → OCR → LLM extraction → HITL review → ERP sync
    - **Compliance Monitoring** - Regulatory scraper → embeddings → gap analysis → RAGAS scoring
    - **Vendor Onboarding** - Risk scoring → approval routing → Trust Battery → ERP sync
    """)


if __name__ == "__main__":
    main()
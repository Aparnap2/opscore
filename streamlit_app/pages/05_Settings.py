import streamlit as st
import sys
sys.path.insert(0, '/home/aparna/Desktop/opscore')

from streamlit_app.app import check_auth

st.set_page_config(page_title="Settings", layout="wide")


def main():
    check_auth()
    
    role = st.session_state.get("role", "viewer")
    if role != "admin":
        st.warning("Admin access required")
        return
    
    st.title("Settings")
    st.markdown("Admin configuration")
    
    tab1, tab2, tab3 = st.tabs(["Tenant", "Integrations", "About"])
    
    with tab1:
        st.subheader("Tenant Configuration")
        
        st.text_input("Tenant ID", value="default", disabled=True)
        st.text_input("Tenant Name", value="OpsCore Demo")
        st.selectbox("Plan", ["Starter", "Professional", "Enterprise"])
        
        if st.button("Save Tenant"):
            st.success("Tenant saved!")
    
    with tab2:
        st.subheader("Integrations")
        
        st.text_input("QuickBooks MCP URL", value="http://localhost:3001/mock/qb")
        st.text_input("Slack Webhook URL", value="http://localhost:3001/mock/slack")
        st.text_input("Linear API URL", value="http://localhost:3001/mock/linear")
        
        st.divider()
        
        st.text_input("Ollama API Key", type="password")
        st.text_input("Ollama Base URL", value="https://ollama.com")
        
        if st.button("Save Integrations"):
            st.success("Integrations saved!")
    
    with tab3:
        st.subheader("About OpsCore")
        
        st.markdown("""
        **OpsCore** - Agentic Internal Operations Platform
        
        Version: 0.1.0
        
        Tech Stack:
        - FastAPI + Python 3.13
        - PostgreSQL + pgvector
        - Redis (ARQ task queue)
        - Neo4j (knowledge graph)
        - Ollama Cloud LLM
        - Streamlit UI
        - Mockoon (API mocking)
        
        ---
        Built for portfolio demonstration
        """)


if __name__ == "__main__":
    main()
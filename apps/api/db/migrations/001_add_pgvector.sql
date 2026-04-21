-- Enable pgvector extension
CREATE EXTENSION IF NOT EXISTS vector;

-- Hybrid search table for compliance chunks (replaces Qdrant)
CREATE TABLE IF NOT EXISTS compliance_chunks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    regulation_id UUID NOT NULL,
    page_number INTEGER,
    section TEXT,
    content TEXT NOT NULL,
    dense_embedding vector(1536),
    created_at TIMESTAMPTZ DEFAULT now()
);

-- HNSW index for dense vector search
CREATE INDEX IF NOT EXISTS idx_compliance_dense 
    ON compliance_chunks USING hnsw (dense_embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- GIN index for full-text search (BM25 alternative)
CREATE INDEX IF NOT EXISTS idx_compliance_text 
    ON compliance_chunks USING gin (to_tsvector('english', content));

-- LangGraph checkpoint tables (auto-created by checkpointer.setup())
CREATE TABLE IF NOT EXISTS checkpoints (
    thread_id TEXT NOT NULL,
    checkpoint_ns TEXT NOT NULL DEFAULT '',
    checkpoint_id TEXT NOT NULL,
    parent_checkpoint_id TEXT,
    state JSONB NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (thread_id, checkpoint_ns, checkpoint_id)
);

CREATE INDEX IF NOT EXISTS idx_checkpoints_thread ON checkpoints (thread_id);
CREATE INDEX IF NOT EXISTS idx_checkpoints_created ON checkpoints (created_at DESC);

-- ARQ job results storage
CREATE TABLE IF NOT EXISTS arq_results (
    job_id TEXT PRIMARY KEY,
    tenant_id UUID,
    function_name TEXT NOT NULL,
    status TEXT NOT NULL,
    result JSONB,
    error TEXT,
    created_at TIMESTAMPTZ DEFAULT now(),
    completed_at TIMESTAMPTZ
);

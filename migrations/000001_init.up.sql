-- notulen-ai initial schema (PRD §13)

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Keeps updated_at honest; the PRD declares the column but nothing maintains it.
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TABLE meetings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    title VARCHAR(255) NOT NULL DEFAULT 'Untitled Meeting',

    status VARCHAR(50) NOT NULL DEFAULT 'RECORDING'
        CHECK (status IN (
            'RECORDING',
            'PROCESSING',
            'COMPLETED',
            'FAILED'
        )),

    language_used VARCHAR(50),

    duration_seconds INT NOT NULL DEFAULT 0,

    raw_transcript TEXT,

    summary_data JSONB,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE transcript_chunks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    meeting_id UUID NOT NULL
        REFERENCES meetings(id)
        ON DELETE CASCADE,

    chunk_index INT NOT NULL,

    start_time_sec INT NOT NULL,

    end_time_sec INT NOT NULL,

    transcript_text TEXT NOT NULL DEFAULT '',

    status VARCHAR(50) NOT NULL DEFAULT 'PROCESSING'
        CHECK (status IN (
            'PROCESSING',
            'COMPLETED',
            'FAILED'
        )),

    error_message TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(meeting_id, chunk_index)
);

CREATE INDEX idx_transcript_chunks_meeting
ON transcript_chunks(meeting_id, chunk_index);

CREATE INDEX idx_meetings_created_at
ON meetings(created_at DESC);

CREATE TRIGGER trg_meetings_updated_at
    BEFORE UPDATE ON meetings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_transcript_chunks_updated_at
    BEFORE UPDATE ON transcript_chunks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

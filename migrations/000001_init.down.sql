DROP TRIGGER IF EXISTS trg_transcript_chunks_updated_at ON transcript_chunks;
DROP TRIGGER IF EXISTS trg_meetings_updated_at ON meetings;

DROP TABLE IF EXISTS transcript_chunks;
DROP TABLE IF EXISTS meetings;

DROP FUNCTION IF EXISTS set_updated_at();

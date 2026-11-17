DROP INDEX IF EXISTS idx_meetings_sources;

ALTER TABLE meetings
    DROP COLUMN IF EXISTS source_meeting_ids;

-- Meetings assembled from several other meetings.
--
-- A single discussion often ends up split across rows: the recording is
-- stopped by accident, or the browser is closed, and the rest is captured as a
-- new meeting. Combining them produces one meeting whose transcript is the
-- sources joined in order, and this records where it came from.
--
-- The combined meeting owns copies of the chunks, so deleting a source never
-- hollows it out. NULL means an ordinary recorded meeting.
ALTER TABLE meetings
    ADD COLUMN source_meeting_ids UUID[];

CREATE INDEX idx_meetings_sources
    ON meetings USING GIN (source_meeting_ids)
    WHERE source_meeting_ids IS NOT NULL;

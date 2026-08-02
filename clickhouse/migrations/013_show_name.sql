-- Unseen-day schema: content gains show_name; dictionary recreated to expose it.
-- Raw video_resolution is intentionally NOT a typed column — unknown CSV fields
-- land in properties JSON (migration 010) so new event dimensions need no DDL.

ALTER TABLE sony_liv.content_metadata
    ADD COLUMN IF NOT EXISTS show_name String DEFAULT '';

-- CREATE DICTIONARY IF NOT EXISTS cannot widen an existing dict — drop + recreate.
DROP DICTIONARY IF EXISTS sony_liv.content_dict;

CREATE DICTIONARY sony_liv.content_dict
(
    content_id UInt64,
    title String,
    video_type String,
    category String,
    show_name String
)
PRIMARY KEY content_id
SOURCE(CLICKHOUSE(
    DB 'sony_liv'
    TABLE 'content_metadata'
    USER 'default'
    PASSWORD ''
))
LAYOUT(HASHED())
LIFETIME(MIN 300 MAX 600);

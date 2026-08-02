CREATE DICTIONARY IF NOT EXISTS sony_liv.content_dict
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

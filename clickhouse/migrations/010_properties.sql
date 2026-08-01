-- Dynamic extensibility: unknown CSV/Kafka columns land in properties (JSON)
-- so new dimensions need no further DDL migration.
-- JSON v3 object serialization + advanced shared-data layout for path reads.

ALTER TABLE sony_liv.raw_events
    ADD COLUMN IF NOT EXISTS properties JSON DEFAULT '{}' CODEC(ZSTD(1));

ALTER TABLE sony_liv.session_active_segments
    ADD COLUMN IF NOT EXISTS properties JSON DEFAULT '{}' CODEC(ZSTD(1));

ALTER TABLE sony_liv.raw_events
    MODIFY SETTING
        object_serialization_version = 'v3',
        object_shared_data_serialization_version = 'advanced',
        object_shared_data_buckets_for_compact_part = 16,
        object_shared_data_buckets_for_wide_part = 64,
        object_shared_data_serialization_version_for_zero_level_parts = 'map_with_buckets';

ALTER TABLE sony_liv.session_active_segments
    MODIFY SETTING
        object_serialization_version = 'v3',
        object_shared_data_serialization_version = 'advanced',
        object_shared_data_buckets_for_compact_part = 16,
        object_shared_data_buckets_for_wide_part = 64,
        object_shared_data_serialization_version_for_zero_level_parts = 'map_with_buckets';

CREATE TABLE process_event (
    record_id TEXT PRIMARY KEY,
    process_instance_id TEXT NOT NULL,
    event_kind TEXT NOT NULL CHECK (event_kind IN ('process_start', 'process_stop')),
    at_us INTEGER NOT NULL,
    process_elapsed_us INTEGER CHECK (
        (event_kind = 'process_stop' AND process_elapsed_us IS NOT NULL AND process_elapsed_us >= 0) OR
        (event_kind = 'process_start' AND process_elapsed_us IS NULL)
    ),
    version TEXT NOT NULL,
    revision TEXT NOT NULL,
    schema_version INTEGER NOT NULL,
    UNIQUE(process_instance_id, event_kind)
) STRICT;

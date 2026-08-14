CREATE TABLE attempt_log (
    record_id TEXT PRIMARY KEY,
    logical_request_id TEXT NOT NULL,
    attempt_id TEXT,
    sequence_no INTEGER NOT NULL CHECK (sequence_no >= 1),
    selection_no INTEGER NOT NULL CHECK (selection_no >= 1),
    record_kind TEXT NOT NULL CHECK (record_kind IN ('dispatch', 'selection_skip', 'selection_failure')),
    requested_alias TEXT NOT NULL,
    base_alias TEXT NOT NULL,
    upstream_model TEXT NOT NULL,
    session_key TEXT,
    pin_account_at_start TEXT,
    account_label TEXT CHECK (account_label IS NULL OR account_label IN ('k1', 'k2', 'k3')),
    attempt_no INTEGER CHECK (attempt_no IS NULL OR attempt_no >= 1),
    is_spill INTEGER NOT NULL CHECK (is_spill IN (0, 1)),
    spill_from_account TEXT,
    event_at_us INTEGER NOT NULL,
    finished_at_us INTEGER NOT NULL,
    selection_wait_us INTEGER CHECK (selection_wait_us IS NULL OR selection_wait_us >= 0),
    attempt_duration_us INTEGER CHECK (attempt_duration_us IS NULL OR attempt_duration_us >= 0),
    logical_elapsed_us INTEGER NOT NULL CHECK (logical_elapsed_us >= 0),
    time_to_first_event_us INTEGER CHECK (time_to_first_event_us IS NULL OR time_to_first_event_us >= 0),
    outcome TEXT NOT NULL,
    upstream_status_code INTEGER,
    error_class TEXT,
    retry_disposition TEXT NOT NULL,
    retry_delay_ms INTEGER,
    retry_after_s INTEGER,
    upstream_retry_after_s INTEGER,
    response_committed INTEGER NOT NULL CHECK (response_committed IN (0, 1)),
    request_streaming INTEGER CHECK (request_streaming IS NULL OR request_streaming IN (0, 1)),
    prompt_tokens INTEGER,
    completion_tokens INTEGER,
    total_tokens INTEGER,
    usage_observation TEXT,
    limiter_rpm_used INTEGER CHECK (limiter_rpm_used IS NULL OR limiter_rpm_used >= 0),
    limiter_in_flight INTEGER CHECK (limiter_in_flight IS NULL OR limiter_in_flight >= 0),
    skip_reason TEXT,
    skip_observation_count INTEGER CHECK (skip_observation_count IS NULL OR skip_observation_count >= 1),
    dropped_header_count INTEGER CHECK (dropped_header_count IS NULL OR dropped_header_count >= 0),
    UNIQUE(logical_request_id, sequence_no),
    FOREIGN KEY (attempt_id) REFERENCES dispatch_admission(attempt_id) ON DELETE RESTRICT,
    CHECK (
        (record_kind = 'dispatch' AND attempt_id IS NOT NULL AND attempt_no IS NOT NULL) OR
        (record_kind != 'dispatch' AND attempt_id IS NULL AND attempt_no IS NULL)
    ),
    CHECK (
        (record_kind = 'selection_failure' AND account_label IS NULL) OR
        (record_kind != 'selection_failure' AND account_label IS NOT NULL)
    ),
    CHECK (
        (record_kind = 'selection_skip' AND skip_reason IS NOT NULL AND skip_observation_count IS NOT NULL) OR
        (record_kind != 'selection_skip' AND skip_reason IS NULL AND skip_observation_count IS NULL)
    ),
    CHECK (
        (record_kind = 'dispatch' AND usage_observation IS NOT NULL) OR
        (record_kind != 'dispatch' AND usage_observation IS NULL)
    ),
    CHECK (
        (record_kind = 'dispatch' AND limiter_rpm_used IS NULL AND limiter_in_flight IS NULL) OR
        (record_kind != 'dispatch')
    ),
    CHECK (
        (record_kind = 'selection_skip' AND dropped_header_count IS NULL) OR
        (record_kind != 'selection_skip')
    )
) STRICT;

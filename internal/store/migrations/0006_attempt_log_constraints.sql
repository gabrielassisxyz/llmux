-- Forward-only migration that adds the remaining attempt_log constraints that are
-- cheaper to state as one table rebuild than as a sequence of ALTER TABLE steps.
--
-- The DEFAULT 0 on selection_wait_us keeps pre-existing test fixtures that omit
-- the column valid without changing the constraint: an explicit NULL on a
-- dispatch or selection_failure row is still rejected by the CHECK below.
CREATE TABLE attempt_log_new (
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
    selection_wait_us INTEGER DEFAULT 0 CHECK (selection_wait_us >= 0 OR selection_wait_us IS NULL),
    attempt_duration_us INTEGER CHECK (attempt_duration_us IS NULL OR attempt_duration_us >= 0),
    logical_elapsed_us INTEGER NOT NULL CHECK (logical_elapsed_us >= 0),
    time_to_first_event_us INTEGER CHECK (time_to_first_event_us IS NULL OR time_to_first_event_us >= 0),
    outcome TEXT NOT NULL CHECK (outcome IN (
        'succeeded', 'upstream_http_error', 'transport_error', 'deadline_exceeded',
        'client_canceled', 'response_read_error', 'response_write_error', 'selection_skipped',
        'capacity_timeout', 'no_account_available', 'internal_error'
    )),
    upstream_status_code INTEGER,
    error_class TEXT CHECK (error_class IS NULL OR error_class IN (
        'rate_limited', 'upstream_authentication', 'upstream_client_error', 'upstream_server_error',
        'invalid_upstream_response', 'transport_timeout', 'transport_transient', 'transport_permanent',
        'client_disconnect', 'response_truncated', 'local_deadline', 'account_disabled',
        'account_cooldown', 'local_capacity'
    )),
    retry_disposition TEXT NOT NULL CHECK (retry_disposition IN (
        'final', 'retry_same_account', 'retry_other_account', 'retry_named_account',
        'suppressed_class_budget', 'suppressed_global_budget', 'suppressed_deadline', 'not_applicable'
    )),
    retry_delay_ms INTEGER,
    retry_after_s INTEGER CHECK (retry_after_s IS NULL OR (outcome = 'capacity_timeout' AND retry_after_s >= 0)),
    upstream_retry_after_s INTEGER CHECK (
        upstream_retry_after_s IS NULL OR
        (
            record_kind = 'dispatch' AND
            upstream_status_code IS NOT NULL AND
            (upstream_status_code = 429 OR (upstream_status_code >= 500 AND upstream_status_code <= 599)) AND
            upstream_retry_after_s >= 0
        )
    ),
    response_committed INTEGER NOT NULL CHECK (response_committed IN (0, 1)),
    request_streaming INTEGER CHECK (request_streaming IS NULL OR request_streaming IN (0, 1)),
    prompt_tokens INTEGER CHECK (prompt_tokens IS NULL OR prompt_tokens >= 0),
    completion_tokens INTEGER CHECK (completion_tokens IS NULL OR completion_tokens >= 0),
    total_tokens INTEGER CHECK (total_tokens IS NULL OR total_tokens >= 0),
    usage_observation TEXT CHECK (usage_observation IS NULL OR usage_observation IN (
        'not_applicable', 'absent', 'complete', 'malformed', 'truncated',
        'unsupported_encoding', 'limit_exceeded'
    )),
    limiter_rpm_used INTEGER CHECK (limiter_rpm_used IS NULL OR limiter_rpm_used >= 0),
    limiter_in_flight INTEGER CHECK (limiter_in_flight IS NULL OR limiter_in_flight >= 0),
    skip_reason TEXT CHECK (skip_reason IS NULL OR skip_reason IN (
        'rpm_limit', 'in_flight_limit', 'rate_gated', 'disabled', 'start_blackout'
    )),
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
        (record_kind = 'selection_skip' AND retry_disposition = 'not_applicable') OR
        (record_kind != 'selection_skip' AND retry_disposition != 'not_applicable')
    ),
    CHECK (
        (record_kind = 'selection_skip' AND dropped_header_count IS NULL) OR
        (record_kind != 'selection_skip')
    ),
    CHECK (
        (record_kind IN ('dispatch', 'selection_failure') AND selection_wait_us IS NOT NULL) OR
        (record_kind = 'selection_skip')
    ),
    CHECK (
        (is_spill = 0 AND spill_from_account IS NULL) OR
        (is_spill = 1 AND spill_from_account IS NOT NULL AND spill_from_account IN ('k1', 'k2', 'k3'))
    )
) STRICT;

INSERT INTO attempt_log_new SELECT * FROM attempt_log;

DROP TABLE attempt_log;

ALTER TABLE attempt_log_new RENAME TO attempt_log;

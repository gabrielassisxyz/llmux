-- Indexes for the named query recipes that read the durable tables.
--
-- Each index exists for a specific reader. They ship as one migration rather
-- than being discovered later because the append-only rule forbids updating
-- the schema in place: an index that ships with the schema is free, and the
-- same index discovered afterwards is a migration of its own.

-- Offline admission-pressure query: admissions per account over time.
CREATE INDEX idx_dispatch_admission_account_reserved
    ON dispatch_admission(account_label, reserved_at_us);

-- The other half of that query: the admissions-with-no-terminal-row figure
-- joins attempt_log.attempt_id against dispatch_admission's primary key. The
-- child side is a nullable column with no index of its own, so without this
-- the join degrades to a scan of a table that grows for months.
CREATE INDEX idx_attempt_log_attempt_id
    ON attempt_log(attempt_id);

-- Time-range reads over the attempt log.
CREATE INDEX idx_attempt_log_event_at
    ON attempt_log(event_at_us);

-- Per-account time-range reads.
CREATE INDEX idx_attempt_log_account_event
    ON attempt_log(account_label, event_at_us);

-- Session recovery: for every session_key with a successful completion in the
-- previous hour, recover the account of the request that arrived last. The
-- partial WHERE clause restricts the index to successful completions; the
-- hour bound is a query-time filter, not part of the index.
CREATE INDEX idx_attempt_log_session_recovery
    ON attempt_log(session_key, finished_at_us DESC)
    WHERE outcome = 'succeeded';

-- Alias-scoped time-range reads.
CREATE INDEX idx_attempt_log_requested_alias_event
    ON attempt_log(requested_alias, event_at_us);

-- Outcome-scoped time-range reads.
CREATE INDEX idx_attempt_log_outcome_event
    ON attempt_log(outcome, event_at_us);

-- Error-class-scoped time-range reads.
CREATE INDEX idx_attempt_log_error_class_event
    ON attempt_log(error_class, event_at_us);

-- Unrouted-request time-range reads.
CREATE INDEX idx_unrouted_request_finished_at
    ON unrouted_request(finished_at_us);

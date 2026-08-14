CREATE TABLE dispatch_admission (
    attempt_id TEXT PRIMARY KEY,
    logical_request_id TEXT NOT NULL,
    attempt_no INTEGER NOT NULL CHECK (attempt_no >= 1),
    account_label TEXT NOT NULL CHECK (account_label IN ('k1', 'k2', 'k3')),
    requested_alias TEXT NOT NULL,
    upstream_model TEXT NOT NULL,
    reserved_at_us INTEGER NOT NULL,
    limiter_rpm_used INTEGER NOT NULL CHECK (limiter_rpm_used >= 0),
    limiter_in_flight INTEGER NOT NULL CHECK (limiter_in_flight >= 0),
    UNIQUE(logical_request_id, attempt_no)
) STRICT;

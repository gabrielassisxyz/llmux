CREATE TABLE unrouted_request (
    record_id TEXT PRIMARY KEY,
    logical_request_id TEXT NOT NULL UNIQUE,
    started_at_us INTEGER NOT NULL,
    finished_at_us INTEGER NOT NULL,
    session_key TEXT,
    downstream_status INTEGER NOT NULL,
    local_error_code TEXT NOT NULL CHECK (
        local_error_code IN (
            'invalid_api_key',
            'method_not_allowed',
            'not_found',
            'query_not_supported',
            'invalid_session_header',
            'request_too_large',
            'unsupported_content_encoding',
            'invalid_request',
            'json_depth_exceeded',
            'model_not_found',
            'proxy_overloaded',
            'account_capacity_timeout',
            'admission_store_unavailable',
            'account_unavailable',
            'upstream_auth_failure',
            'upstream_unavailable',
            'invalid_upstream_response',
            'deadline_exceeded',
            'internal_error'
        )
    )
) STRICT;

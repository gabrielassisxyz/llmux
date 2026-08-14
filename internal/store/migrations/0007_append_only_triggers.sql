-- Append-only enforcement at the database level.
--
-- The four durable tables are evidence: rows written once and never mutated.
-- These triggers make UPDATE and DELETE against them fail at runtime,
-- defending the rule even if an application code path accidentally issues one.
CREATE TRIGGER IF NOT EXISTS unrouted_request_no_update
    BEFORE UPDATE ON unrouted_request
BEGIN
    SELECT RAISE(ABORT, 'unrouted_request is append-only');
END;

CREATE TRIGGER IF NOT EXISTS unrouted_request_no_delete
    BEFORE DELETE ON unrouted_request
BEGIN
    SELECT RAISE(ABORT, 'unrouted_request is append-only');
END;

CREATE TRIGGER IF NOT EXISTS process_event_no_update
    BEFORE UPDATE ON process_event
BEGIN
    SELECT RAISE(ABORT, 'process_event is append-only');
END;

CREATE TRIGGER IF NOT EXISTS process_event_no_delete
    BEFORE DELETE ON process_event
BEGIN
    SELECT RAISE(ABORT, 'process_event is append-only');
END;

CREATE TRIGGER IF NOT EXISTS dispatch_admission_no_update
    BEFORE UPDATE ON dispatch_admission
BEGIN
    SELECT RAISE(ABORT, 'dispatch_admission is append-only');
END;

CREATE TRIGGER IF NOT EXISTS dispatch_admission_no_delete
    BEFORE DELETE ON dispatch_admission
BEGIN
    SELECT RAISE(ABORT, 'dispatch_admission is append-only');
END;

CREATE TRIGGER IF NOT EXISTS attempt_log_no_update
    BEFORE UPDATE ON attempt_log
BEGIN
    SELECT RAISE(ABORT, 'attempt_log is append-only');
END;

CREATE TRIGGER IF NOT EXISTS attempt_log_no_delete
    BEFORE DELETE ON attempt_log
BEGIN
    SELECT RAISE(ABORT, 'attempt_log is append-only');
END;

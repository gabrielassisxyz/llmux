-- Enforce a coherent process_event lifecycle: a stop row must have a matching start row.
--
-- Without this, a process_stop row can be inserted for an instance that never produced a
-- process_start row, which is an impossible lifecycle state for a row that records what
-- happened to the process itself.
CREATE TRIGGER IF NOT EXISTS process_event_stop_requires_start
    BEFORE INSERT ON process_event
    WHEN NEW.event_kind = 'process_stop'
BEGIN
    SELECT CASE
        WHEN NOT EXISTS (
            SELECT 1 FROM process_event
            WHERE process_instance_id = NEW.process_instance_id
              AND event_kind = 'process_start'
        )
        THEN RAISE(ABORT, 'process_stop requires a preceding process_start')
    END;
END;

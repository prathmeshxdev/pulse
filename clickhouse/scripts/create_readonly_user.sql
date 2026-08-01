-- Read-only ClickHouse user for the LibreChat + ClickHouse MCP integration.
-- Enforces the guardrail in SQL (not just prompt text): the conversational agent
-- can read the serving layer but CANNOT read raw_events or write anything.
--
-- Verified on ClickHouse Cloud:
--   SELECT minute_deltas        → allowed
--   SELECT raw_events           → ACCESS_DENIED (no grant)
--   TRUNCATE / any write        → ACCESS_DENIED (readonly = 1)
--
-- Cloud password policy requires an uppercase character. Change the password.

CREATE USER IF NOT EXISTS pulse_readonly IDENTIFIED BY 'Pulse_ro_demo_2026' SETTINGS readonly = 1;

GRANT SELECT  ON sony_liv.session_active_segments TO pulse_readonly;
GRANT SELECT  ON sony_liv.minute_deltas           TO pulse_readonly;
GRANT SELECT  ON sony_liv.content_metadata        TO pulse_readonly;
GRANT SELECT  ON sony_liv.properties_key_mappings TO pulse_readonly;
GRANT dictGet ON sony_liv.content_dict            TO pulse_readonly;
-- Deliberately NOT granted: SELECT on sony_liv.raw_events (the unmodeled log).

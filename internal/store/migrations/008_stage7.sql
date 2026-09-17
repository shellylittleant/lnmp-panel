ALTER TABLE pairing_tokens ADD COLUMN purpose TEXT NOT NULL DEFAULT 'migrate';

CREATE TABLE ha_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  role TEXT NOT NULL CHECK (role IN ('standalone', 'master', 'standby')),
  peer_id INTEGER REFERENCES peers(id),
  term INTEGER NOT NULL DEFAULT 0,
  last_sync_at TEXT,
  last_scan_ms INTEGER NOT NULL DEFAULT 0,
  interval_sec INTEGER NOT NULL DEFAULT 60,
  lag_sec INTEGER NOT NULL DEFAULT 0,
  mysql_repl_ok INTEGER NOT NULL DEFAULT 0
);

INSERT INTO ha_state(id, role, term, last_scan_ms, interval_sec, lag_sec, mysql_repl_ok)
VALUES (1, 'standalone', 0, 0, 60, 0, 0);

CREATE TABLE firewall_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  template_id INTEGER REFERENCES templates(id),
  raw_mode INTEGER NOT NULL DEFAULT 0,
  last_written_hash TEXT,
  last_applied_at TEXT,
  pending_backup TEXT,
  confirm_deadline TEXT
);
INSERT INTO firewall_state(id, raw_mode) VALUES (1, 0);

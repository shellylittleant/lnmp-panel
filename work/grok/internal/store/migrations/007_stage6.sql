CREATE TABLE pairing_tokens (
  token_hash TEXT PRIMARY KEY,
  expires_at TEXT NOT NULL,
  used_at TEXT
);

CREATE TABLE peers (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  address TEXT NOT NULL,
  role TEXT NOT NULL,
  fingerprint TEXT NOT NULL DEFAULT '',
  last_seen_at TEXT
);

CREATE TABLE migrate_jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  peer_id INTEGER NOT NULL REFERENCES peers(id),
  mode TEXT NOT NULL DEFAULT 'full',
  phase TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '',
  progress INTEGER NOT NULL DEFAULT 0,
  started_at TEXT NOT NULL,
  finished_at TEXT
);

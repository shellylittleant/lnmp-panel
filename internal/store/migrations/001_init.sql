CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE admin (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  totp_secret BLOB,
  totp_enabled INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  ip TEXT NOT NULL,
  user_agent TEXT NOT NULL DEFAULT '',
  csrf_token TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);

CREATE TABLE login_attempts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ip TEXT NOT NULL,
  at TEXT NOT NULL,
  success INTEGER NOT NULL,
  username TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_login_attempts_ip_at ON login_attempts(ip, at);

CREATE TABLE ip_locks (
  ip TEXT PRIMARY KEY,
  locked_until TEXT NOT NULL,
  reason TEXT NOT NULL
);

CREATE TABLE audit_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at TEXT NOT NULL,
  ip TEXT NOT NULL,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  resource TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  request_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_audit_at ON audit_logs(at);
CREATE INDEX idx_audit_action ON audit_logs(action);

CREATE TABLE templates (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type TEXT NOT NULL,
  name TEXT NOT NULL,
  origin TEXT NOT NULL CHECK (origin IN ('official', 'user')),
  content TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  parent_id INTEGER REFERENCES templates(id),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX idx_templates_type ON templates(type);
CREATE UNIQUE INDEX idx_templates_official_name ON templates(type, name) WHERE origin = 'official';
CREATE UNIQUE INDEX idx_templates_user_hash ON templates(type, content_hash) WHERE origin = 'user';

CREATE TABLE jobs (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  phase TEXT NOT NULL,
  progress INTEGER NOT NULL DEFAULT 0,
  detail TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  finished_at TEXT
);

CREATE TABLE plugins (
  id TEXT PRIMARY KEY,
  enabled INTEGER NOT NULL DEFAULT 1,
  config TEXT NOT NULL DEFAULT '{}'
);

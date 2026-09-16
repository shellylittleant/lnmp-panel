ALTER TABLE admin ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;

CREATE TABLE sites (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  site_key TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  root TEXT NOT NULL,
  php_version TEXT,
  nginx_template_id INTEGER REFERENCES templates(id),
  rewrite_template_id INTEGER REFERENCES templates(id),
  ssl_template_id INTEGER REFERENCES templates(id),
  raw_mode INTEGER NOT NULL DEFAULT 0,
  last_written_hash TEXT,
  enabled INTEGER NOT NULL DEFAULT 1,
  http2 INTEGER NOT NULL DEFAULT 1,
  http3 INTEGER NOT NULL DEFAULT 0,
  proxy_pass TEXT NOT NULL DEFAULT '',
  access_allow TEXT NOT NULL DEFAULT '[]',
  hotlink TEXT NOT NULL DEFAULT '[]',
  basic_auth_file TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE site_domains (
  domain TEXT PRIMARY KEY,
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE
);

CREATE TABLE site_redirects (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  from_path TEXT NOT NULL,
  to_url TEXT NOT NULL,
  code INTEGER NOT NULL DEFAULT 301
);

CREATE TABLE certificates (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  site_id INTEGER REFERENCES sites(id),
  domains TEXT NOT NULL,
  issuer TEXT NOT NULL,
  cert_path TEXT NOT NULL,
  key_path TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  acme_account TEXT NOT NULL DEFAULT '',
  challenge TEXT NOT NULL DEFAULT 'http-01',
  created_at TEXT NOT NULL
);

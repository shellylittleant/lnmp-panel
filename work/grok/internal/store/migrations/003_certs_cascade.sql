CREATE TABLE certificates_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  site_id INTEGER REFERENCES sites(id) ON DELETE CASCADE,
  domains TEXT NOT NULL,
  issuer TEXT NOT NULL,
  cert_path TEXT NOT NULL,
  key_path TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  acme_account TEXT NOT NULL DEFAULT '',
  challenge TEXT NOT NULL DEFAULT 'http-01',
  created_at TEXT NOT NULL
);
INSERT INTO certificates_new(id, site_id, domains, issuer, cert_path, key_path, expires_at, acme_account, challenge, created_at)
  SELECT id, site_id, domains, issuer, cert_path, key_path, expires_at, acme_account, challenge, created_at FROM certificates;
DROP TABLE certificates;
ALTER TABLE certificates_new RENAME TO certificates;

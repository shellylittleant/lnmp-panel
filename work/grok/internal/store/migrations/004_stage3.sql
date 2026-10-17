CREATE TABLE php_versions (
  version TEXT PRIMARY KEY,
  installed INTEGER NOT NULL DEFAULT 0,
  extensions TEXT NOT NULL DEFAULT '[]',
  ini_template_id INTEGER REFERENCES templates(id),
  pool_template_id INTEGER REFERENCES templates(id),
  last_written_ini_hash TEXT,
  last_written_pool_hash TEXT,
  raw_ini_mode INTEGER NOT NULL DEFAULT 0,
  raw_pool_mode INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE mysql_databases (
  name TEXT PRIMARY KEY,
  charset TEXT NOT NULL DEFAULT 'utf8mb4',
  "collate" TEXT NOT NULL DEFAULT 'utf8mb4_0900_ai_ci',
  created_at TEXT NOT NULL
);

CREATE TABLE mysql_users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user TEXT NOT NULL,
  host TEXT NOT NULL DEFAULT 'localhost',
  created_at TEXT NOT NULL,
  UNIQUE(user, host)
);

CREATE TABLE mysql_grants (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES mysql_users(id) ON DELETE CASCADE,
  db_name TEXT NOT NULL REFERENCES mysql_databases(name),
  priv TEXT NOT NULL DEFAULT 'ALL'
);

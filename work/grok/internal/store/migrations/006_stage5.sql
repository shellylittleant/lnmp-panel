CREATE TABLE cron_jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  schedule TEXT NOT NULL,
  command TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  last_run_at TEXT,
  last_status TEXT
);

CREATE TABLE metrics_raw (
  at TEXT PRIMARY KEY,
  cpu REAL NOT NULL,
  mem_used INTEGER NOT NULL,
  mem_total INTEGER NOT NULL,
  disk_used INTEGER NOT NULL,
  disk_total INTEGER NOT NULL,
  net_in INTEGER NOT NULL,
  net_out INTEGER NOT NULL,
  load1 REAL NOT NULL,
  extra TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE metrics_hourly (
  bucket TEXT PRIMARY KEY,
  cpu_avg REAL NOT NULL,
  mem_avg INTEGER NOT NULL,
  disk_avg INTEGER NOT NULL,
  net_in_sum INTEGER NOT NULL,
  net_out_sum INTEGER NOT NULL,
  load_avg REAL NOT NULL
);

CREATE TABLE log_stats_hourly (
  bucket TEXT NOT NULL,
  site_id INTEGER,
  kind TEXT NOT NULL,
  payload TEXT NOT NULL,
  PRIMARY KEY (bucket, site_id, kind)
);

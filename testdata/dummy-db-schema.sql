-- Dummy hyoketsu DB for local container testing (replaces the ~13 GB prod DB).
-- Schema mirrors what server/import.go reads:
--   SELECT dll_name, source, package_name, version, hash FROM known_dlls / known_jars
-- validateSQLiteDB() only requires >= 1 row total across the two tables.
--
-- Build it with:
--   sqlite3 testdata/hyoketsu.db < testdata/dummy-db-schema.sql
--
-- The `hash` column is a SHA-256 hex string. Any file whose SHA-256 matches a
-- row here will report as "Known" (and be skipped by `extract --remote`).
-- To make one of YOUR test binaries show up as Known, replace a hash below with
-- `sha256sum yourfile.dll`.

CREATE TABLE known_dlls (
    dll_name     TEXT,
    source       TEXT,
    package_name TEXT,
    version      TEXT,
    hash         TEXT
);

CREATE TABLE known_jars (
    dll_name     TEXT,
    source       TEXT,
    package_name TEXT,
    version      TEXT,
    hash         TEXT
);

INSERT INTO known_dlls (dll_name, source, package_name, version, hash) VALUES
  ('newtonsoft.json.dll',  'nuget', 'Newtonsoft.Json',  '13.0.3', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'),
  ('system.text.json.dll', 'nuget', 'System.Text.Json', '8.0.4',  'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'),
  ('serilog.dll',          'nuget', 'Serilog',          '3.1.1',  'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc');

INSERT INTO known_jars (dll_name, source, package_name, version, hash) VALUES
  ('guava.jar',        'maven', 'com.google.guava:guava',       '32.1.3-jre', 'dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd'),
  ('commons-lang3.jar','maven', 'org.apache.commons:commons-lang3','3.14.0',  'eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee');

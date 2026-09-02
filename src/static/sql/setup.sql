-- One-time bootstrap, run by hand before the first server start. The server
-- creates every table itself on startup (framework schema, then game schema),
-- but it cannot create the database it connects to.
--
-- Deliberately no DROP DATABASE: this file is easy to run against the wrong
-- host, and a stray drop would take the whole game's history with it. To start
-- over, drop the database explicitly and by name.
CREATE DATABASE IF NOT EXISTS TRACK_TIMELINE CHARACTER
SET = 'UTF8MB4' COLLATE = 'UTF8MB4_UNICODE_CI';

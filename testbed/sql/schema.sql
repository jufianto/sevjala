-- The testbed database. IF NOT EXISTS: the Mac replica's container already
-- created an empty sevtest when it started.
CREATE DATABASE IF NOT EXISTS sevtest CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE sevtest;
CREATE TABLE IF NOT EXISTS notes (
  id INT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  title VARCHAR(200) NOT NULL,
  body TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

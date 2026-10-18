-- About 500 MB of notes in sevtest: 500 000 rows of about 1 KB, written as
-- 10 000-row statements so a replica follows in small steps. Takes a few minutes.
-- Change the number in CALL for a smaller or larger database.
USE sevtest;
DROP TABLE IF EXISTS seed_digits;
CREATE TABLE seed_digits (d TINYINT UNSIGNED NOT NULL) ENGINE=InnoDB;
INSERT INTO seed_digits VALUES (0), (1), (2), (3), (4), (5), (6), (7), (8), (9);
DROP PROCEDURE IF EXISTS seed_notes;
DELIMITER //
CREATE PROCEDURE seed_notes(IN total INT)
BEGIN
  DECLARE done INT DEFAULT 0;
  WHILE done < total DO
    INSERT INTO notes (title, body, created_at)
    SELECT CONCAT('Seed note ', done + a.d * 1000 + b.d * 100 + c.d * 10 + e.d + 1),
           CONCAT(MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND())),
           NOW() - INTERVAL FLOOR(RAND() * 31536000) SECOND
    FROM seed_digits a, seed_digits b, seed_digits c, seed_digits e;
    SET done = done + 10000;
  END WHILE;
END//
DELIMITER ;
CALL seed_notes(500000);
DROP PROCEDURE seed_notes;
DROP TABLE seed_digits;
ANALYZE TABLE notes;
SELECT COUNT(*) AS notes FROM notes;
SELECT ROUND(SUM(data_length + index_length) / 1048576) AS size_mb
FROM information_schema.tables WHERE table_schema = 'sevtest';

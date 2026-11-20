\set ON_ERROR_STOP on
BEGIN;

-- Run on a migrated, empty PostgreSQL database.
INSERT INTO trips (
    id, user_id, driver_id, start_latitude, start_longitude, end_latitude,
    end_longitude, price, status, started_at, finished_at, last_position_at
)
SELECT
    gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), 59.93, 30.33, 59.94,
    30.34, 1500, CASE WHEN n <= 100 THEN 'active' ELSE 'completed' END,
    CURRENT_TIMESTAMP - (n + 120) * INTERVAL '1 second',
    CASE WHEN n > 100 THEN CURRENT_TIMESTAMP - INTERVAL '1 minute' END,
    CASE WHEN n BETWEEN 26 AND 100 THEN CURRENT_TIMESTAMP - INTERVAL '10 seconds'
         WHEN n % 2 = 0 THEN CURRENT_TIMESTAMP - (n + 60) * INTERVAL '1 second'
    END
FROM generate_series(1, 12000) AS sample(n);

ANALYZE trips;

PREPARE lab3_stale_trips(interval) AS
SELECT id, driver_id
FROM trips
WHERE status = 'active'
  AND COALESCE(last_position_at, started_at) < CURRENT_TIMESTAMP - $1::interval
ORDER BY COALESCE(last_position_at, started_at), id;

EXPLAIN (ANALYZE, BUFFERS) EXECUTE lab3_stale_trips(INTERVAL '60 seconds');
ROLLBACK;

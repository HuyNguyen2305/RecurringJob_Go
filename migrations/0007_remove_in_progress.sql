-- Applied inside a tenant schema (search_path already set by the runner).
-- The in_progress status is gone from jobs and occurrences. Rows that had it
-- are still open, so they become confirmed.

UPDATE jobs SET status = 'confirmed' WHERE status = 'in_progress';
UPDATE job_occurrences SET status = 'confirmed' WHERE status = 'in_progress';

ALTER TABLE jobs DROP CONSTRAINT jobs_status_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_status_check
    CHECK (status IN ('unconfirmed', 'confirmed', 'completed', 'canceled', 'terminate_service'));

ALTER TABLE job_occurrences DROP CONSTRAINT job_occurrences_status_check;
ALTER TABLE job_occurrences ADD CONSTRAINT job_occurrences_status_check
    CHECK (status IN ('unconfirmed', 'confirmed', 'completed', 'canceled', 'terminate_service', 'rescheduled'));

-- Applied inside a tenant schema (search_path already set by the runner).

CREATE TABLE jobs (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    date        date        NOT NULL,
    status      text        NOT NULL DEFAULT 'unconfirmed'
        CHECK (status IN ('unconfirmed', 'confirmed', 'in_progress', 'completed', 'canceled', 'terminate_service')),
    recurrence  jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE job_occurrences (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id            uuid        NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    occurrence_date   date        NOT NULL,
    status            text        NOT NULL
        CHECK (status IN ('unconfirmed', 'confirmed', 'in_progress', 'completed', 'canceled', 'terminate_service', 'rescheduled')),
    rescheduled_to    date,
    rescheduled_from  date,
    completed_at      timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT job_occurrences_job_date_key UNIQUE (job_id, occurrence_date)
);

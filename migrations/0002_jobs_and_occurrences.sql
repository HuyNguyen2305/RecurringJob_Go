-- Applied inside a tenant schema (search_path already set by the runner).
-- Jobs and their stored occurrences. Differences from the Sequelize schema,
-- on purpose: statuses are text + CHECK instead of Postgres enums, and the
-- technician / notification columns are plain nullable uuids (no technician
-- table here yet).

CREATE TABLE jobs (
    id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id               uuid    NOT NULL REFERENCES customers (id) ON DELETE RESTRICT,
    location_id               uuid    NOT NULL REFERENCES locations (id) ON DELETE RESTRICT,
    service_type_id           uuid    NOT NULL REFERENCES service_types (id) ON DELETE RESTRICT,
    date                      date    NOT NULL,
    start_time                time    NOT NULL,
    length_minutes            integer NOT NULL CHECK (length_minutes BETWEEN 1 AND 1440),
    time_window_start         time,
    time_window_end           time,
    sold_by_technician_id     uuid,
    status                    text    NOT NULL DEFAULT 'unconfirmed'
        CHECK (status IN ('unconfirmed', 'confirmed', 'in_progress', 'completed', 'canceled', 'terminate_service')),
    is_locked                 boolean NOT NULL DEFAULT false,
    notify_technician         boolean NOT NULL DEFAULT false,
    notify_customer           boolean NOT NULL DEFAULT false,
    notification_template_id  uuid,
    recurrence                jsonb,
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX jobs_customer_id ON jobs (customer_id);
CREATE INDEX jobs_location_id ON jobs (location_id);
CREATE INDEX jobs_date ON jobs (date);

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
    CONSTRAINT job_occurrences_job_date_key UNIQUE (job_id, occurrence_date),
    -- The same consistency rules the Sequelize schema enforces.
    CONSTRAINT job_occurrences_completed_at_check CHECK ((status = 'completed') = (completed_at IS NOT NULL)),
    CONSTRAINT job_occurrences_rescheduled_to_check CHECK ((status = 'rescheduled') = (rescheduled_to IS NOT NULL)),
    CONSTRAINT job_occurrences_rescheduled_from_check CHECK (rescheduled_from IS NULL OR rescheduled_from < occurrence_date)
);

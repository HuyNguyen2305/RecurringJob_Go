-- Applied inside a tenant schema (search_path already set by the runner).
-- One row of settings per tenant. The time zone decides what "today" means
-- (e.g. the earliest date a job can be approved for); dates elsewhere stay
-- plain calendar dates.

CREATE TABLE tenant_settings (
    id          smallint    PRIMARY KEY CHECK (id = 1),
    timezone    text        NOT NULL DEFAULT 'UTC' CHECK (timezone <> ''),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

INSERT INTO tenant_settings (id) VALUES (1);

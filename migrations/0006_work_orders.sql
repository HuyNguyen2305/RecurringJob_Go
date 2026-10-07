-- Applied inside a tenant schema (search_path already set by the runner).
-- A work order is the task sheet for one occurrence (visit) of a job. Like an
-- invoice it takes its customer, location and service type from the job and
-- keeps a snapshot of the job; unlike one it carries tasks, not prices.

CREATE SEQUENCE work_order_number_seq;

CREATE TABLE work_orders (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    number           text        NOT NULL,
    status           text        NOT NULL CHECK (status IN ('draft', 'scheduled', 'in_progress', 'completed', 'canceled')),
    job_id           uuid        NOT NULL REFERENCES jobs (id) ON DELETE RESTRICT,
    occurrence_date  date        NOT NULL,
    job_snapshot     jsonb       NOT NULL,
    customer_id      uuid        NOT NULL REFERENCES customers (id) ON DELETE RESTRICT,
    location_id      uuid        NOT NULL REFERENCES locations (id) ON DELETE RESTRICT,
    service_type_id  uuid        NOT NULL REFERENCES service_types (id) ON DELETE RESTRICT,
    notes            text        NOT NULL DEFAULT '',
    completed_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT work_orders_number_key UNIQUE (number),
    CONSTRAINT work_orders_completed_at_check CHECK ((status = 'completed') = (completed_at IS NOT NULL))
);

-- The number comes from the database, through the sequence of the schema of
-- the table being inserted into, so each tenant counts on its own.
CREATE FUNCTION work_orders_set_number() RETURNS trigger AS $$
BEGIN
    IF NEW.number IS NULL OR NEW.number = '' THEN
        NEW.number := 'WO-' || lpad(nextval((quote_ident(TG_TABLE_SCHEMA) || '.work_order_number_seq')::regclass)::text, 6, '0');
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER work_orders_number
    BEFORE INSERT ON work_orders
    FOR EACH ROW EXECUTE FUNCTION work_orders_set_number();

-- One live work order per occurrence; a canceled one frees it for a new one.
CREATE UNIQUE INDEX work_orders_job_date_key
    ON work_orders (job_id, occurrence_date) WHERE status <> 'canceled';

CREATE INDEX work_orders_customer_id ON work_orders (customer_id);
CREATE INDEX work_orders_status_idx ON work_orders (status, created_at DESC);

CREATE TABLE work_order_tasks (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    work_order_id  uuid    NOT NULL REFERENCES work_orders (id) ON DELETE CASCADE,
    position       integer NOT NULL CHECK (position >= 0),
    description    text    NOT NULL,
    done           boolean NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT work_order_tasks_order_position_key UNIQUE (work_order_id, position)
);

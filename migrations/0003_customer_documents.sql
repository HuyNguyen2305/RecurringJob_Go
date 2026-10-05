-- Applied inside a tenant schema (search_path already set by the runner).
-- Estimates and invoices share one table, told apart by type. Every document
-- belongs to a customer, a location and a service type. Differences from the
-- Sequelize schema, on purpose: statuses are text + CHECK, invoices also have
-- 'void', and money is stored as integer cents.

CREATE TABLE customer_documents (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    type             text        NOT NULL CHECK (type IN ('estimate', 'invoice')),
    status           text        NOT NULL,
    customer_id      uuid        NOT NULL REFERENCES customers (id) ON DELETE RESTRICT,
    location_id      uuid        NOT NULL REFERENCES locations (id) ON DELETE RESTRICT,
    service_type_id  uuid        NOT NULL REFERENCES service_types (id) ON DELETE RESTRICT,
    notes            text        NOT NULL DEFAULT '',
    job_id           uuid        REFERENCES jobs (id) ON DELETE RESTRICT,
    job_snapshot     jsonb,
    occurrence_date  date,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT customer_documents_status_per_type CHECK (
        (type = 'estimate' AND status IN ('draft', 'sent', 'approved', 'declined'))
        OR (type = 'invoice' AND status IN ('draft', 'sent', 'paid', 'void'))
    ),
    CONSTRAINT customer_documents_invoice_requires_job CHECK (
        type <> 'invoice'
        OR (job_id IS NOT NULL AND job_snapshot IS NOT NULL AND occurrence_date IS NOT NULL)
    ),
    CONSTRAINT customer_documents_estimate_shape CHECK (
        type <> 'estimate'
        OR (
            occurrence_date IS NULL
            AND (job_id IS NULL) = (job_snapshot IS NULL)
            AND (job_id IS NULL OR status = 'approved')
        )
    )
);

-- One estimate per job; one live (non-void) invoice per occurrence, so a
-- voided invoice frees its occurrence for a new one.
CREATE UNIQUE INDEX customer_documents_estimate_job_key
    ON customer_documents (job_id) WHERE type = 'estimate' AND job_id IS NOT NULL;
CREATE UNIQUE INDEX customer_documents_invoice_job_date_key
    ON customer_documents (job_id, occurrence_date) WHERE type = 'invoice' AND status <> 'void';

CREATE INDEX customer_documents_customer_id ON customer_documents (customer_id);
CREATE INDEX customer_documents_type_status_idx ON customer_documents (type, status, created_at DESC);

-- Line items of either document type; the parent's type says which.
CREATE TABLE customer_line_items (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id         uuid    NOT NULL REFERENCES customer_documents (id) ON DELETE CASCADE,
    position          integer NOT NULL CHECK (position >= 0),
    description       text    NOT NULL,
    quantity          integer NOT NULL CHECK (quantity > 0),
    unit_price_cents  bigint  NOT NULL CHECK (unit_price_cents >= 0),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT customer_line_items_parent_position_key UNIQUE (parent_id, position)
);

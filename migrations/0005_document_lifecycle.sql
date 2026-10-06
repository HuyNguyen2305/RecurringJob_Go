-- Applied inside a tenant schema (search_path already set by the runner).
-- Document numbers, send / pay / refund timestamps, a revision counter with
-- the earlier content of edited estimates, and the 'refunded' invoice status.

ALTER TABLE customer_documents
    ADD COLUMN number       text,
    ADD COLUMN sent_at      timestamptz,
    ADD COLUMN paid_at      timestamptz,
    ADD COLUMN refunded_at  timestamptz,
    ADD COLUMN revision     integer NOT NULL DEFAULT 1 CHECK (revision >= 1);

-- Numbers come from the database so every insert gets one. The sequence is
-- looked up through the schema of the table being inserted into, so each
-- tenant counts on its own.
CREATE SEQUENCE estimate_number_seq;
CREATE SEQUENCE invoice_number_seq;

CREATE FUNCTION customer_documents_set_number() RETURNS trigger AS $$
BEGIN
    IF NEW.number IS NULL THEN
        IF NEW.type = 'estimate' THEN
            NEW.number := 'EST-' || lpad(nextval((quote_ident(TG_TABLE_SCHEMA) || '.estimate_number_seq')::regclass)::text, 6, '0');
        ELSE
            NEW.number := 'INV-' || lpad(nextval((quote_ident(TG_TABLE_SCHEMA) || '.invoice_number_seq')::regclass)::text, 6, '0');
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER customer_documents_number
    BEFORE INSERT ON customer_documents
    FOR EACH ROW EXECUTE FUNCTION customer_documents_set_number();

-- Rows that exist already get numbers in creation order.
UPDATE customer_documents d
   SET number = CASE d.type WHEN 'estimate' THEN 'EST-' ELSE 'INV-' END || lpad(x.n::text, 6, '0')
  FROM (SELECT id, row_number() OVER (PARTITION BY type ORDER BY created_at, id) AS n FROM customer_documents) x
 WHERE x.id = d.id;
SELECT setval('estimate_number_seq', count(*)) FROM customer_documents WHERE type = 'estimate' HAVING count(*) > 0;
SELECT setval('invoice_number_seq', count(*)) FROM customer_documents WHERE type = 'invoice' HAVING count(*) > 0;

ALTER TABLE customer_documents ALTER COLUMN number SET NOT NULL;
ALTER TABLE customer_documents ADD CONSTRAINT customer_documents_number_key UNIQUE (type, number);

-- Existing paid invoices were paid at their last change.
UPDATE customer_documents SET paid_at = updated_at WHERE type = 'invoice' AND status = 'paid';

ALTER TABLE customer_documents DROP CONSTRAINT customer_documents_status_per_type;
ALTER TABLE customer_documents ADD CONSTRAINT customer_documents_status_per_type CHECK (
    (type = 'estimate' AND status IN ('draft', 'sent', 'approved', 'declined'))
    OR (type = 'invoice' AND status IN ('draft', 'sent', 'paid', 'refunded', 'void'))
);
ALTER TABLE customer_documents ADD CONSTRAINT customer_documents_refunded_at_check
    CHECK ((status = 'refunded') = (refunded_at IS NOT NULL));
ALTER TABLE customer_documents ADD CONSTRAINT customer_documents_paid_at_check
    CHECK ((status IN ('paid', 'refunded')) = (paid_at IS NOT NULL));

-- A refunded invoice frees its occurrence for a new invoice, like a void one.
DROP INDEX customer_documents_invoice_job_date_key;
CREATE UNIQUE INDEX customer_documents_invoice_job_date_key
    ON customer_documents (job_id, occurrence_date)
    WHERE type = 'invoice' AND status NOT IN ('void', 'refunded');

-- What an estimate said before each edit of it (once it was sent).
CREATE TABLE customer_document_revisions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id  uuid        NOT NULL REFERENCES customer_documents (id) ON DELETE CASCADE,
    revision     integer     NOT NULL CHECK (revision >= 1),
    content      jsonb       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT customer_document_revisions_document_revision_key UNIQUE (document_id, revision)
);

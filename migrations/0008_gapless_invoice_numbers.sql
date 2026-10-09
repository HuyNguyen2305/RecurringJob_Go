-- Applied inside a tenant schema (search_path already set by the runner).
-- Invoice numbers must have no gaps, so they stop coming from a sequence
-- (which skips a number whenever an insert fails, and whose numbers would go
-- missing when a draft is deleted). An invoice now gets its number from a
-- counter row the moment it is issued (sent); a draft has none. The counter
-- row is locked until the transaction ends, so a send that rolls back gives its
-- number back and two sends never get the same one. Estimates keep their
-- sequence.

CREATE TABLE document_counters (
    kind        text   PRIMARY KEY,
    last_value  bigint NOT NULL CHECK (last_value >= 0)
);

-- Carry on after the highest invoice number already given.
INSERT INTO document_counters (kind, last_value)
SELECT 'invoice', COALESCE(max(substring(number FROM 5)::bigint), 0)
  FROM customer_documents
 WHERE type = 'invoice' AND number ~ '^INV-[0-9]+$';

-- Estimates are numbered on insert, as before; invoices no longer are.
CREATE OR REPLACE FUNCTION customer_documents_set_number() RETURNS trigger AS $$
BEGIN
    IF NEW.number IS NULL AND NEW.type = 'estimate' THEN
        NEW.number := 'EST-' || lpad(nextval((quote_ident(TG_TABLE_SCHEMA) || '.estimate_number_seq')::regclass)::text, 6, '0');
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- An invoice that is neither a draft nor void has been issued and needs its
-- number. The counter is found through the schema of the table being changed,
-- so each tenant counts on its own.
CREATE FUNCTION customer_documents_set_invoice_number() RETURNS trigger AS $$
DECLARE
    issued bigint;
BEGIN
    -- An issued number is final: changing or clearing it would leave a hole.
    IF TG_OP = 'UPDATE' AND OLD.type = 'invoice' AND OLD.number IS NOT NULL AND NEW.number IS DISTINCT FROM OLD.number THEN
        RAISE EXCEPTION 'the number of invoice % cannot be changed', OLD.number USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.type = 'invoice' AND NEW.number IS NULL AND NEW.status NOT IN ('draft', 'void') THEN
        EXECUTE format('UPDATE %I.document_counters SET last_value = last_value + 1 WHERE kind = %L RETURNING last_value', TG_TABLE_SCHEMA, 'invoice')
           INTO issued;
        NEW.number := 'INV-' || lpad(issued::text, 6, '0');
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER customer_documents_invoice_number
    BEFORE INSERT OR UPDATE ON customer_documents
    FOR EACH ROW EXECUTE FUNCTION customer_documents_set_invoice_number();

-- Only a draft invoice, or a void one that was never issued, may have no number.
ALTER TABLE customer_documents ALTER COLUMN number DROP NOT NULL;
ALTER TABLE customer_documents ADD CONSTRAINT customer_documents_number_required CHECK (
    (type = 'estimate' AND number IS NOT NULL)
    OR (type = 'invoice' AND (number IS NOT NULL OR status IN ('draft', 'void')))
);

-- Nothing uses the old invoice sequence any more.
DROP SEQUENCE invoice_number_seq;

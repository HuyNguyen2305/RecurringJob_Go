-- Applied inside a tenant schema (search_path already set by the runner).
-- Reference data every job and document points at. The columns mirror the
-- customers, locations and service_types tables of the Sequelize-managed
-- schema, so existing rows can be copied across as they are.

CREATE TABLE customers (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        varchar(255) NOT NULL,
    email       varchar(255),
    phone       varchar(50),
    created_at  timestamptz  NOT NULL DEFAULT now(),
    updated_at  timestamptz  NOT NULL DEFAULT now()
);

CREATE TABLE locations (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id    uuid         NOT NULL REFERENCES customers (id) ON DELETE RESTRICT,
    address_line1  varchar(255) NOT NULL,
    city           varchar(100),
    state          varchar(100),
    zip            varchar(20),
    created_at     timestamptz  NOT NULL DEFAULT now(),
    updated_at     timestamptz  NOT NULL DEFAULT now()
);

CREATE INDEX locations_customer_id ON locations (customer_id);

CREATE TABLE service_types (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name         varchar(255) NOT NULL,
    description  text,
    created_at   timestamptz  NOT NULL DEFAULT now(),
    updated_at   timestamptz  NOT NULL DEFAULT now()
);

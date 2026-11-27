CREATE DATABASE sample_db2 OWNER sample_app;
\connect sample_db2
SET ROLE sample_app;

CREATE TABLE products (
    id         SERIAL PRIMARY KEY,
    sku        TEXT          NOT NULL UNIQUE,
    name       TEXT          NOT NULL,
    price      NUMERIC(12,2) NOT NULL CHECK (price >= 0),
    created_at TIMESTAMPTZ   NOT NULL DEFAULT now()
);

INSERT INTO products (sku, name, price) VALUES
    ('SKU-001', 'Keyboard', 350000),
    ('SKU-002', 'Mouse', 150000),
    ('SKU-003', 'Monitor', 2100000);

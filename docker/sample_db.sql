CREATE ROLE sample_app LOGIN PASSWORD 'sample_app';
CREATE DATABASE sample_db OWNER sample_app;
\connect sample_db
SET ROLE sample_app;

CREATE TABLE customers (
    id         SERIAL PRIMARY KEY,
    name       TEXT        NOT NULL,
    email      TEXT        NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE orders (
    id          SERIAL PRIMARY KEY,
    customer_id INTEGER       NOT NULL REFERENCES customers (id),
    total       NUMERIC(12,2) NOT NULL CHECK (total >= 0),
    status      TEXT          NOT NULL DEFAULT 'pending',
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now()
);
CREATE INDEX orders_customer_idx ON orders (customer_id);

INSERT INTO customers (name, email) VALUES
    ('Andi', 'andi@example.com'),
    ('Bunga', 'bunga@example.com'),
    ('Citra', 'citra@example.com');
INSERT INTO orders (customer_id, total, status) VALUES
    (1, 150000, 'paid'), (1, 72000, 'pending'), (2, 310500, 'paid'), (3, 45000, 'cancelled');

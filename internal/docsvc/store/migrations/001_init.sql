CREATE TABLE documents (
    id             uuid PRIMARY KEY,
    title          text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    attachment_key text NOT NULL UNIQUE,
    payment_status text NOT NULL DEFAULT 'pending'
                   CHECK (payment_status IN ('pending', 'paid', 'failed')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- Drill canary: one row per acknowledged client write, used to measure real RPO.
CREATE TABLE canary (
    seq        bigint PRIMARY KEY,
    written_at timestamptz NOT NULL DEFAULT now()
);

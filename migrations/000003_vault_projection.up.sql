-- Distinguish rows synchronized from the encrypted web document from rows
-- created through the resource API. A vault save replaces only its own rows.
ALTER TABLE categories ADD COLUMN sync_origin TEXT NOT NULL DEFAULT 'api' CHECK (sync_origin IN ('api','vault'));
ALTER TABLE expenses ADD COLUMN sync_origin TEXT NOT NULL DEFAULT 'api' CHECK (sync_origin IN ('api','vault'));
ALTER TABLE income ADD COLUMN sync_origin TEXT NOT NULL DEFAULT 'api' CHECK (sync_origin IN ('api','vault'));
ALTER TABLE budgets ADD COLUMN sync_origin TEXT NOT NULL DEFAULT 'api' CHECK (sync_origin IN ('api','vault'));
ALTER TABLE loans ADD COLUMN sync_origin TEXT NOT NULL DEFAULT 'api' CHECK (sync_origin IN ('api','vault'));
ALTER TABLE goals ADD COLUMN sync_origin TEXT NOT NULL DEFAULT 'api' CHECK (sync_origin IN ('api','vault'));
ALTER TABLE planned_payments ADD COLUMN sync_origin TEXT NOT NULL DEFAULT 'api' CHECK (sync_origin IN ('api','vault'));

ALTER TABLE planned_payments DROP COLUMN sync_origin;
ALTER TABLE goals DROP COLUMN sync_origin;
ALTER TABLE loans DROP COLUMN sync_origin;
ALTER TABLE budgets DROP COLUMN sync_origin;
ALTER TABLE income DROP COLUMN sync_origin;
ALTER TABLE expenses DROP COLUMN sync_origin;
ALTER TABLE categories DROP COLUMN sync_origin;

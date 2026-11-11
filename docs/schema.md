# Data model

`users` owns all finance records and settings. `user_sessions` and `refresh_tokens` hold session metadata and SHA-256 token hashes; `email_verifications` and `password_resets` hold one-use token hashes. `audit_events` records security actions.

`categories` classifies `expenses`; `income` records earned or borrowed inflows. `loans` and `goals` can be linked from expenses; `planned_payments` records monthly obligations and has per-month overrides and skips. `budgets` attach to categories through `budget_categories`. `savings_accounts` and `savings_transactions` track balances. `subscriptions`, `payments` and `webhook_events` support billing. `vaults` stores ciphertext and its version.

Every finance table has an owner key or an owner-constrained foreign key. `sync_origin` distinguishes rows from the browser vault from direct API rows. Amount columns use `BIGINT` minor units; currency columns use three-letter codes. SQL aggregations group by currency. Indexes on owner and date support activity lists and reports. The versioned migrations in `migrations/` are the schema source of truth; sqlc-generated Go files are derived from them and `queries/`.

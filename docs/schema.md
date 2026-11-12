# MongoDB data model

`users` owns all finance records and settings. `user_sessions` and `refresh_tokens` hold session metadata and SHA-256 token hashes; `email_verifications` and `password_resets` hold one-use token hashes. `audit_events` records security actions.

`categories` classifies `expenses`; `income` records earned or borrowed inflows. `loans` and `goals` can be linked from expenses; `planned_payments` records monthly obligations and has per-month overrides and skips. `budgets` attach to categories through `budget_categories`. `savings_accounts` and `savings_transactions` track balances. `subscriptions`, `payments` and `webhook_events` support billing. `vaults` stores ciphertext and its version.

Documents use UUID strings for IDs and `user_id`, except payments and token collections, whose natural keys identify documents. Date-only values use `YYYY-MM-DD` strings; timestamps use BSON dates. Amounts are signed 64-bit integer minor units and currencies use three-letter codes. `sync_origin` distinguishes vault projections from direct API records. The application enforces owner-scoped references and creates unique and query indexes in `internal/infrastructure/mongodb/mongodb.go` at startup. MongoDB transactions protect vault updates, session rotation, savings withdrawals and billing state.

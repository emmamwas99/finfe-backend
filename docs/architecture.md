# Architecture and migration

The Go API is a modular monolith: `cmd/api` composes configuration, PostgreSQL, mail and Chi routes; `internal/domain` contains pure money and health calculations; `internal/infrastructure` contains pgx/sqlc, security, Redis rate limiting, email and Paystack; `internal/delivery/httpapi` validates requests and enforces ownership.

The existing Next.js app uses one encrypted document (`vaults`) for offline editing, recovery and cross-device merge. In Go mode, the client sends the corresponding readable `finance` document with each data change. A version check, vault write and replacement of only `sync_origin='vault'` rows happen in one PostgreSQL transaction. A conflict rolls back both representations. The resource API writes `sync_origin='api'` rows, which the vault sync leaves alone. The frontend currently edits the vault and reads server totals on Reports; resource hooks are available for future direct editors. Direct resource API writes do not appear in the legacy vault UI.

The readable projection is necessary for server SQL reports. It changes the previous privacy model: the server can read these finance rows. The public privacy text and product copy disclose this in Go mode. The encrypted vault remains a backup and offline source. Existing Supabase mode continues to use encrypted vault sync only.

Money crosses the API as decimal values in the legacy browser document, then is parsed exactly as rational numbers and stored as `BIGINT` minor units. New resource endpoints accept integer minor units directly. A projection rejects values finer than one cent. Dates selected in the UI are stored as PostgreSQL `DATE`; timestamps use `TIMESTAMPTZ`. User timezone determines default reporting months. PostgreSQL constraints and composite owner foreign keys protect cross-user references. Reports group by currency.

The initial migration creates the normalized tables. Migration 2 adds the encrypted vault. Migration 3 adds the sync-origin marker. Run migrations before deployment; database backups should precede production upgrades.

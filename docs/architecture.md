# Architecture

The Go API is a modular monolith: `cmd/api` composes configuration, MongoDB, mail and Chi routes; `internal/domain` contains pure money and health calculations; `internal/infrastructure` contains persistence, security, Redis rate limiting, email and Paystack; `internal/delivery/httpapi` validates requests and enforces ownership.

The existing Next.js app uses one encrypted document (`vaults`) for offline editing, recovery and cross-device merge. In Go mode, the client sends the corresponding readable `finance` document with each data change. A version check, vault write and replacement of only `sync_origin='vault'` documents happen in one MongoDB transaction. A conflict rolls back both representations. The resource API writes `sync_origin='api'` documents, which the vault sync leaves alone. The frontend currently edits the vault and reads server totals on Reports; resource hooks are available for future direct editors. Direct resource API writes do not appear in the legacy vault UI.

The readable projection is necessary for server reports. It changes the previous privacy model: the server can read these finance records. The encrypted vault remains a backup and offline source. Existing Supabase mode continues to use encrypted vault sync only.

Money crosses the API as decimal values in the legacy browser document, then is parsed exactly as rational numbers and stored as integer minor units. New resource endpoints accept integer minor units directly. A projection rejects values finer than one cent. Dates selected in the UI use ISO date strings; timestamps use BSON dates. User timezone determines default reporting months. Owner-scoped checks protect cross-user references. Reports group by currency.

MongoDB must run as a replica set for the multi-document transactions. The local Compose service starts a single-node replica set for development. Use a managed replica set in production. The database starts empty.

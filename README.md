# Finfe Go backend

Standalone Go API for the adjacent `finance-freedom` Next.js app. The app keeps an encrypted browser vault for offline use and recovery. In Go mode, each vault save also writes readable finance rows to PostgreSQL in the same transaction so the server can calculate reports. This is a deliberate privacy change from the former Supabase-only mode; see [architecture](docs/architecture.md).

## Run locally

1. Start PostgreSQL, Redis and the API with `docker compose up --build` from this folder. The API serves `http://localhost:8080/health` and `/ready`.
2. In `../finance-freedom`, set `NEXT_PUBLIC_FINFE_API_URL=http://localhost:8080` in `.env.local`, run `npm install`, then `npm run dev`.
3. Sign up. In development, verification and reset links are printed in the API logs. Email verification is on by default. Use the link, then log in. Checkout uses a local simulation unless Paystack keys are configured and `BILLING_DEMO=false`.

If running Go outside Compose, copy `.env.example` to `.env`, load it into the shell, and run `make migrate-up && make dev`. The API itself applies embedded migrations when `MIGRATE_ON_START=true`.

## Commands

```sh
make test            # Go unit and handler tests
make race            # race detector
make vet             # static checks
make sqlc            # regenerate typed SQL after changing migrations or queries
make migrate-up      # apply embedded migrations
make migrate-down    # revert one migration (local development only)
```

The API contract is [api/openapi.yaml](api/openapi.yaml). Regenerate frontend types from `../finance-freedom` with `npm run api:types`. Routes use `/api/v1`. Authentication uses HttpOnly access and rotating refresh cookies, CORS allowlists and an Origin check for cookie writes. All finance queries filter by authenticated user ID; money is stored in integer minor units with explicit currency. Reports never combine currencies.

See [schema](docs/schema.md) and [financial health formulas](docs/financial-health.md). Production needs a strong `JWT_SECRET`, HTTPS, `COOKIE_SECURE=true`, SMTP, a managed PostgreSQL database, appropriate CORS origins and real Paystack keys. Keep `BILLING_DEMO=false` in production.

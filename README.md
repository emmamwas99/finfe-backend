# Finfe Go backend

Standalone Go API for the adjacent `finance-freedom` Next.js app. MongoDB stores accounts, finance records, billing state and the encrypted browser vault. Each vault save updates its readable finance projection in a MongoDB transaction so the server can calculate reports. See [architecture](docs/architecture.md).

## Run locally

1. Start MongoDB, Redis and the API with `docker compose up --build` from this folder. The API serves `http://localhost:8080/health` and `/ready`.
2. In `../finance-freedom`, set `NEXT_PUBLIC_FINFE_API_URL=http://localhost:8080` in `.env.local`, run `npm install`, then `npm run dev`.
3. Sign up. In development, verification and reset links are printed in the API logs. Email verification is on by default. Use the link, then log in. Checkout uses a local simulation unless Paystack keys are configured and `BILLING_DEMO=false`.

If running Go outside Compose, copy `.env.example` to `.env`, load it into the shell, start a MongoDB replica set, and run `make dev`. That command rebuilds and restarts the API when Go source, `go.mod`, or `go.sum` changes. You can also run `./scripts/dev.sh` directly. The API creates its required indexes at startup. `make mongo-setup` can create them separately. The MongoDB database starts empty.

## Commands

```sh
make dev             # run API with automatic reload on file changes
make test            # Go unit and handler tests
make race            # race detector
make vet             # static checks
make mongo-setup     # create MongoDB indexes
```

The API contract is [api/openapi.yaml](api/openapi.yaml). Regenerate frontend types from `../finance-freedom` with `npm run api:types`. Routes use `/api/v1`. Authentication uses HttpOnly access and rotating refresh cookies, CORS allowlists and an Origin check for cookie writes. All finance queries filter by authenticated user ID; money is stored in integer minor units with explicit currency. Reports never combine currencies.

See [schema](docs/schema.md) and [financial health formulas](docs/financial-health.md). Production needs a strong `JWT_SECRET`, HTTPS, `COOKIE_SECURE=true`, SMTP, a managed MongoDB replica set, appropriate CORS origins and real Paystack keys. Keep `BILLING_DEMO=false` in production.

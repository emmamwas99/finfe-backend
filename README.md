# Finfe Go backend

Standalone Go API for the adjacent `finance-web` Next.js app. MongoDB stores accounts, finance records, billing state and the encrypted browser vault. Each vault save updates its readable finance projection in a MongoDB transaction so the server can calculate reports. See [architecture](docs/architecture.md).

## Run locally

1. For MongoDB Atlas, put its URI and `MONGODB_DATABASE=finfe_development_db` in this repository's ignored `.env.local`. For a bundled local MongoDB and Redis instead, run `docker compose up --build` from this folder.
2. In `../finance-web`, set `NEXT_PUBLIC_FINFE_API_URL=http://localhost:8080` in `.env.local`, run `npm install`, then `npm run dev`. That command starts this API when it is not already running. Check `http://localhost:8080/health` and `/ready`.
3. Sign up, follow the confirmation link in your email, and log in. Existing accounts that need confirmation can request a fresh link on the login screen. Checkout uses a local simulation unless Paystack keys are configured and `BILLING_DEMO=false`.

If running Go outside Compose, put private settings in the ignored `.env.local` and run `make dev`. The dev script loads the tracked `.env` defaults, then `.env.local`, and rebuilds on Go source changes. For MongoDB Atlas, set `MONGODB_URI` and `MONGODB_DATABASE=finfe_development_db` in `.env.local`; a local MongoDB server is unnecessary. The API creates its required indexes at startup. Redis is optional in local development because rate limits fall back to process memory.

For live transactional email, configure `MAIL_DRIVER=smtp`, `SMTP_HOST`, `SMTP_PORT`, `SMTP_SECURE`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `MAIL_FROM`, and `REQUIRE_EMAIL_VERIFICATION=true` in `.env.local`. Set `SMTP_SECURE=true` for implicit TLS on port 465, or `false` for STARTTLS (typically port 587). Signup confirmation and password reset are encrypted in MongoDB's `mail_outbox` before the API replies; a background worker sends them and retries temporary SMTP failures. Keep `JWT_SECRET` stable so queued mail can be decrypted after a restart. Keep credentials out of the tracked `.env`. If you use the log driver for isolated development, confirmation links appear in API logs instead. The current local sender uses the existing Shopi mailbox with a Finfe display name until a dedicated Finfe sender domain is configured.

## Commands

```sh
make dev             # run API with automatic reload on file changes
make test            # Go unit and handler tests
make race            # race detector
make vet             # static checks
make mongo-setup     # create MongoDB indexes
```

The API contract is [api/openapi.yaml](api/openapi.yaml). Regenerate frontend types from `../finance-web` with `npm run api:types`. Routes use `/api/v1`. Authentication uses HttpOnly access and rotating refresh cookies, CORS allowlists and an Origin check for cookie writes. All finance queries filter by authenticated user ID; money is stored in integer minor units with explicit currency. Reports never combine currencies.

See [schema](docs/schema.md) and [financial health formulas](docs/financial-health.md). Production needs a strong `JWT_SECRET`, HTTPS, `COOKIE_SECURE=true`, SMTP, a managed MongoDB replica set, appropriate CORS origins and real Paystack keys. Keep `BILLING_DEMO=false` in production.

-- =====================================================================
-- Finfe: initial schema
--
-- Conventions
--   * Money: BIGINT minor units (cents) + an explicit ISO-4217 currency. Never floats.
--   * Rates and thresholds: integer basis points (1600 = 16.00%).
--   * Timestamps: TIMESTAMPTZ (stored in UTC). Calendar dates the user picks: DATE.
--   * Months: DATE pinned to the 1st of the month.
--   * Ownership: every financial row carries user_id. Composite foreign keys
--     (user_id, x_id) → (user_id, id) make it impossible for one user's row to
--     reference another user's category, loan, goal or account.
-- =====================================================================

CREATE TABLE users (
    id                    UUID PRIMARY KEY,
    email                 TEXT NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254),
    password_hash         TEXT NOT NULL,
    name                  TEXT NOT NULL DEFAULT '' CHECK (char_length(name) <= 100),
    email_verified_at     TIMESTAMPTZ,
    currency              CHAR(3) NOT NULL DEFAULT 'KES' CHECK (currency ~ '^[A-Z]{3}$'),
    country               CHAR(2) NOT NULL DEFAULT 'KE' CHECK (country ~ '^[A-Z]{2}$'),
    timezone              TEXT NOT NULL DEFAULT 'Africa/Nairobi',
    language              TEXT NOT NULL DEFAULT 'en' CHECK (char_length(language) BETWEEN 2 AND 10),
    expected_income_minor BIGINT NOT NULL DEFAULT 0 CHECK (expected_income_minor >= 0),
    warn_at_bp            INTEGER NOT NULL DEFAULT 8000 CHECK (warn_at_bp BETWEEN 1000 AND 10000),
    auto_lock_minutes     INTEGER NOT NULL DEFAULT 5 CHECK (auto_lock_minutes BETWEEN 1 AND 1440),
    last_backup_at        TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

-- Expected income: changes "from this month on" and one-month overrides.
CREATE TABLE income_expectations (
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    scope        TEXT NOT NULL CHECK (scope IN ('from', 'only')),
    month        DATE NOT NULL CHECK (EXTRACT(DAY FROM month) = 1),
    amount_minor BIGINT NOT NULL CHECK (amount_minor >= 0),
    PRIMARY KEY (user_id, scope, month)
);

-- ---------------------------------------------------------------------
-- Authentication
-- ---------------------------------------------------------------------
CREATE TABLE user_sessions (
    id           UUID PRIMARY KEY,
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_agent   TEXT NOT NULL DEFAULT '',
    ip           TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    revoked_at   TIMESTAMPTZ
);
CREATE INDEX user_sessions_user_idx ON user_sessions (user_id) WHERE revoked_at IS NULL;

-- Refresh tokens are stored as SHA-256 hashes and rotated on every use.
CREATE TABLE refresh_tokens (
    id         UUID PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES user_sessions (id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX refresh_tokens_session_idx ON refresh_tokens (session_id);

CREATE TABLE email_verifications (
    token_hash BYTEA PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX email_verifications_user_idx ON email_verifications (user_id);

CREATE TABLE password_resets (
    token_hash BYTEA PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX password_resets_user_idx ON password_resets (user_id);

-- Security-relevant events (logins, password changes, deletions, imports, billing).
CREATE TABLE audit_events (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    UUID REFERENCES users (id) ON DELETE SET NULL,
    action     TEXT NOT NULL,
    ip         TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    metadata   JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_events_user_idx ON audit_events (user_id, created_at DESC);

-- ---------------------------------------------------------------------
-- Categories (expense kinds need/want/debt/invest/saving, plus income)
-- ---------------------------------------------------------------------
CREATE TABLE categories (
    id         UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    emoji      TEXT NOT NULL DEFAULT '•' CHECK (char_length(emoji) <= 16),
    kind       TEXT NOT NULL CHECK (kind IN ('need', 'want', 'debt', 'invest', 'saving', 'income')),
    archived   BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, id)
);
CREATE INDEX categories_user_idx ON categories (user_id);

-- ---------------------------------------------------------------------
-- Loans (debts) and goals
-- ---------------------------------------------------------------------
CREATE TABLE loans (
    id                    UUID PRIMARY KEY,
    user_id               UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name                  TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    lender                TEXT NOT NULL DEFAULT '' CHECK (char_length(lender) <= 100),
    principal_minor       BIGINT NOT NULL CHECK (principal_minor > 0), -- amount owed when recorded (+ top-ups)
    currency              CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    interest_rate_bp      INTEGER NOT NULL DEFAULT 0 CHECK (interest_rate_bp BETWEEN 0 AND 100000),
    monthly_payment_minor BIGINT NOT NULL DEFAULT 0 CHECK (monthly_payment_minor >= 0),
    start_date            DATE NOT NULL,
    due_day               SMALLINT CHECK (due_day BETWEEN 1 AND 31),
    closed                BOOLEAN NOT NULL DEFAULT false,
    note                  TEXT NOT NULL DEFAULT '' CHECK (char_length(note) <= 1000),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, id)
);
CREATE INDEX loans_user_idx ON loans (user_id);

CREATE TABLE goals (
    id              UUID PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name            TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    emoji           TEXT NOT NULL DEFAULT '🎯' CHECK (char_length(emoji) <= 16),
    target_minor    BIGINT NOT NULL CHECK (target_minor > 0),
    starting_minor  BIGINT NOT NULL DEFAULT 0 CHECK (starting_minor >= 0),
    currency        CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    target_month    DATE CHECK (target_month IS NULL OR EXTRACT(DAY FROM target_month) = 1),
    note            TEXT NOT NULL DEFAULT '' CHECK (char_length(note) <= 1000),
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, id)
);
CREATE INDEX goals_user_idx ON goals (user_id);

-- ---------------------------------------------------------------------
-- Planned payments: the monthly plan (loans, rent, school fees, house help…)
-- ---------------------------------------------------------------------
CREATE TABLE planned_payments (
    id           UUID PRIMARY KEY,
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency     CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    category_id  UUID NOT NULL,
    due_day      SMALLINT NOT NULL DEFAULT 1 CHECK (due_day BETWEEN 0 AND 31),
    -- months of the year it's due (1–12); empty = every month. Ignored when only_month is set.
    months       SMALLINT[] NOT NULL DEFAULT '{}' CHECK (months <@ ARRAY[1,2,3,4,5,6,7,8,9,10,11,12]::SMALLINT[]),
    only_month   DATE CHECK (only_month IS NULL OR EXTRACT(DAY FROM only_month) = 1),
    from_month   DATE CHECK (from_month IS NULL OR EXTRACT(DAY FROM from_month) = 1),
    loan_id      UUID,
    goal_id      UUID,
    active       BOOLEAN NOT NULL DEFAULT true,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, id),
    FOREIGN KEY (user_id, category_id) REFERENCES categories (user_id, id),
    FOREIGN KEY (user_id, loan_id) REFERENCES loans (user_id, id) ON DELETE SET NULL (loan_id),
    FOREIGN KEY (user_id, goal_id) REFERENCES goals (user_id, id) ON DELETE SET NULL (goal_id)
);
CREATE INDEX planned_payments_user_idx ON planned_payments (user_id);

CREATE TABLE planned_payment_overrides (
    planned_payment_id UUID NOT NULL REFERENCES planned_payments (id) ON DELETE CASCADE,
    month              DATE NOT NULL CHECK (EXTRACT(DAY FROM month) = 1),
    amount_minor       BIGINT NOT NULL CHECK (amount_minor > 0),
    PRIMARY KEY (planned_payment_id, month)
);

CREATE TABLE planned_payment_skips (
    planned_payment_id UUID NOT NULL REFERENCES planned_payments (id) ON DELETE CASCADE,
    month              DATE NOT NULL CHECK (EXTRACT(DAY FROM month) = 1),
    PRIMARY KEY (planned_payment_id, month)
);

-- ---------------------------------------------------------------------
-- Recurring transactions (income or expenses posted automatically)
-- ---------------------------------------------------------------------
CREATE TABLE recurring_transactions (
    id             UUID PRIMARY KEY,
    user_id        UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('expense', 'income')),
    name           TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    amount_minor   BIGINT NOT NULL CHECK (amount_minor > 0),
    currency       CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    category_id    UUID,
    source         TEXT NOT NULL DEFAULT '' CHECK (char_length(source) <= 60),
    frequency      TEXT NOT NULL CHECK (frequency IN ('weekly', 'monthly')),
    day_of_month   SMALLINT CHECK (day_of_month BETWEEN 1 AND 31),
    weekday        SMALLINT CHECK (weekday BETWEEN 0 AND 6),
    start_date     DATE NOT NULL,
    end_date       DATE,
    last_posted_on DATE,
    active         BOOLEAN NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, id),
    FOREIGN KEY (user_id, category_id) REFERENCES categories (user_id, id),
    CHECK (end_date IS NULL OR end_date >= start_date),
    CHECK ((frequency = 'monthly' AND day_of_month IS NOT NULL) OR (frequency = 'weekly' AND weekday IS NOT NULL)),
    CHECK (kind = 'income' OR category_id IS NOT NULL)
);
CREATE INDEX recurring_active_idx ON recurring_transactions (active, start_date) WHERE active;

-- ---------------------------------------------------------------------
-- Expenses and income
-- ---------------------------------------------------------------------
CREATE TABLE expenses (
    id                 UUID PRIMARY KEY,
    user_id            UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    occurred_on        DATE NOT NULL,
    amount_minor       BIGINT NOT NULL CHECK (amount_minor > 0),
    currency           CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    category_id        UUID NOT NULL,
    note               TEXT NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),
    payment_method     TEXT CHECK (payment_method IN ('cash', 'mpesa', 'card', 'bank', 'other')),
    loan_id            UUID,
    goal_id            UUID,
    planned_payment_id UUID,
    recurring_id       UUID,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (user_id, category_id) REFERENCES categories (user_id, id),
    FOREIGN KEY (user_id, loan_id) REFERENCES loans (user_id, id) ON DELETE SET NULL (loan_id),
    FOREIGN KEY (user_id, goal_id) REFERENCES goals (user_id, id) ON DELETE SET NULL (goal_id),
    FOREIGN KEY (user_id, planned_payment_id) REFERENCES planned_payments (user_id, id) ON DELETE SET NULL (planned_payment_id),
    FOREIGN KEY (user_id, recurring_id) REFERENCES recurring_transactions (user_id, id) ON DELETE SET NULL (recurring_id),
    -- a recurring rule posts at most once per date
    UNIQUE (recurring_id, occurred_on)
);
CREATE INDEX expenses_user_date_idx ON expenses (user_id, occurred_on DESC, created_at DESC);
CREATE INDEX expenses_user_category_idx ON expenses (user_id, category_id, occurred_on);
CREATE INDEX expenses_loan_idx ON expenses (loan_id) WHERE loan_id IS NOT NULL;
CREATE INDEX expenses_goal_idx ON expenses (goal_id) WHERE goal_id IS NOT NULL;

CREATE TABLE income (
    id           UUID PRIMARY KEY,
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    occurred_on  DATE NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency     CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    source       TEXT NOT NULL CHECK (char_length(source) BETWEEN 1 AND 60),
    category_id  UUID,
    note         TEXT NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),
    is_borrowed  BOOLEAN NOT NULL DEFAULT false, -- money that came from a loan, not earned
    loan_id      UUID,
    recurring_id UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (user_id, category_id) REFERENCES categories (user_id, id),
    FOREIGN KEY (user_id, loan_id) REFERENCES loans (user_id, id) ON DELETE SET NULL (loan_id),
    FOREIGN KEY (user_id, recurring_id) REFERENCES recurring_transactions (user_id, id) ON DELETE SET NULL (recurring_id),
    UNIQUE (recurring_id, occurred_on)
);
CREATE INDEX income_user_date_idx ON income (user_id, occurred_on DESC, created_at DESC);

-- ---------------------------------------------------------------------
-- Budgets (monthly or weekly, over one or more categories)
-- ---------------------------------------------------------------------
CREATE TABLE budgets (
    id                 UUID PRIMARY KEY,
    user_id            UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name               TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    period             TEXT NOT NULL CHECK (period IN ('monthly', 'weekly')),
    amount_minor       BIGINT NOT NULL CHECK (amount_minor > 0),
    currency           CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    alert_threshold_bp INTEGER NOT NULL DEFAULT 8000 CHECK (alert_threshold_bp BETWEEN 1000 AND 10000),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, id)
);
CREATE INDEX budgets_user_idx ON budgets (user_id);

CREATE TABLE budget_categories (
    budget_id   UUID NOT NULL,
    user_id     UUID NOT NULL,
    category_id UUID NOT NULL,
    PRIMARY KEY (budget_id, category_id),
    FOREIGN KEY (user_id, budget_id) REFERENCES budgets (user_id, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, category_id) REFERENCES categories (user_id, id) ON DELETE CASCADE
);
CREATE INDEX budget_categories_category_idx ON budget_categories (category_id);

-- ---------------------------------------------------------------------
-- Savings accounts and their deposits / withdrawals
-- ---------------------------------------------------------------------
CREATE TABLE savings_accounts (
    id                UUID PRIMARY KEY,
    user_id           UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name              TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    currency          CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    is_emergency_fund BOOLEAN NOT NULL DEFAULT false,
    target_minor      BIGINT CHECK (target_minor IS NULL OR target_minor > 0),
    archived          BOOLEAN NOT NULL DEFAULT false,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, id)
);
CREATE INDEX savings_accounts_user_idx ON savings_accounts (user_id);

CREATE TABLE savings_transactions (
    id           UUID PRIMARY KEY,
    user_id      UUID NOT NULL,
    account_id   UUID NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('deposit', 'withdrawal')),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    occurred_on  DATE NOT NULL,
    note         TEXT NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (user_id, account_id) REFERENCES savings_accounts (user_id, id) ON DELETE CASCADE
);
CREATE INDEX savings_transactions_account_idx ON savings_transactions (account_id, occurred_on DESC);

-- ---------------------------------------------------------------------
-- Billing (Paystack). Written only by the server.
-- ---------------------------------------------------------------------
CREATE TABLE subscriptions (
    user_id                    UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    status                     TEXT NOT NULL DEFAULT 'trialing'
                                 CHECK (status IN ('trialing', 'active', 'non_renewing', 'past_due', 'cancelled', 'expired')),
    trial_ends_at              TIMESTAMPTZ,
    current_period_end         TIMESTAMPTZ,
    billing                    TEXT CHECK (billing IN ('card', 'prepaid')),
    period                     TEXT CHECK (period IN ('monthly', 'yearly')),
    paystack_customer_code     TEXT,
    paystack_subscription_code TEXT,
    paystack_email_token       TEXT,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX subscriptions_customer_idx ON subscriptions (paystack_customer_code);

CREATE TABLE payments (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      UUID REFERENCES users (id) ON DELETE SET NULL,
    reference    TEXT NOT NULL UNIQUE,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency     CHAR(3) NOT NULL DEFAULT 'KES',
    period       TEXT NOT NULL CHECK (period IN ('monthly', 'yearly')),
    method       TEXT,
    channel      TEXT,
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'success', 'failed')),
    paid_at      TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX payments_user_idx ON payments (user_id, created_at DESC);

-- Paystack resends events until it gets a 200: remember what we processed.
CREATE TABLE webhook_events (
    id          TEXT PRIMARY KEY,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

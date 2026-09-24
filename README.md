# sofar

A personal budgeting PWA. Real money, a clear picture, room to breathe.

React + TypeScript + Vite on the front end; Go + Postgres on the back end. The Go module uses Backr's `server` / `internal` package layout and zerolog conventions. No bank credentials or real transactions are bundled.

## Try the interface

```sh
npm ci
npm run dev
```

Open the URL printed by Vite. When no API is running, the interface opens a **clearly labeled demo workspace**, with sample checking, savings, and investment balances. Review decisions and edits persist in IndexedDB on this browser. The demo has no bank connection, does not send push notifications, and does not move money. With a running API, the app opens the account setup or login screen instead; the login screen also offers the demo.

The interface includes overview, searchable/exportable transactions, swipe review with accessible buttons and a skip action, income classification, cross-account partial repayments and unlinking, recurring confirmation/tolerances and reversible pattern dismissal, one editable savings goal, accounts, category names, and settings. It also has a “should i buy this?” comparison and calculators for savings timing, emergency-fund runway, debt payoff, and hypothetical investment growth. Calculators update as you edit their assumptions and do not create transactions or change your saved goal. The three category IDs remain fixed even when their labels change. Demo data can be reset from Settings.

## Run your own instance

Requires Docker Compose, a domain pointing to your host, and a TLS reverse proxy. Alternatively, run Go 1.26+, Node 24, and Postgres 17 directly.

1. Copy `.env.example` to `.env`.
2. Generate **independent** random secrets: a 32-byte base64 encryption key, a setup key of at least 24 characters, and a database password. The database password should be URL-safe because Compose places it in `DATABASE_URL`. This command prints a fresh random 32-byte base64 value:

   ```sh
   node -e "console.log(require('crypto').randomBytes(32).toString('base64'))"
   ```

   For the setup key and database password, use the same command with `'base64url'` instead of `'base64'`. Run it separately for every secret. Keep `.env` private and retain the encryption key in your backup; it is needed to decrypt bank access tokens and authenticator secrets.

3. Set `SOFAR_ORIGIN` to the exact public HTTPS origin, without a trailing slash. Set `TZ` to your budget timezone. Set `TRUST_PROXY=true` only when traffic comes through your trusted reverse proxy. The Compose service publishes the backend on loopback only.
4. Generate your Web Push keypair:

   ```sh
   go run ./server vapid
   ```

   Or build the image and run `docker compose run --rm --no-deps app vapid`. Put both returned keys in `.env`, and set `VAPID_SUBJECT` to your contact `mailto:` address. Keep the private key stable across deployments.

5. Add `PLAID_CLIENT_ID`, `PLAID_SECRET`, and `PLAID_ENV=sandbox`. Switch to `production` only after configuring the appropriate Plaid account/products. The app does not assume any particular Plaid plan or production entitlement.
6. Start the stack:

   ```sh
   docker compose up -d --build
   ```

7. Route your HTTPS domain to `127.0.0.1:8080`; `Caddyfile.example` is a minimal example. The reverse proxy must overwrite `X-Forwarded-Proto`, and the Go port must remain inaccessible directly from the internet. Plain HTTP requests are rejected in production.
8. Open the site. Use `SOFAR_SETUP_KEY` to create the single user with a password of at least 12 characters. The setup endpoint cannot create a second user. Remove the setup key from the environment after enrollment if desired.
9. In Settings, enroll an authenticator app and verify a code. Then connect your accounts and enable notifications on each device.

There are no recovery codes in this version. Retain access to your authenticator and server/database backups. To recover a lost authenticator as the server owner, clear `users.totp_secret`, `users.totp_pending`, and `users.totp_last_step` directly in Postgres, delete all sessions, and enroll again after signing in with the password. This is a server administration procedure, not a public bypass endpoint.

## Local full-stack development

Provide the environment variables from `.env` to the Go process; **Go does not automatically load `.env`**. On PowerShell:

```powershell
Get-Content .env | ForEach-Object {
  if ($_ -match '^([A-Z_]+)=(.*)$') {
    [Environment]::SetEnvironmentVariable($matches[1], $matches[2], 'Process')
  }
}
$env:SOFAR_ENV = 'development'
$env:SOFAR_ORIGIN = 'http://127.0.0.1:5173'
$env:TRUST_PROXY = 'false'
go run ./server
```

Run `npm run dev` in another terminal and use **http://127.0.0.1:5173** (the origin must match). Point `DATABASE_URL` at your local Postgres. For a production frontend locally, `npm run build`, set `SOFAR_ORIGIN=http://localhost:8080` and `SOFAR_ENV=development`, and visit the Go server at that origin. Development explicitly allows HTTP with a non-Secure session cookie; production does not.

## How the numbers work

- Amounts in the database, domain logic, API, and IndexedDB are integer USD cents. Provider decimal values are rounded to cents at ingestion.
- Income uses the **three previous completed calendar months**, excluding the current partial month. Each income stream is summed and divided by three, including zero-income months; confirmed salary and self-employment averages stay separate.
- Only posted, confirmed incoming transactions explicitly classified into one of those two income streams are income. Transfers and linked repayments never contribute to the baseline. A partially linked incoming credit is excluded in full from income; only the explicitly linked amount reduces the expense. Split treatment of the rest is outside MVP scope.
- **Safe to spend = salary average + self-employment average − confirmed recurring Expenses − monthly savings contribution.** Weekly commitments use 52/12, biweekly use 26/12, annual use 1/12. Negative results remain visible.
- This is the spec's **monthly planning allowance**, not a running bank balance or remaining discretionary balance. Actual discretionary spending appears in the Spending bucket; it is not subtracted a second time from the headline formula. Historical month navigation recalculates the income window and category totals using the **current** commitments and goal configuration.
- The dashboard's **per-day guide** divides the monthly allowance by the number of calendar days in the selected month. It is not a real-time remaining daily balance.
- Repayments search across all accounts, rank amount/date closeness, allow partial amounts, and cannot exceed the incoming credit or outstanding expense. Unlinking restores the expense and sends the credit back for classification.
- Savings progress sums all confirmed outgoing Savings-category transactions minus linked reimbursements. It does not use expected contributions or initiate bank transfers. This first version treats those historical Savings transactions as progress toward the one active goal; it does not allocate transactions among multiple goals.
- Merchant rules normalize case, numbers, punctuation, and `.com`, then use substring matches. Rules suggest a category; nothing is silently confirmed.
- Recurring candidates need the configured occurrence count, consistent weekly/biweekly/monthly/annual spacing, and initially similar amounts (within 20% of their average). Once identified, the item's own configured tolerance governs changes. Irregular income is classified independently in the transaction queue, never inferred as salary from an inconsistent cadence.

## Plaid and sync

Bank Link initializes Transactions with **730 days requested**, and optionally Investments where supported. Investment-only accounts have a separate Link action requiring Investments. Checking/savings/investment filtering is confined to integration logic; stored account type/subtype values are generic.

Initial sync consumes every available `/transactions/sync` page from the empty cursor, then uses the saved cursor for incremental updates. It processes added, modified, removed, and pending-to-posted records. Pagination is collected before a database transaction commits the imported data and cursor together; Plaid pagination-mutation errors restart from the original cursor. If amounts change, prior confirmation/netting is invalidated for review.

Balances come from `/accounts/get`; investment holdings are requested where available to refresh investment balances. Holdings positions and full investment transactions are not displayed. Institutions may deliver initial history asynchronously, and the amount available depends on the bank. See [Plaid Transactions](https://plaid.com/docs/transactions/) and [Link configuration](https://plaid.com/docs/api/link/).

HTTPS Link sessions register `/api/plaid/webhook`. Requests are checked with Plaid's ES256 verification key, issued-at window, and constant-time body-hash comparison using [Plaid's documented verification process](https://plaid.com/docs/api/webhooks/webhook-verification/). Relevant verified webhooks trigger a sync. There is also hourly polling and manual **Sync now**. Polling retrieves available changes; it does not force banks to produce fresh data every hour. Failed items return an actionable error and preserve their last successful cursor.

## Offline, notifications, and security

- Installable manifest with PNG/maskable icons and a service worker. Static shell/assets are cached after loading. Financial API responses are never put in the service-worker HTTP cache.
- IndexedDB stores the last loaded workspace and offline actions. Reopening offline shows that snapshot; pending actions replay on reconnection or the next authenticated load. Actions have stable IDs and a database idempotency table to prevent duplicate repayments.
- Browser storage contains your cached financial data. Use trusted devices. Signing out clears the live cache and action queue. The offline snapshot is intentionally viewable without contacting the server; device security matters.
- A server-side Web Push subscription store supports multiple devices. After each sync cycle, one batched push per subscribed device summarizes pending transactions, income, recurring candidates, amount changes, and possible repayments. Expired subscriptions are removed. Browser/OS badges and the inbox badge are updated too.
- Settings can enable or turn off push for the current device. Dismissing a false recurring pattern removes it from the review count and future mismatch alerts; the Recurring page can restore it.
- Push requires HTTPS, notification permission, VAPID keys, and a compatible browser. On iOS, install the app to the home screen first. Push endpoints are restricted to known browser push providers to avoid arbitrary server-side requests. VAPID credentials and real-device delivery must be verified on your deployed instance.
- Passwords use bcrypt cost 12. Sessions use random server-side tokens with only a hash stored in Postgres, a seven-day expiry, and HttpOnly/SameSite cookies. Production cookies are Secure.
- Optional TOTP enrollment requires verifying a code; enabled login rejects reused time steps. Enabling TOTP invalidates other sessions. Basic per-address authentication rate limiting is included.
- Mutations require JSON and an exact same-origin `Origin` header. Production requires HTTPS. Security headers and a restrictive CSP allow the Plaid Link domains and Google Fonts. System fonts work when fonts are unavailable offline.
- Bank access tokens and TOTP secrets are encrypted with AES-256-GCM. Back up Postgres and the encryption key separately. Do not commit credentials or enable public database ports.

## Validation

```sh
npm test
npm run build
go test ./...
go vet ./...
go test -tags integration ./server -run TestFullFlow -v -timeout 5m
```

The integration test downloads a disposable Postgres 17 binary, uses loopback port 55439 and a project-local cache, and stops the test database afterward. It exercises actual schema creation, account setup, cookie sessions, cross-account partial repayment, duplicate-action retries, unlinking, categories, goals, mocked Plaid pagination/recurring detection, logout, and TOTP login. It **does not contact a real bank or send real push notifications**. The regular test suite does not download or start Postgres.

To test Plaid and push end to end, provide your own Sandbox credentials, complete Link, sync and review the data, then enable notifications on an HTTPS device and trigger a sync with pending review items. Live institution connectivity, public TLS routing, and OS push delivery depend on your deployment and cannot be validated with the bundled fixtures.

## Project map

```text
src/                 React interface, IndexedDB client, money rules/tests
public/              PWA manifest, service worker, app icons
server/              HTTP API, auth, Plaid sync, notifications, integration tests
internal/budget/     Go budget engine, merchant matching, recurrence cadence/tests
internal/db/         Embedded idempotent Postgres schema and connection setup
compose.yaml         Private Postgres + application deployment
Dockerfile           React/Go build and non-root runtime image
```

No multi-user support, arbitrary categories, split transactions, multiple active goals in the UI, or credit-card linking is implemented. Data tables keep room for additional account types and goals later.

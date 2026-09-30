# sofar

A personal budgeting PWA. Real money, a clear picture, room to breathe.

React + TypeScript + Vite on the front end; Go + Postgres on the back end. The Go module uses Backr's `server` / `internal` package layout and zerolog conventions. No bank credentials or real transactions are bundled.

## Try the interface

```sh
npm ci
npm run dev
```

Open the URL printed by Vite. When no API is running, the interface opens a **clearly labeled demo workspace**, with sample checking, savings, and investment balances. Review decisions and edits persist in IndexedDB on this browser. The demo has no bank connection, does not send push notifications, and does not move money. With a running API, the app opens the account setup or login screen instead; the login screen also offers the demo.

The interface follows the supplied design's home, review, history, tools, categories, and sorting-rules flows. It includes searchable/filterable/exportable transactions, swipe review with accessible buttons and a skip action, income classification, cross-account partial repayments and unlinking, recurring confirmation/tolerances and reversible pattern dismissal, one editable savings goal, accounts, and settings. Custom subcategories can have monthly plans, and merchant sorting rules can be edited, paused, or deleted. The three top-level category IDs remain fixed even when their labels change. The “should i buy this?” comparison shows a purchase against the daily guide and hypothetical investment growth; “sleep on it” saves an item locally in the browser. Tools also includes calculators for savings timing, emergency-fund runway, debt payoff, and hypothetical investment growth. Calculators update as you edit assumptions and do not create transactions or change your saved goal. Demo data can be reset from Settings.

Review cards drag left to change category and right to confirm. Editor and filter sheets dismiss with a downward drag, and pulling down on Home syncs accounts. The design's spring motion, animated figures, and interaction feedback are included; Settings offers expressive, calm, and off motion plus a touch-feedback switch. The device's reduced-motion preference overrides the motion setting. Vibration depends on device and browser support, with visual feedback elsewhere.

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

5. Sign up for [SimpleFIN Bridge](https://bridge.simplefin.org) (a flat $15/year at the time of writing), connect your banks there, and create a **setup token**. You paste it into sofar after signing in (onboarding or **Accounts**); no bank or SimpleFIN credentials go in `.env`. Remove any old `PLAID_*` variables.
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

Run `npm run dev` in another terminal. The Vite dev server proxies `/api` to the Go process at `SOFAR_DEV_API` (default `http://127.0.0.1:8080`) and rewrites the request `Origin` to `SOFAR_ORIGIN` (default `http://localhost:8080`). Vite reads both from the environment or from `.env` / `.env.local`, so set `SOFAR_ORIGIN` to the address you browse (for example `http://127.0.0.1:5173`) and `SOFAR_DEV_API` to wherever `SOFAR_ADDR` listens if it isn't port 8080. Point `DATABASE_URL` at your local Postgres. To serve the built frontend from Go instead, `npm run build`, set `SOFAR_ORIGIN=http://localhost:8080` and `SOFAR_ENV=development`, and visit the Go server at that origin. Development explicitly allows HTTP with a non-Secure session cookie; production does not.

Without Go installed, `docker compose up -d --build` runs the whole stack (Postgres, the API, and the built frontend) at `http://localhost:8080` using the values in `.env` (set `SOFAR_ENV=development`, `SOFAR_ORIGIN=http://localhost:8080`, and `TRUST_PROXY=false` for local HTTP). `docker compose down -v` resets the database.

## How the numbers work

- Amounts in the database, domain logic, API, and IndexedDB are integer USD cents. Provider decimal values are rounded to cents at ingestion.
- Income uses the **three previous completed calendar months**, excluding the current partial month. Each income stream is summed and divided by three, including zero-income months; confirmed salary and self-employment averages stay separate.
- Only posted, confirmed incoming transactions explicitly classified into one of those two income streams are income. Transfers and linked repayments never contribute to the baseline. A partially linked incoming credit is excluded in full from income; only the explicitly linked amount reduces the expense. Split treatment of the rest is outside MVP scope.
- **Monthly safe-to-spend plan = salary average + self-employment average − the larger of confirmed recurring Expenses or planned Expenses subcategories − the larger of the goal contribution or planned Savings subcategories.** Taking the larger amount avoids counting the same planned commitment twice. Weekly commitments use 52/12, biweekly use 26/12, annual use 1/12. Negative results remain visible.
- The monthly plan is not a bank balance. Confirmed discretionary spending in the current month is subtracted when showing the **remaining room** on the home screen. Historical month navigation recalculates the income window and category totals using the current commitments and goal configuration.
- The home screen's **per-day guide** divides the remaining room by the days left in the current calendar month, including today. It is a planning guide, not a bank balance or a promise that future bills are funded.
- Repayments search across all accounts, rank amount/date closeness, allow partial amounts, and cannot exceed the incoming credit or outstanding expense. Unlinking restores the expense and sends the credit back for classification.
- Savings progress sums all confirmed outgoing Savings-category transactions minus linked reimbursements. It does not use expected contributions or initiate bank transfers. This first version treats those historical Savings transactions as progress toward the one active goal; it does not allocate transactions among multiple goals.
- Merchant rules normalize case, numbers, punctuation, and `.com`, then use substring matches. The longest matching pattern wins. Rules suggest a category or subcategory; nothing is silently confirmed.
- Recurring candidates need the configured occurrence count, consistent weekly/biweekly/monthly/annual spacing, and initially similar amounts (within 20% of their average). Once identified, the item's own configured tolerance governs changes. Irregular income is classified independently in the transaction queue, never inferred as salary from an inconsistent cadence.

## Reviewing and adjusting transactions

- **Ignore** removes a transaction from every budget total. An optional reason is stored, and "always ignore" saves the merchant so future posted outgoing transactions from it arrive already ignored. Re-categorizing an ignored transaction brings it back.
- **Split** divides one posted outgoing transaction across two to ten category parts that must add up to its exact amount. Split transactions cannot carry linked repayments; a material change from the bank (amount or direction) clears the split and sends it back for review.
- **Notes** are free text (up to 500 characters) stored with the transaction.
- **Add money** on the goal page records a manual outgoing Savings transaction from a chosen account, which counts toward goal progress like any other savings transaction.
- **Accounts** can be excluded from safe-to-spend; excluded accounts' transactions do not feed income, bills, or spending. When SimpleFIN reports that a bank needs attention (for example a changed password), the account shows SimpleFIN's message with a link to fix it in SimpleFIN Bridge, and the next successful sync clears the flag.
- **Sorting rules** can apply to past matching outgoing transactions ("fix past ones too"). Rules still only suggest categories for new transactions.
- **Sort all from a merchant**: confirming a review card can also confirm every other waiting transaction from the same merchant with the same category (on by default when there are others). "Always ignore" likewise ignores matching transactions already waiting.
- **Manual bills** can be added on the Recurring page; they are confirmed immediately and count as commitments. Due dates roll forward by cadence for display.
- **Manual entries** (goal deposits) can be deleted; bank transactions can only be ignored.
- **Accounts** can be renamed (the name survives syncs), retyped (checking, savings, credit card, or investment), or removed from sofar, which hides the account and deletes its imported transactions. **Disconnect** on the Accounts page forgets the SimpleFIN access and deletes all imported accounts and transactions; rules, categories, bills, and entries you added by hand stay.
- **Settings** can change the password (signing out other sessions) and turn off two-step verification with the password and a current code. Enrollment shows a QR code.

## SimpleFIN and sync

sofar reads your banks through [SimpleFIN Bridge](https://www.simplefin.org/protocol.html). Paste a setup token once; sofar claims it, stores the resulting access URL **encrypted** (AES-256-GCM), and never shows it again. One connection covers every bank linked in SimpleFIN Bridge. Access is read-only. sofar only accepts setup tokens and access URLs on `simplefin.org` (set `SIMPLEFIN_ALLOWED_HOSTS` to a comma-separated list to use a self-hosted bridge instead). All outbound requests refuse to connect to loopback, private, link-local, and other non-public addresses and never follow redirects, so a hostile token cannot aim the server at your internal network.

- **Freshness.** SimpleFIN refreshes each bank about once a day, so sofar cannot be more current than that. sofar checks every four hours and **Sync now** fetches at most once an hour (otherwise it says you're up to date), well inside SimpleFIN's request guidance.
- **History.** The first sync fetches the most recent 44 days including pending items, then walks back in 45-day requests (SimpleFIN's recommended range) for up to about a year, stopping when a window comes back empty. How much a bank provides varies.
- **Pending items** are imported and replaced when they post. A pending item that disappears is removed. If a posted amount or direction changes, earlier confirmation, splits, and netting are invalidated for review.
- **Account types** are guessed from the account and institution names (checking, savings, credit card, investment) and can be changed on the Accounts page. Investment accounts track balances only; their trades, dividends, and sweeps are not imported. Changing an account away from investment brings its history in on the next sync.
- **Errors.** Connection problems reported by SimpleFIN are shown on the affected accounts; failed fetches keep your data and show the reason.

## Categories, merchants, and suggestions

- **Default categories.** A first run adds 24 starter categories under the three fixed groups (for example rent & mortgage, utilities, phone & internet, subscriptions under expenses; groceries, dining out, gas & convenience, shopping, health & medical, travel under spending; savings account and investing under savings). They are modeled on [Plaid's published category taxonomy](https://plaid.com/documents/transactions-personal-finance-category-taxonomy.csv). Rename, re-plan, or delete any; deleted defaults are not re-added.
- **Merchant directory.** `internal/budget/merchants.json` holds several hundred merchants and descriptive keywords (weighted toward Texas chains and common national brands). The Go server and the web app both read this one file, so they sort the same way. Bank descriptions are cleaned for display (processor prefixes, store numbers, and addresses are dropped, and the original text stays in the transaction details).
- **Suggestions never confirm anything.** For each waiting transaction sofar suggests, in order: your own sorting rule (always final, including a rule that says "just the group"); money moving between your own accounts, matched by amount and date within four days (to savings or investing counts as savings; between checking accounts, or a credit card payment, is offered as "not spending"); your earlier choices for the same payer; then the directory. Deposits are suggested as paycheck, other income (dividends, interest, refunds), or transfer; an unknown deposit must be classified before it can be confirmed.
- **Other income** is tracked but, like transfers, does not raise the safe-to-spend plan. Only deposits marked as paycheck or self-employed do.
- **Subscriptions & recurring** has its own page: monthly and yearly cost, what is due in the next 30 days, detected patterns to confirm, flags for charges that stopped, and a list you can filter and sort. Known monthly merchants (streaming, phone, utilities, insurance, gyms, and similar) are suggested after their first charge; other patterns need the configured number of occurrences.

## Offline, notifications, and security

- Installable manifest with PNG/maskable icons and a service worker. Static shell/assets are cached after loading. Financial API responses are never put in the service-worker HTTP cache.
- IndexedDB stores the last loaded workspace and offline actions. Reopening offline shows that snapshot; pending actions replay on reconnection or the next authenticated load. Actions have stable IDs and a database idempotency table to prevent duplicate repayments.
- Browser storage contains your cached financial data. Use trusted devices. Signing out clears the live cache and action queue. The offline snapshot is intentionally viewable without contacting the server; device security matters.
- A server-side Web Push subscription store supports multiple devices. After each sync cycle, one batched push per subscribed device summarizes pending transactions, income, recurring candidates, amount changes, and possible repayments. Expired subscriptions are removed. Browser/OS badges and the inbox badge are updated too.
- Settings can enable or turn off push for the current device. Dismissing a false recurring pattern removes it from the review count and future mismatch alerts; the Recurring page can restore it.
- Push requires HTTPS, notification permission, VAPID keys, and a compatible browser. On iOS, install the app to the home screen first. Push endpoints are restricted to known browser push providers to avoid arbitrary server-side requests. Signing out removes that device's subscription, changing the password removes every subscription, and Settings can turn notifications off on all devices at once. VAPID credentials and real-device delivery must be verified on your deployed instance.
- Passwords use bcrypt cost 12. Sessions use random server-side tokens with only a hash stored in Postgres, a seven-day expiry, and HttpOnly/SameSite cookies. Production cookies are Secure.
- Optional TOTP enrollment requires verifying a code; enabled login rejects reused time steps. Enabling TOTP invalidates other sessions. Sign-in attempts are rate limited per caller (ten failures per fifteen minutes), never per proxy, and a flood of failures across many addresses slows further failures without ever delaying a correct login. Behind a reverse proxy set `TRUST_PROXY=true`: the caller's address is then the last `X-Forwarded-For` entry, which is the one your proxy appended, so the proxy must be the outermost hop (Caddy in `Caddyfile.example` is). Without `TRUST_PROXY` the header is ignored. Enrolling an authenticator, turning it off, and changing the password all require the current password, not just a session.
- Mutations require JSON and an exact same-origin `Origin` header. Production requires HTTPS. Security headers and a restrictive CSP allow only this site and Google Fonts. System fonts work when fonts are unavailable offline.
- Bank access tokens and TOTP secrets are encrypted with AES-256-GCM. Back up Postgres and the encryption key separately. Do not commit credentials or enable public database ports.

## Validation

```sh
npm test
npm run build
go test ./...
go vet ./...
go test -tags integration ./server -run TestFullFlow -v -timeout 5m
```

The integration test downloads a disposable Postgres 17 binary, uses loopback port 55439 and a project-local cache, and stops the test database afterward. To use your own database instead (for example a throwaway container), set `SOFAR_TEST_DATABASE_URL`; it must be empty because the test inserts fixed rows. It exercises actual schema creation, account setup, cookie sessions, cross-account partial repayment, duplicate-action retries, unlinking, categories, goals, notes, ignore rules, splits, manual savings deposits, account exclusion and reconnect flagging, a mocked SimpleFIN Bridge (claiming, history backfill, pending reconciliation, connection errors, disconnect), merchant suggestions and transfer matching, recurring detection, logout, and TOTP login. It **does not contact a real bank or send real push notifications**. The regular test suite does not download or start Postgres.

To test SimpleFIN and push end to end, connect a real SimpleFIN token, sync and review the data, then enable notifications on an HTTPS device and trigger a sync with pending review items. Live institution connectivity, public TLS routing, and OS push delivery depend on your deployment and cannot be validated with the bundled fixtures.

## Project map

```text
src/                 React interface, IndexedDB client, money rules/tests
public/              PWA manifest, service worker, app icons
server/              HTTP API, auth, SimpleFIN sync, suggestions, notifications, integration tests
internal/budget/     Go budget engine, merchant matching, recurrence cadence/tests
internal/db/         Embedded idempotent Postgres schema and connection setup
compose.yaml         Private Postgres + application deployment
Dockerfile           React/Go build and non-root runtime image
```

No multi-user support, arbitrary top-level categories, multiple active goals in the UI, or credit-card linking is implemented. Data tables keep room for additional account types and goals later.

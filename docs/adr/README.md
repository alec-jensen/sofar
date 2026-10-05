# architecture decision records

Short records of decisions. The first nine were made during design (0010 to 0020 were made in the implementation plan) and are final unless the owner says otherwise. Add a new record for every non-obvious technical decision (copy `0000-template.md`).

| id | title |
|---|---|
| 0001 | stack: go, postgres, react pwa |
| 0002 | events plus derived state |
| 0003 | server is authoritative for budget math |
| 0004 | go-native ml core |
| 0005 | simplefin only, provider seam kept |
| 0006 | single-user, self-hosted, not a product |
| 0007 | request pending and design for its absence |
| 0008 | no scope cutoff |
| 0009 | gamification as a skin over truth (the grove) |
| 0010 | migrations: goose with embedded sql |
| 0011 | job queue: small in-repo postgres queue |
| 0012 | layout: cmd/sofar, internal/*, web/ |
| 0013 | property tests: rapid |
| 0014 | api contract: spec-first openapi |
| 0015 | database access: database/sql with pgx stdlib |
| 0016 | config names: keep the deployed names |
| 0017 | end-to-end tests: playwright with axe |
| 0018 | passkeys and oidc libraries |
| 0019 | ofx, qfx and qif parsing is in-repo |
| 0020 | sync request budget: token bucket |

# Project completion report

## What’s implemented
- Server routes/pages: `/`, `/login`, `/sign-up`, `/dashboard`
- Auth APIs: `/api/signup`, `/api/login`, `/api/logout` (session cookie)
- Transaction APIs: 
  - `GET /api/transactions` (list, newest-first)
  - `POST /api/transactions` (add)
  - `DELETE /api/transactions/{id}` (delete)
- Dashboard UI is wired to the APIs:
  - Loads transactions from `GET /api/transactions`
  - Adds via `POST /api/transactions`
  - Deletes via `DELETE /api/transactions/{id}`
  - Recalculates income/expenses/balance after each update

## Verified
- `go test ./...` passes (no test files).

## Notes
- Static assets are served from `templates/assets/`.
- Templates live under `templates/html/` and are parsed by `NewApp()`.
- Session storage is in-memory (sessions vanish on server restart).

# Budget-tracker completion TODO

- [ ] Replace `main.go` server startup to use `App` from `tracker.go` (auth + dashboard + transaction APIs).
- [ ] Wire `/api/login` and `/api/signup` to `templates/html/login.html` and `templates/html/sign-up.html` (forms submit to real endpoints).
- [ ] Make `templates/html/dashboard.html` dynamic:
  - [ ] Fetch transactions via `GET /api/transactions` and render list.
  - [ ] Submit add-transaction via `POST /api/transactions`.
  - [ ] Delete transactions via `DELETE /api/transactions/{id}`.
- [ ] Ensure static assets path works for images/CSS.
- [ ] Run `go test ./...` and run the server; sanity-check end-to-end flow.


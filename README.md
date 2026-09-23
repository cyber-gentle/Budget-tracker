# Spendly — Modern Personal Finance & Budget Tracker

Spendly is a fast, responsive, and intuitive personal budget tracking web application built with Go, Vanilla CSS, and modern interactive analytics.

---

## ✨ Features

- **Executive KPI Cards**: Real-time overview of Total Balance, Monthly Income, Monthly Expenses, and Savings Rate %.
- **Visual Analytics**: Interactive Category Expense Donut Chart & 6-month historical Cash Flow Bar Chart powered by Chart.js.
- **Monthly Category Budgets**: Set spending caps per category with color-coded progress bars (<80% green, 80–99% amber, ≥100% red alert).
- **Frictionless Transactions**:
  - Custom date selection (backdate or schedule transactions).
  - 9 pre-configured category buckets with emojis.
  - Live client-side search by note, merchant, or category.
  - Instant filters by Type (All / Income / Expense) and Category.
- **Data Portability**: 1-click CSV export (`/api/export`) for spreadsheets.
- **Multi-Currency**: Toggle between `₦` (NGN), `$` (USD), `€` (EUR), and `£` (GBP).
- **Turso / libSQL Cloud Database**: Permanent serverless database that never sleeps or wipes data.
- **Hardened Security**: Salted SHA-256 password hashing with persistent sessions across server reboots.

---

## 🚀 Getting Started

### Local Development

1. Ensure Go 1.22+ is installed.
2. Run the application:
   ```bash
   go run .
   ```
   *(Without environment variables, Spendly automatically falls back to a local SQLite database `spendly.db`)*
3. Open [http://localhost:8080](http://localhost:8080) in your browser.

### Running Tests

```bash
go test -v ./...
```

---

## ⚡ Deployment: Vercel + Turso (100% Free Forever)

This repository is pre-configured for instant **Vercel** serverless deployment with a cloud **Turso (libSQL)** database.

### Step 1: Create a Free Turso Database (Takes 60 seconds)

1. Go to [turso.tech](https://turso.tech/) and sign in with GitHub.
2. In the Turso dashboard (or CLI), create a database:
   ```bash
   # Or create directly via web dashboard at app.turso.tech
   turso db create spendly-db
   ```
3. Copy your **Database URL** (e.g. `libsql://spendly-db-yourname.turso.io`) and generate an **Auth Token**:
   ```bash
   turso db tokens create spendly-db
   ```

### Step 2: Deploy to Vercel

1. Push your repository to GitHub.
2. In the [Vercel Dashboard](https://vercel.com/dashboard), click **Add New...** > **Project** and import this repository.
3. In **Environment Variables**, add:
   - `TURSO_DATABASE_URL`: `libsql://spendly-db-yourname.turso.io`
   - `TURSO_AUTH_TOKEN`: your generated token
4. Click **Deploy**.

Vercel will build the Go serverless function and launch your application at a permanent URL like `https://spendly.vercel.app`!
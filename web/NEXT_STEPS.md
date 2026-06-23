# DocsMCP Web — Next Steps

## 1. Frontend setup (do this first)

```bash
cd web
npm install
cp .env.example .env.local
# fill in .env.local (see below)
npm run dev   # http://localhost:3000
```

### `.env.local` values needed

| Variable | Where to get it |
|---|---|
| `BETTER_AUTH_SECRET` | `openssl rand -hex 32` |
| `BETTER_AUTH_URL` | `http://localhost:3000` for dev |
| `GITHUB_CLIENT_ID` | GitHub → Settings → Developer Settings → OAuth Apps → New OAuth App |
| `GITHUB_CLIENT_SECRET` | Same OAuth App |
| `DATABASE_URL` | Same Postgres as the Go API |
| `NEXT_PUBLIC_APP_URL` | `http://localhost:3000` for dev |
| `NEXT_PUBLIC_API_URL` | `http://localhost:8080` for dev |

### GitHub OAuth App settings (for local dev)
- **Homepage URL**: `http://localhost:3000`
- **Authorization callback URL**: `http://localhost:3000/api/auth/callback/github`

Register a **separate** OAuth App for production with:
- **Homepage URL**: `https://your-app.vercel.app`
- **Authorization callback URL**: `https://your-app.vercel.app/api/auth/callback/github`

---

## 2. Better Auth database migration

Better Auth needs its own tables in Postgres. Run the CLI to generate:

```bash
cd web
npx @better-auth/cli migrate --config src/lib/auth.ts
```

This creates `user`, `session`, `account`, `verification` tables. Safe to run on the same DB as the Go API — no table name conflicts.

---

## 3. Go backend — add the GitHub auth endpoint

Three changes needed in the Go service before the dashboard's "API key" section works:

### a) `pkg/store/postgres.go` — add `UpsertUserByEmail`

```go
func (s *PostgresStore) UpsertUserByEmail(ctx context.Context, r *UserRecord) error {
    _, err := s.pool.Exec(ctx,
        `INSERT INTO users (id, email, platform_api_key_hash, created_at)
         VALUES ($1, $2, $3, now())
         ON CONFLICT (email)
         DO UPDATE SET platform_api_key_hash = EXCLUDED.platform_api_key_hash`,
        r.ID, r.Email, r.PlatformAPIKeyHash,
    )
    return err
}
```

### b) `pkg/store/db.go` — add to `UserDB` interface

```go
UpsertUserByEmail(ctx context.Context, r *UserRecord) error
```

### c) `cmd/server/main.go` — register the route (public, no platform key needed)

```go
v1.POST("/auth/github", api.NewGitHubAuthHandler(pg).GitHubLogin)
```

Add this line in the `v1` group, alongside the existing `/register` route.

---

## 4. Vercel deployment

```bash
# from repo root — Vercel auto-detects the Next.js app in /web
vercel --cwd web
```

Set all env vars from `.env.example` in the Vercel project dashboard.
Use the production GitHub OAuth App credentials.

---

## 5. CORS on the Go API

The browser makes requests from `https://your-app.vercel.app` to `https://mcp-me-production.up.railway.app`.
Add the Vercel origin to the Go API's allowed CORS origins before deploying.

In `cmd/server/main.go`, add after `gin.SetMode(gin.ReleaseMode)`:

```go
r.Use(func(c *gin.Context) {
    origin := c.Request.Header.Get("Origin")
    allowed := os.Getenv("CORS_ORIGINS") // comma-separated, e.g. "https://your-app.vercel.app"
    for _, o := range strings.Split(allowed, ",") {
        if strings.TrimSpace(o) == origin {
            c.Header("Access-Control-Allow-Origin", origin)
            c.Header("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
            c.Header("Access-Control-Allow-Headers", "Content-Type,Authorization,X-API-Key")
            break
        }
    }
    if c.Request.Method == "OPTIONS" {
        c.AbortWithStatus(204)
        return
    }
    c.Next()
})
```

Add `CORS_ORIGINS=https://your-app.vercel.app` to Railway env vars.

---

## Status

- [x] Homepage (`/`) — hero, pipeline demo, marquee, terminal demo, CTA
- [x] Login page (`/login`) — GitHub OAuth via Better Auth
- [x] Dashboard (`/dashboard`) — collections, API key, add new URL
- [x] Better Auth config (`src/lib/auth.ts`, `src/app/api/auth/[...all]/route.ts`)
- [x] Go auth endpoint handler (`pkg/api/auth_github.go`) — written, not yet wired
- [ ] Better Auth DB migration (step 2 above)
- [ ] Go: `UpsertUserByEmail` in postgres.go (step 3a)
- [ ] Go: route registered in main.go (step 3c)
- [ ] Go: CORS middleware (step 5)
- [ ] Vercel deploy (step 4)

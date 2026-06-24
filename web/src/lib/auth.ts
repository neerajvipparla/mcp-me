import { betterAuth } from "better-auth"
import { Kysely, PostgresDialect } from "kysely"
import { Pool } from "pg"

// VERCEL_URL is auto-injected by Vercel on every build (no https://)
const baseURL =
  process.env.BETTER_AUTH_URL ??
  (process.env.VERCEL_URL ? `https://${process.env.VERCEL_URL}` : "http://localhost:3000")

const pool = new Pool({ connectionString: process.env.DATABASE_URL })

// Exported for server-side direct queries (e.g. reading account.accessToken)
export const db = new Kysely<{
  account: { userId: string; providerId: string; accessToken: string | null }
}>({
  dialect: new PostgresDialect({ pool }),
})

export const auth = betterAuth({
  baseURL,
  database: pool,
  socialProviders: {
    github: {
      clientId: process.env.GITHUB_CLIENT_ID!,
      clientSecret: process.env.GITHUB_CLIENT_SECRET!,
    },
  },
  trustedOrigins: [
    process.env.NEXT_PUBLIC_APP_URL ?? "http://localhost:3000",
  ],
})

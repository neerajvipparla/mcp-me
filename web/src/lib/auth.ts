import { betterAuth } from "better-auth"
import { Pool } from "pg"

export const auth = betterAuth({
  database: {
    db: new Pool({ connectionString: process.env.DATABASE_URL }),
    type: "pg",
  },
  socialProviders: {
    github: {
      clientId: process.env.GITHUB_CLIENT_ID!,
      clientSecret: process.env.GITHUB_CLIENT_SECRET!,
    },
  },
  trustedOrigins: [
    process.env.NEXT_PUBLIC_APP_URL ?? "http://localhost:3000",
  ],
  user: {
    additionalFields: {
      githubUsername: { type: "string", required: false },
    },
  },
})

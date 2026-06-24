import { createAuthClient } from "better-auth/react"

function resolveBaseURL(): string {
  const url = process.env.NEXT_PUBLIC_APP_URL
  if (url) {
    try { new URL(url); return url } catch {}
  }
  return "http://localhost:3000"
}

export const authClient = createAuthClient({
  baseURL: resolveBaseURL(),
})

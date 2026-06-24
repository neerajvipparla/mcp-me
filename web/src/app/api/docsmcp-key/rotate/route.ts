import { NextResponse } from "next/server"
import { auth, db } from "@/lib/auth"
import { headers } from "next/headers"

export const dynamic = "force-dynamic"

const API = process.env.NEXT_PUBLIC_API_URL ?? "https://mcp-me-production.up.railway.app"

export async function POST() {
  const session = await auth.api.getSession({ headers: await headers() })
  if (!session?.user) {
    return NextResponse.json({ error: "unauthorized" }, { status: 401 })
  }

  const account = await db
    .selectFrom("account")
    .where("userId", "=", session.user.id)
    .where("providerId", "=", "github")
    .select("accessToken")
    .executeTakeFirst()

  if (!account?.accessToken) {
    return NextResponse.json({ error: "github token not found" }, { status: 404 })
  }

  const res = await fetch(`${API}/v1/auth/github/rotate`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ github_token: account.accessToken }),
  })

  if (!res.ok) {
    return NextResponse.json({ error: "backend error" }, { status: 502 })
  }

  const data = await res.json()
  return NextResponse.json({ api_key: data.api_key, email: data.email })
}

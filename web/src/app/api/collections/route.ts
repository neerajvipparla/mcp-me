import { NextResponse } from "next/server"
import { auth } from "@/lib/auth"
import { headers, cookies } from "next/headers"

export const dynamic = "force-dynamic"

const API = process.env.NEXT_PUBLIC_API_URL ?? "https://mcp-me-production.up.railway.app"

// Forward the Better Auth session token to Go so it can verify directly against
// the shared Postgres session table — no DASHBOARD_SECRET env var needed.
// Better Auth uses the __Secure- prefix on HTTPS (production); bare name on HTTP (local dev).
async function sessionHeader(): Promise<HeadersInit> {
  const cookieStore = await cookies()
  const token =
    cookieStore.get("__Secure-better-auth.session_token")?.value ??
    cookieStore.get("better-auth.session_token")?.value ??
    ""
  return { "X-Auth-Session": token }
}

export async function GET(req: Request) {
  const session = await auth.api.getSession({ headers: await headers() })
  if (!session?.user?.email) {
    return NextResponse.json({ error: "unauthorized" }, { status: 401 })
  }

  const url = new URL(req.url)
  const page = url.searchParams.get("page") ?? "1"
  const limit = url.searchParams.get("limit") ?? "10"

  const res = await fetch(`${API}/v1/crawls?page=${page}&limit=${limit}`, {
    headers: await sessionHeader(),
    cache: "no-store",
  })

  if (!res.ok) {
    return NextResponse.json({ crawls: [], page: 1, limit: 10, has_more: false })
  }

  return NextResponse.json(await res.json())
}

export async function POST(req: Request) {
  const session = await auth.api.getSession({ headers: await headers() })
  if (!session?.user?.email) {
    return NextResponse.json({ error: "unauthorized" }, { status: 401 })
  }

  const body = await req.json()
  const res = await fetch(`${API}/v1/crawl`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...(await sessionHeader()),
    },
    body: JSON.stringify(body),
  })

  const data = await res.json()
  return NextResponse.json(data, { status: res.status })
}

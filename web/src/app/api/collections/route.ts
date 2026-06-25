import { NextResponse } from "next/server"
import { auth } from "@/lib/auth"
import { headers } from "next/headers"

export const dynamic = "force-dynamic"

const API = process.env.NEXT_PUBLIC_API_URL ?? "https://mcp-me-production.up.railway.app"

export async function GET(req: Request) {
  const session = await auth.api.getSession({ headers: await headers() })
  if (!session?.user?.email) {
    return NextResponse.json({ error: "unauthorized" }, { status: 401 })
  }

  // session.session.token is the exact value stored in Better Auth's session table.
  // Go verifies it directly against the shared Postgres — no cookie parsing, no shared secret.
  const token: string = (session as any).session?.token ?? ""

  const url = new URL(req.url)
  const page = url.searchParams.get("page") ?? "1"
  const limit = url.searchParams.get("limit") ?? "10"

  const res = await fetch(`${API}/v1/crawls?page=${page}&limit=${limit}`, {
    headers: { "X-Auth-Session": token },
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

  const token: string = (session as any).session?.token ?? ""

  const body = await req.json()
  const res = await fetch(`${API}/v1/crawl`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Auth-Session": token,
    },
    body: JSON.stringify(body),
  })

  const data = await res.json()
  return NextResponse.json(data, { status: res.status })
}

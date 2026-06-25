"use client"

import Link from "next/link"
import Image from "next/image"
import { authClient } from "@/lib/auth-client"
import { useState } from "react"

export default function LoginPage() {
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")

  const signInWithGitHub = async () => {
    setLoading(true)
    setError("")
    try {
      await authClient.signIn.social({
        provider: "github",
        callbackURL: "/dashboard",
      })
    } catch {
      setError("Sign in failed. Please try again.")
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-bg flex flex-col">
      {/* Ambient background */}
      <div className="fixed inset-0 pointer-events-none">
        <div className="absolute top-0 left-1/2 -translate-x-1/2 w-[600px] h-[400px] bg-accent/8 rounded-full blur-[120px]" />
      </div>

      {/* Nav */}
      <div className="relative z-10 px-6 h-16 flex items-center">
        <Link href="/" className="flex items-center gap-2.5 hover:opacity-80 transition-opacity">
          <Image src="/mcpme-logo.png" alt="mcp-me" width={30} height={30} className="rounded-md" />
          <span className="font-serif italic text-xl text-tx">mcp-me</span>
        </Link>
      </div>

      {/* Card */}
      <div className="relative z-10 flex-1 flex items-center justify-center px-6">
        <div className="w-full max-w-sm">
          <div className="rounded-2xl border border-border bg-surface p-8">
            {/* Logotype */}
            <div className="text-center mb-8">
              <Image
                src="/mcpme-logo.png"
                alt="mcp-me"
                width={52}
                height={52}
                className="rounded-xl mx-auto mb-4"
              />
              <h1 className="font-serif italic text-2xl text-tx mb-1">Welcome back</h1>
              <p className="text-sm text-tx-muted">Sign in to manage your doc collections</p>
            </div>

            {/* GitHub button */}
            <button
              onClick={signInWithGitHub}
              disabled={loading}
              className="w-full flex items-center justify-center gap-3 px-5 py-3.5 rounded-xl bg-[#24292F] hover:bg-[#2F353C] border border-[#3D444D] text-white text-sm font-medium transition-all duration-200 disabled:opacity-60 disabled:cursor-not-allowed"
            >
              {loading ? (
                <span className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin" />
              ) : (
                <svg className="w-5 h-5" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0 0 24 12c0-6.63-5.37-12-12-12z" />
                </svg>
              )}
              {loading ? "Signing in…" : "Continue with GitHub"}
            </button>

            {error && (
              <p className="mt-4 text-center text-xs text-red-400">{error}</p>
            )}

            <div className="mt-6 pt-6 border-t border-border">
              <p className="text-xs text-tx-muted text-center leading-relaxed">
                First time here? Signing in creates your account automatically.
                Your API key will be waiting in the dashboard.
              </p>
            </div>
          </div>

          <p className="mt-6 text-center text-xs text-tx-muted">
            <Link href="/" className="hover:text-tx transition-colors">← Back to home</Link>
          </p>
        </div>
      </div>
    </div>
  )
}

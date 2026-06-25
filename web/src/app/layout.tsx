import type { Metadata } from "next"
import "./globals.css"

export const metadata: Metadata = {
  title: "mcp-me",
  description:
    "Crawl any documentation URL. Get a private MCP endpoint. Let Claude search real docs instead of guessing.",
  icons: {
    icon: [
      { url: "/favicon.ico", sizes: "any" },
      { url: "/mcpme-logo.png", type: "image/png" },
    ],
    apple: { url: "/mcpme-logo.png", type: "image/png" },
  },
  openGraph: {
    title: "mcp-me",
    description: "Give Claude the actual docs.",
    type: "website",
  },
}

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  )
}

import type { Metadata } from "next"
import "./globals.css"

export const metadata: Metadata = {
  title: "DocsMCP — Give Claude the actual docs",
  description:
    "Crawl any documentation URL. Get a private MCP endpoint. Let Claude search real docs instead of guessing.",
  openGraph: {
    title: "DocsMCP",
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

import type { Metadata } from "next";
import "./globals.css";
import { AppShell } from "./layout/AppShell";
import { Providers } from "./layout/providers";

export const metadata: Metadata = {
  title: "Dev Plane | Engineering Reviews",
  description:
    "Review, verify, and manage software delivery from one workspace",
};

export default function RootLayout({
  children,
}: { children: React.ReactNode }) {
  return (
    <html lang="en" className="dark">
      <body>
        <Providers>
          <AppShell>{children}</AppShell>
        </Providers>
      </body>
    </html>
  );
}

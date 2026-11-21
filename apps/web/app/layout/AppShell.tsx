"use client";

import { usePathname } from "next/navigation";
import { ReviewShell } from "./ReviewShell";
import { Sidebar } from "./Sidebar";
import { TopBar } from "./TopBar";

export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();

  if (pathname.startsWith("/reviews")) {
    return <ReviewShell>{children}</ReviewShell>;
  }

  return (
    <div className="flex h-screen bg-[#0d1117] text-[#e6edf3]">
      <Sidebar />
      <div className="flex-1 flex flex-col min-w-0">
        <TopBar />
        <main className="flex-1 overflow-auto p-6">{children}</main>
      </div>
    </div>
  );
}

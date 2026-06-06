import type { Metadata } from 'next';
import './globals.css';
import { Providers } from './layout/providers';
import { Sidebar } from './layout/Sidebar';
import { TopBar } from './layout/TopBar';

export const metadata: Metadata = {
  title: 'AI Dev Control Plane',
  description: 'Control plane for AI software teams',
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en" className="dark">
      <body>
        <Providers>
          <div className="flex h-screen bg-[#0d1117]">
            <Sidebar />
            <div className="flex-1 flex flex-col min-w-0">
              <TopBar />
              <main className="flex-1 overflow-auto p-6">{children}</main>
            </div>
          </div>
        </Providers>
      </body>
    </html>
  );
}

"use client";

import { Loading } from "@/components/common/Loading";
import { ToastProvider } from "@/components/ui/toast";
import { api, decodeTokenClaims } from "@/lib/api";
import { useStore } from "@/lib/store";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode, useEffect, useState } from "react";

function BootstrapProvider({ children }: { children: ReactNode }) {
  const [ready, setReady] = useState(false);
  const {
    setUser,
    setOrganizations,
    setSelectedOrg,
    setProjects,
    setBootstrapped,
  } = useStore();

  useEffect(() => {
    let cancelled = false;

    async function bootstrap() {
      const token = localStorage.getItem("token");
      if (!token) {
        setBootstrapped(true);
        setReady(true);
        return;
      }

      const claims = decodeTokenClaims(token);
      if (claims) {
        setUser({
          id: claims.user_id,
          organization_id: claims.org_id,
          email: claims.email,
          role: claims.role as "owner" | "admin" | "member",
          name: claims.name || claims.email.split("@")[0],
          avatar_url: claims.avatar_url,
          created_at: "",
          updated_at: "",
        });
      }

      try {
        const orgs = await api.listOrganizations();
        const orgList = (orgs as any)?.data || orgs || [];
        if (!cancelled) {
          setOrganizations(orgList);
          const firstOrg = orgList[0];
          if (firstOrg?.id) {
            setSelectedOrg(firstOrg.id);
            const projects = await api.listProjects(firstOrg.id);
            const projectList = (projects as any)?.data || projects || [];
            if (!cancelled) {
              setProjects(projectList);
            }
          }
        }
      } catch {
        // If the token is invalid or the API is unavailable, leave the user
        // signed out and let the UI surface the auth state.
        setUser(null);
        localStorage.removeItem("token");
      } finally {
        if (!cancelled) {
          setBootstrapped(true);
          setReady(true);
        }
      }
    }

    bootstrap();
    return () => {
      cancelled = true;
    };
  }, [setUser, setOrganizations, setSelectedOrg, setProjects, setBootstrapped]);

  if (!ready) {
    return (
      <div className="h-screen w-screen flex items-center justify-center bg-[#0d1117]">
        <Loading />
      </div>
    );
  }

  return <>{children}</>;
}

export function Providers({ children }: { children: ReactNode }) {
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            staleTime: 30 * 1000,
            refetchOnWindowFocus: false,
            retry: 2,
          },
        },
      }),
  );

  return (
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <BootstrapProvider>{children}</BootstrapProvider>
      </ToastProvider>
    </QueryClientProvider>
  );
}

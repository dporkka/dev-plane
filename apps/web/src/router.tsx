import { Sidebar } from "@/app/layout/Sidebar";
import { TopBar } from "@/app/layout/TopBar";
import { Providers } from "@/app/layout/providers";
import ArchitecturePage from "@/app/architecture/page";
import DashboardPage from "@/app/dashboard/page";
import ProjectArchitecturePage from "@/app/projects/[id]/architecture/page";
import ProjectDetailPage from "@/app/projects/[id]/page";
import ProjectSettingsPage from "@/app/projects/[id]/settings/page";
import ProjectTasksPage from "@/app/projects/[id]/tasks/page";
import ProjectsPage from "@/app/projects/page";
import RepositoriesPage from "@/app/repositories/page";
import RunDetailPage from "@/app/runs/[id]/page";
import IntegrationsPage from "@/app/settings/integrations/page";
import SettingsPage from "@/app/settings/page";
import PoliciesPage from "@/app/settings/policies/page";
import TaskDetailPage from "@/app/tasks/[id]/page";
import TaskRunsPage from "@/app/tasks/[id]/runs/page";
import TaskSpecPage from "@/app/tasks/[id]/spec/page";
import {
  Outlet,
  createRootRoute,
  createRoute,
  createRouter,
  redirect,
} from "@tanstack/react-router";

function AppShell() {
  return (
    <Providers>
      <div className="flex h-screen bg-[#0d1117]">
        <Sidebar />
        <div className="flex-1 flex flex-col min-w-0">
          <TopBar />
          <main className="flex-1 overflow-auto p-6">
            <Outlet />
          </main>
        </div>
      </div>
    </Providers>
  );
}

function NotFound() {
  return (
    <div className="flex h-full items-center justify-center">
      <div className="text-center">
        <h1 className="text-xl font-semibold text-white">Page not found</h1>
        <p className="mt-2 text-sm text-gray-400">
          The requested Dev Plane route does not exist.
        </p>
      </div>
    </div>
  );
}

const rootRoute = createRootRoute({
  component: AppShell,
  notFoundComponent: NotFound,
});

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  beforeLoad: () => {
    throw redirect({ to: "/dashboard" });
  },
});

function page(path: string, component: React.ComponentType) {
  return createRoute({
    getParentRoute: () => rootRoute,
    path,
    component,
  });
}

const routeTree = rootRoute.addChildren([
  indexRoute,
  page("/dashboard", DashboardPage),
  page("/architecture", ArchitecturePage),
  page("/projects", ProjectsPage),
  page("/projects/$id", ProjectDetailPage),
  page("/projects/$id/architecture", ProjectArchitecturePage),
  page("/projects/$id/settings", ProjectSettingsPage),
  page("/projects/$id/tasks", ProjectTasksPage),
  page("/repositories", RepositoriesPage),
  page("/runs/$id", RunDetailPage),
  page("/settings", SettingsPage),
  page("/settings/integrations", IntegrationsPage),
  page("/settings/policies", PoliciesPage),
  page("/tasks/$id", TaskDetailPage),
  page("/tasks/$id/runs", TaskRunsPage),
  page("/tasks/$id/spec", TaskSpecPage),
]);

export const router = createRouter({
  routeTree,
  defaultPreload: "intent",
  scrollRestoration: true,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

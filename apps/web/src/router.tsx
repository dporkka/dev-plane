import ArchitecturePage from "@/app/architecture/page";
import DashboardPage from "@/app/dashboard/page";
import { Sidebar } from "@/app/layout/Sidebar";
import { TopBar } from "@/app/layout/TopBar";
import { Providers } from "@/app/layout/providers";
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
    throw redirect({ href: "/dashboard" });
  },
});

const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/dashboard",
  component: DashboardPage,
});
const architectureRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/architecture",
  component: ArchitecturePage,
});
const projectsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/projects",
  component: ProjectsPage,
});
const projectDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/projects/$id",
  component: ProjectDetailPage,
});
const projectArchitectureRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/projects/$id/architecture",
  component: ProjectArchitecturePage,
});
const projectSettingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/projects/$id/settings",
  component: ProjectSettingsPage,
});
const projectTasksRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/projects/$id/tasks",
  component: ProjectTasksPage,
});
const repositoriesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/repositories",
  component: RepositoriesPage,
});
const runDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/runs/$id",
  component: RunDetailPage,
});
const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings",
  component: SettingsPage,
});
const integrationsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings/integrations",
  component: IntegrationsPage,
});
const policiesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings/policies",
  component: PoliciesPage,
});
const taskDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/tasks/$id",
  component: TaskDetailPage,
});
const taskRunsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/tasks/$id/runs",
  component: TaskRunsPage,
});
const taskSpecRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/tasks/$id/spec",
  component: TaskSpecPage,
});

const routeTree = rootRoute.addChildren([
  indexRoute,
  dashboardRoute,
  architectureRoute,
  projectsRoute,
  projectDetailRoute,
  projectArchitectureRoute,
  projectSettingsRoute,
  projectTasksRoute,
  repositoriesRoute,
  runDetailRoute,
  settingsRoute,
  integrationsRoute,
  policiesRoute,
  taskDetailRoute,
  taskRunsRoute,
  taskSpecRoute,
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

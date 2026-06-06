import { create } from 'zustand';
import type { Task, Project, Repository, AgentRun, User, Organization } from './types';

interface AppState {
  // Auth
  user: User | null;
  setUser: (user: User | null) => void;

  // Organization
  organizations: Organization[];
  setOrganizations: (orgs: Organization[]) => void;
  selectedOrg: string | null;
  setSelectedOrg: (id: string | null) => void;

  // Project
  projects: Project[];
  setProjects: (projects: Project[]) => void;
  selectedProject: string | null;
  setSelectedProject: (id: string | null) => void;

  // Live updates
  activeRuns: AgentRun[];
  setActiveRuns: (runs: AgentRun[]) => void;
  updateRun: (run: AgentRun) => void;

  // UI state
  sidebarOpen: boolean;
  toggleSidebar: () => void;

  // Tasks cache
  tasks: Task[];
  setTasks: (tasks: Task[]) => void;
  updateTask: (task: Task) => void;

  // Repositories cache
  repositories: Repository[];
  setRepositories: (repos: Repository[]) => void;
}

export const useStore = create<AppState>((set) => ({
  // Auth
  user: null,
  setUser: (user) => set({ user }),

  // Organization
  organizations: [],
  setOrganizations: (organizations) => set({ organizations }),
  selectedOrg: null,
  setSelectedOrg: (id) => set({ selectedOrg: id }),

  // Project
  projects: [],
  setProjects: (projects) => set({ projects }),
  selectedProject: null,
  setSelectedProject: (id) => set({ selectedProject: id }),

  // Live updates
  activeRuns: [],
  setActiveRuns: (runs) => set({ activeRuns: runs }),
  updateRun: (run) =>
    set((state) => ({
      activeRuns: state.activeRuns.map((r) =>
        r.id === run.id ? run : r
      ),
    })),

  // UI state
  sidebarOpen: true,
  toggleSidebar: () =>
    set((state) => ({ sidebarOpen: !state.sidebarOpen })),

  // Tasks cache
  tasks: [],
  setTasks: (tasks) => set({ tasks }),
  updateTask: (task) =>
    set((state) => ({
      tasks: state.tasks.map((t) =>
        t.id === task.id ? task : t
      ),
    })),

  // Repositories cache
  repositories: [],
  setRepositories: (repositories) => set({ repositories }),
}));

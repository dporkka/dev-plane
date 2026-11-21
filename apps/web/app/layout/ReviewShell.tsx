"use client";

import { api } from "@/lib/api";
import { isReviewNavigationActive } from "@/lib/review-navigation";
import { useStore } from "@/lib/store";
import {
	Activity,
	BarChart3,
	Bot,
	CheckCircle2,
	ChevronDown,
	Code2,
	FolderGit2,
	GitPullRequest,
	Github,
	LayoutDashboard,
	Search,
	Settings,
	ShieldCheck,
	Terminal,
	X,
	Zap,
} from "lucide-react";
import Image from "next/image";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useRef, useState } from "react";

const navigation = [
	{ href: "/reviews", name: "PR Reviews", icon: GitPullRequest },
	{ href: "/reviews/triage", name: "External triage", icon: GitPullRequest },
	{ href: "/dashboard", name: "Verify", icon: CheckCircle2 },
	{ href: "/settings/policies", name: "Security", icon: ShieldCheck },
	{ href: "/tasks", name: "Coding Agent", icon: Code2 },
	{ href: "/dashboard", name: "Automations", icon: Zap },
	{ href: "/reviews/analytics", name: "Analytics", icon: BarChart3 },
];

const tabs = [
	{ href: "/reviews", name: "PR reviews", icon: GitPullRequest },
	{ href: "/reviews/triage", name: "External triage", icon: GitPullRequest },
	{ href: "/reviews/analytics", name: "Review analytics", icon: BarChart3 },
	{ href: "/dashboard", name: "CI/CD reviews", icon: Terminal },
	{ href: "/tasks", name: "Coding Agent", icon: Bot },
	{ href: "/projects", name: "Projects", icon: FolderGit2 },
];

export function ReviewShell({ children }: { children: React.ReactNode }) {
	const pathname = usePathname();
	const { user, organizations, selectedOrg, setSelectedOrg } = useStore();
	const [search, setSearch] = useState("");
	const [mobileOpen, setMobileOpen] = useState(false);
	const searchRef = useRef<HTMLInputElement>(null);
	const menuButtonRef = useRef<HTMLButtonElement>(null);

	const closeMobileNavigation = () => {
		setMobileOpen(false);
		menuButtonRef.current?.focus();
	};

	useEffect(() => {
		const onKeyDown = (event: KeyboardEvent) => {
			if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
				event.preventDefault();
				searchRef.current?.focus();
			}
		};
		window.addEventListener("keydown", onKeyDown);
		return () => window.removeEventListener("keydown", onKeyDown);
	}, []);

	return (
		<div className="flex min-h-screen bg-white text-[#171717]">
			{mobileOpen && (
				<button
					type="button"
					aria-label="Dismiss navigation backdrop"
					onClick={() => setMobileOpen(false)}
					className="fixed inset-0 z-30 bg-black/30 lg:hidden"
				/>
			)}
			<aside
				id="review-primary-sidebar"
				className={`${mobileOpen ? "flex" : "hidden"} lg:flex fixed lg:sticky top-0 left-0 z-40 lg:z-auto w-[248px] xl:w-[282px] shrink-0 flex-col border-r border-[#e5e5e5] bg-[#fafafa] h-screen`}
			>
				<div className="h-14 px-4 border-b border-[#ededed] flex items-center gap-2">
					<div className="w-7 h-7 rounded-lg bg-[#1b1b1b] text-white flex items-center justify-center">
						<Activity size={17} />
					</div>
					<span className="font-semibold text-sm tracking-tight">
						Dev Plane
					</span>
					<span className="ml-auto rounded-md bg-[#ececec] px-2 py-0.5 text-xs text-[#676767]">
						Review
					</span>
					<ChevronDown size={14} className="text-[#777]" />
					<button
						type="button"
						aria-label="Close navigation"
						onClick={closeMobileNavigation}
						className="lg:hidden inline-flex items-center justify-center rounded-md p-1 text-[#555] hover:bg-[#e9e9e9] focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1"
					>
						<X size={18} aria-hidden="true" />
					</button>
				</div>

				<div className="px-2 pt-3 pb-2">
					<div className="relative">
						<Search className="absolute left-3 top-2.5 text-[#888]" size={16} />
						<input
							ref={searchRef}
							value={search}
							onChange={(event) => setSearch(event.target.value)}
							aria-label="Search navigation"
							placeholder="Search navigation"
							className="w-full bg-[#efefef] border-none rounded-lg h-9 pl-9 pr-12 text-sm focus:outline-none focus:ring-2 focus:ring-[#aaa]"
						/>
						<span className="absolute right-3 top-2.5 text-[11px] text-[#888]">
							Ctrl K
						</span>
					</div>
				</div>

				<nav aria-label="Primary" className="px-2 pt-2 flex-1 space-y-1">
					{navigation
						.filter((item) =>
							item.name.toLowerCase().includes(search.toLowerCase()),
						)
						.map((item) => {
							const active = isReviewNavigationActive(item.href, pathname);
							return (
								<Link
									key={item.name}
									href={item.href}
									onClick={() => setMobileOpen(false)}
									className={`flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm transition-colors ${active ? "bg-[#eaeaea] font-medium" : "hover:bg-[#efefef] text-[#3d3d3d]"}`}
								>
									<item.icon size={18} strokeWidth={1.8} />
									{item.name}
								</Link>
							);
						})}
				</nav>

				<div className="border-t border-[#e5e5e5] px-2 py-3 space-y-1">
					<Link
						href="/repositories"
						className="flex items-center gap-3 px-3 py-2 rounded-lg text-sm hover:bg-[#ececec]"
					>
						<FolderGit2 size={18} />
						Repositories
					</Link>
					<Link
						href="/settings"
						className="flex items-center gap-3 px-3 py-2 rounded-lg text-sm hover:bg-[#ececec]"
					>
						<Settings size={18} />
						Organization settings
					</Link>
				</div>
				<div className="border-t border-[#e5e5e5] px-3 py-3 flex items-center gap-3">
					{user?.avatar_url ? (
						<Image
							src={user.avatar_url}
							alt=""
							width={32}
							height={32}
							unoptimized
							className="w-8 h-8 rounded-full"
						/>
					) : (
						<div className="w-8 h-8 rounded-full bg-indigo-100 text-indigo-700 flex items-center justify-center text-xs font-semibold">
							{(user?.name || user?.email || "D")[0].toUpperCase()}
						</div>
					)}
					<div className="min-w-0 flex-1">
						<p className="truncate text-sm font-medium">
							{user?.name || user?.email || "Dev Plane"}
						</p>
						<p className="text-xs text-[#777]">{user?.role || "Workspace"}</p>
					</div>
					<LayoutDashboard size={16} className="text-[#888]" />
				</div>
			</aside>
			<div className="flex min-w-0 flex-1 flex-col">
				<header className="flex h-14 shrink-0 items-center justify-between border-b border-[#e6e6e6] bg-white px-4">
					<div className="flex items-center gap-1 h-full min-w-0 overflow-x-auto">
						<button
							type="button"
							aria-label="Open navigation"
							aria-expanded={mobileOpen}
							aria-controls="review-primary-sidebar"
							ref={menuButtonRef}
							onClick={() => setMobileOpen(!mobileOpen)}
							className="lg:hidden mr-2 px-2 py-1 border rounded-md"
						>
							Menu
						</button>
						{tabs.map((tab) => (
							<Link
								key={tab.name}
								href={tab.href}
								className={`h-full whitespace-nowrap flex items-center gap-2 border-b-2 px-3 text-sm ${isReviewNavigationActive(tab.href, pathname) ? "border-[#fb563e] text-[#1c1c1c] font-medium" : "border-transparent text-[#727272] hover:text-black"}`}
							>
								<tab.icon size={17} />
								{tab.name}
							</Link>
						))}
					</div>
					{!user ? (
						<button
							type="button"
							onClick={() => api.githubAuth()}
							className="ml-3 shrink-0 flex items-center gap-2 rounded-lg bg-[#171717] text-white text-xs px-3 py-2 hover:bg-[#333]"
						>
							<Github size={15} /> Sign in
						</button>
					) : (
						<label className="ml-3 flex items-center gap-2 text-xs text-[#777]">
							<span className="hidden xl:inline">Organization</span>
							<select
								aria-label="Organization"
								value={selectedOrg || ""}
								onChange={(e) => setSelectedOrg(e.target.value)}
								className="max-w-[145px] bg-white border border-[#e8e8e8] rounded-lg py-1.5 px-2 text-sm text-[#333]"
							>
								{!selectedOrg && <option value="">Select</option>}
								{organizations.map((org) => (
									<option value={org.id} key={org.id}>
										{org.name}
									</option>
								))}
							</select>
						</label>
					)}
				</header>
				<div className="flex-1 min-w-0">{children}</div>
			</div>
		</div>
	);
}

/**
 * Distinguish task-owned Dev Plane PR reviews from external GitHub snapshots.
 * Both are advisory read views, but they have different completeness contracts.
 */
export const reviewSections = [
	{ href: "/reviews", name: "PR reviews" },
	{ href: "/reviews/triage", name: "External triage" },
	{ href: "/reviews/analytics", name: "Review analytics" },
] as const;

export function isReviewNavigationActive(
	href: string,
	pathname: string,
): boolean {
	if (href === "/reviews") {
		return (
			(pathname === "/reviews" || pathname.startsWith("/reviews/")) &&
			!["/reviews/triage", "/reviews/analytics"].some(
				(section) => pathname === section || pathname.startsWith(`${section}/`),
			)
		);
	}
	return pathname === href || pathname.startsWith(`${href}/`);
}

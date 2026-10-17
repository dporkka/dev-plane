/**
 * Converts the Studio's human-reviewed brief form into the existing Dev Plane
 * handoff API contract. This is a client-side UX check, not a server-side
 * authorization or URL-fetching security boundary.
 */
export interface BriefHandoffForm {
  repositoryId: string;
  title: string;
  description?: string;
  briefProjectId?: string;
  briefUrl: string;
  briefContent: string;
  acceptanceCriteria: string;
}

export interface BriefHandoffPayload {
  repository_id: string;
  brief_project_id?: string;
  brief_url?: string;
  title: string;
  description?: string;
  priority: "medium";
  risk_level: "medium";
  acceptance_criteria: string[];
  documents?: Array<{ slug: string; title: string; content: string }>;
}

export function buildBriefHandoffPayload(input: BriefHandoffForm): BriefHandoffPayload {
  const repositoryId = input.repositoryId.trim();
  if (!repositoryId) {
    throw new Error("Select a connected repository.");
  }

  const briefContent = input.briefContent.trim();
  const briefUrl = input.briefUrl.trim();
  if (Boolean(briefContent) === Boolean(briefUrl)) {
    throw new Error("Provide exactly one brief source: pasted Markdown or an HTTPS link.");
  }
  if (briefContent.length > 80000) {
    throw new Error("Pasted briefs must not exceed 80,000 characters.");
  }

  if (briefUrl) {
    let url: URL;
    try {
      url = new URL(briefUrl);
    } catch {
      throw new Error("Enter a valid absolute HTTPS brief URL.");
    }
    if (url.protocol !== "https:" || !url.hostname) {
      throw new Error("The brief link must use HTTPS.");
    }
    if (url.username || url.password) {
      throw new Error("Do not include credentials in brief links.");
    }
    if (url.search || url.hash) {
      throw new Error("Remove query parameters and fragments from the brief link; they may contain private access tokens.");
    }
    // The backend stores this pointer without fetching it. Runtime URL access
    // must still enforce its own network/SSRF policy.
  }

  const acceptanceCriteria = Array.from(
    new Map(
      input.acceptanceCriteria
        .split(/\r?\n/)
        .map((line) => line.trim())
        .filter(Boolean)
        .map((line) => [line.toLocaleLowerCase(), line] as const),
    ).values(),
  );
  if (acceptanceCriteria.length === 0) {
    throw new Error("Add at least one acceptance criterion, one per line.");
  }
  if (acceptanceCriteria.length > 30 || acceptanceCriteria.some((line) => line.length > 500)) {
    throw new Error("Use at most 30 acceptance criteria of 500 characters each.");
  }

  const briefProjectId = input.briefProjectId?.trim();
  const description = input.description?.trim();

  return {
    repository_id: repositoryId,
    ...(briefProjectId ? { brief_project_id: briefProjectId } : {}),
    ...(briefUrl ? { brief_url: briefUrl } : {}),
    title: input.title.trim() || "Build from Builder's Brief",
    ...(description ? { description } : {}),
    priority: "medium",
    risk_level: "medium",
    acceptance_criteria: acceptanceCriteria,
    ...(briefContent
      ? {
          documents: [
            { slug: "builder-brief", title: "Builder's Brief", content: briefContent },
          ],
        }
      : {}),
  };
}

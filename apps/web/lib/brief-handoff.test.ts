import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { buildBriefHandoffPayload } from "./brief-handoff";

describe("buildBriefHandoffPayload", () => {
  it("creates a self-contained handoff from pasted Markdown without making the brief public", () => {
    const payload = buildBriefHandoffPayload({
      repositoryId: " repo-1 ",
      title: " Customer portal ",
      briefContent: " # Product requirements\nBuild the customer portal. ",
      briefUrl: "",
      briefProjectId: " brief-42 ",
      description: " Include access control. ",
      acceptanceCriteria:
        " Users can sign in.\n\n Reports are tenant-scoped.\nusers can sign in.",
    });

    assert.deepEqual(payload, {
      repository_id: "repo-1",
      brief_project_id: "brief-42",
      title: "Customer portal",
      description: "Include access control.",
      priority: "medium",
      risk_level: "medium",
      acceptance_criteria: ["Users can sign in.", "Reports are tenant-scoped."],
      documents: [
        {
          slug: "builder-brief",
          title: "Builder's Brief",
          content: "# Product requirements\nBuild the customer portal.",
        },
      ],
    });
  });

  it("accepts a public HTTPS brief link without embedding documents", () => {
    const payload = buildBriefHandoffPayload({
      repositoryId: "repo-2",
      title: "Website",
      briefContent: "",
      briefUrl: " https://build.davidporkka.com/public/briefs/example ",
      acceptanceCriteria: "Landing page has a working contact form.",
    });
    assert.equal(
      payload.brief_url,
      "https://build.davidporkka.com/public/briefs/example",
    );
    assert.equal(payload.documents, undefined);
    assert.equal(payload.risk_level, "medium");
  });

  it("rejects missing repo, missing brief, or missing acceptance criteria", () => {
    const base = {
      repositoryId: "repo",
      title: "Website",
      briefContent: "# Requirements",
      briefUrl: "",
      acceptanceCriteria: "Works in the browser",
    };
    assert.throws(
      () => buildBriefHandoffPayload({ ...base, repositoryId: " " }),
      /repository/i,
    );
    assert.throws(
      () => buildBriefHandoffPayload({ ...base, briefContent: " " }),
      /brief/i,
    );
    assert.throws(
      () => buildBriefHandoffPayload({ ...base, acceptanceCriteria: " \n " }),
      /acceptance/i,
    );
  });

  it("rejects insecure or credential-bearing links and ambiguous sources", () => {
    const base = {
      repositoryId: "repo",
      title: "Website",
      briefContent: "",
      briefUrl: "https://example.com/brief",
      acceptanceCriteria: "Works in the browser",
    };
    assert.throws(
      () =>
        buildBriefHandoffPayload({
          ...base,
          briefUrl: "http://example.com/brief",
        }),
      /HTTPS/,
    );
    assert.throws(
      () =>
        buildBriefHandoffPayload({
          ...base,
          briefUrl: "https://user:pass@example.com/brief",
        }),
      /credential/i,
    );
    assert.throws(
      () =>
        buildBriefHandoffPayload({
          ...base,
          briefUrl: "https://example.com/brief?token=secret",
        }),
      /query|fragment/i,
    );
    assert.throws(
      () =>
        buildBriefHandoffPayload({ ...base, briefContent: "# Requirements" }),
      /one brief source/i,
    );
  });

  it("rejects oversized pasted briefs to avoid unbounded task payloads", () => {
    assert.throws(
      () =>
        buildBriefHandoffPayload({
          repositoryId: "repo",
          title: "Website",
          briefContent: "x".repeat(80001),
          briefUrl: "",
          acceptanceCriteria: "Works",
        }),
      /80,000/,
    );
  });
});

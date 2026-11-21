import assert from "node:assert/strict";
import test from "node:test";
import { isReviewNavigationActive, reviewSections } from "./review-navigation";

test("external and internal PR reviews are separate navigation sections", () => {
	assert.ok(
		reviewSections.some((section) => section.href === "/reviews/triage"),
	);
	assert.ok(reviewSections.some((section) => section.href === "/reviews"));
	assert.ok(
		reviewSections.some((section) => section.href === "/reviews/analytics"),
	);
	const urls = reviewSections.map((section) => section.href);
	assert.equal(new Set(urls).size, urls.length);
});

test("external triage is active only on its own route", () => {
	for (const path of ["/reviews/triage", "/reviews/triage/foo"]) {
		assert.equal(isReviewNavigationActive("/reviews/triage", path), true);
		assert.equal(isReviewNavigationActive("/reviews", path), false);
		assert.equal(isReviewNavigationActive("/reviews/analytics", path), false);
	}
});

test("internal review details keep the PR inbox tab selected", () => {
	for (const path of ["/reviews", "/reviews/pr-123"]) {
		assert.equal(isReviewNavigationActive("/reviews", path), true);
		assert.equal(isReviewNavigationActive("/reviews/triage", path), false);
	}
});

test("analytics is never mistaken for the PR inbox", () => {
	assert.equal(
		isReviewNavigationActive("/reviews/analytics", "/reviews/analytics"),
		true,
	);
	assert.equal(
		isReviewNavigationActive("/reviews", "/reviews/analytics"),
		false,
	);
});

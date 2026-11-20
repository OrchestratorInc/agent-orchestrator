import assert from "node:assert/strict";
import test from "node:test";
import { dailySeries, niceStep, renderSvg } from "./star-history.mjs";

test("dailySeries counts stars cumulatively through today", () => {
	const series = dailySeries(["2026-02-14T10:00:00Z", "2026-02-13T23:59:59Z", "2026-02-16T00:00:00Z"], {
		createdAt: "2026-02-13T09:52:36Z",
		now: Date.parse("2026-02-17T12:00:00Z"),
	});
	assert.deepEqual(
		series.map(({ day, stars }) => [new Date(day).toISOString().slice(0, 10), stars]),
		[
			["2026-02-13", 1],
			["2026-02-14", 2],
			["2026-02-15", 2],
			["2026-02-16", 3],
			["2026-02-17", 3],
		],
	);
});

test("dailySeries handles a repository with no stars", () => {
	const series = dailySeries([], { createdAt: "2026-02-13T09:52:36Z", now: Date.parse("2026-02-14T00:00:00Z") });
	assert.deepEqual(
		series.map((point) => point.stars),
		[0, 0],
	);
});

test("niceStep picks round gridline steps", () => {
	assert.equal(niceStep(14_968), 5000);
	assert.equal(niceStep(9_000), 2000);
	assert.equal(niceStep(40), 10);
	assert.equal(niceStep(0), 1);
});

test("renderSvg states the latest count and date and labels the current month", () => {
	const series = dailySeries(["2026-02-14T10:00:00Z", "2026-11-20T10:00:00Z"], {
		createdAt: "2026-02-13T09:52:36Z",
		now: Date.parse("2026-11-20T15:00:00Z"),
	});
	for (const theme of ["light", "dark"]) {
		const svg = renderSvg(series, { repo: "Acme/<repo>", theme });
		assert.match(svg, /2 stars · Nov 20, 2026/);
		assert.match(svg, />Nov</);
		assert.match(svg, />Mar 2026</);
		assert.match(svg, /Acme\/&lt;repo&gt;/);
		assert.ok(!svg.includes("Acme/<repo>"));
	}
});

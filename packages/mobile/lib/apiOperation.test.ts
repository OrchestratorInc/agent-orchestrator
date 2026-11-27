import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { API_ROUTE_WORDS, apiOperation, isApiOperation } from "./apiOperation";

describe("apiOperation", () => {
	it("keeps route words and replaces ids", () => {
		expect(apiOperation("post", "/api/v1/sessions/agent-orchestrator-mo-402/send")).toBe("POST /api/v1/sessions/:id/send");
		expect(apiOperation(undefined, "/api/v1/prs/12/merge")).toBe("GET /api/v1/prs/:id/merge");
		expect(apiOperation("GET", "/api/v1/notifications/read-all")).toBe("GET /api/v1/notifications/read-all");
	});

	it("drops the query string", () => {
		expect(apiOperation("GET", "/api/v1/sessions?project=secret-project&active=false")).toBe("GET /api/v1/sessions");
	});

	// A file named like a route word is still a file.
	it("collapses a file tail even when its name is a route word", () => {
		expect(apiOperation("GET", "/api/v1/sessions/s1/preview/files/settings")).toBe("GET /api/v1/sessions/:id/preview/files/*");
		expect(apiOperation("GET", "/api/v1/sessions/s1/artifact-files/tree")).toBe("GET /api/v1/sessions/:id/artifact-files/*");
	});

	it("keeps the route words after a render id", () => {
		expect(apiOperation("GET", "/api/v1/sessions/s1/renders/r1")).toBe("GET /api/v1/sessions/:id/renders/:id");
	});

	it("accepts only its own operation shape", () => {
		expect(isApiOperation(apiOperation("POST", "/api/v1/sessions/acme-1/kill"))).toBe(true);
		expect(isApiOperation("GET /api/v1/sessions/:id/preview/files/*")).toBe(true);
		expect(isApiOperation("GET /api/v1/projects/acme-secret/sessions")).toBe(false);
		expect(isApiOperation("GET /api/v1/*/sessions")).toBe(false);
		expect(isApiOperation("GET api/v1/sessions")).toBe(false);
		expect(isApiOperation(42)).toBe(false);
	});

	it("collapses a workspace file path to *", () => {
		expect(apiOperation("GET", "/api/v1/sessions/s1/preview/files/__ao_artifacts__/site/my%20page.html"))
			.toBe("GET /api/v1/sessions/:id/preview/files/*");
		expect(apiOperation("GET", "/api/v1/sessions/s1/files")).toBe("GET /api/v1/sessions/:id/files");
	});

	// The privacy contract: no project, session, branch or file name survives.
	it("never forwards a segment that is not a route word", () => {
		const operation = apiOperation("POST", "/api/v1/projects/acme-secret/sessions/feat%2Fprivate-branch/reviews/review%2F1/conversation");
		expect(operation).toBe("POST /api/v1/projects/:id/sessions/:id/reviews/:id/conversation");
		expect(operation).not.toMatch(/acme|secret|private|branch|review%2F1/);
	});

	it("names an unexpected method OTHER", () => {
		expect(apiOperation("TRACE", "/api/v1/sessions")).toBe("OTHER /api/v1/sessions");
	});

	// Fails when a request the app builds uses a route word that is not listed,
	// so the list keeps up with the routes (a missing word would read as :id).
	it("lists every route word the app's requests use", () => {
		const words = new Set<string>();
		const add = (path: string) => {
			for (const segment of path.replace(/\$\{[^}]*\}/g, "").split(/[/?]/)) {
				if (/^[a-z][a-z0-9-]*$/.test(segment)) words.add(segment);
			}
		};
		for (const file of sourceFiles(join(__dirname, ".."))) {
			const source = readFileSync(file, "utf8");
			for (const [, path] of source.matchAll(/[`"](?:\$\{API\}|\/api\/v1)(\/[^`"?]*)/g)) add(path);
			for (const [, path] of source.matchAll(/sessionPath\([^,]+, "([^"]+)"/g)) add(path);
		}
		expect(words.size).toBeGreaterThan(40);
		expect([...words].filter((word) => !API_ROUTE_WORDS.has(word)).sort()).toEqual([]);
	});
});

function sourceFiles(root: string): string[] {
	const out: string[] = [];
	for (const dir of ["lib", "app"]) {
		for (const entry of readdirSync(join(root, dir), { recursive: true, encoding: "utf8" })) {
			if (/\.tsx?$/.test(entry) && !/\.test\.tsx?$/.test(entry)) out.push(join(root, dir, entry));
		}
	}
	return out;
}

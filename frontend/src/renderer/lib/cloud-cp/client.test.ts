import { describe, expect, it, vi } from "vitest";
import { createCloudCpClient, MAX_GITHUB_REPOSITORY_DESCRIPTION, retryAfterMs } from "./client";
import { CloudCpError } from "./errors";

describe("Cloud control-plane interface transitions", () => {
	it("preserves caller message ids for Cloud send retries and steering", async () => {
		const fetchImpl = vi.fn().mockImplementation(async () => new Response(JSON.stringify({ event: {} }), { status: 202, headers: { "Content-Type": "application/json" } }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test", getToken: async () => "token", fetchImpl });
		await client.sendSessionMessage("org", "session", { text: "retry me" }, { idempotencyKey: "stable-message-id" });
		await client.steerTurn("org", "session", "turn", { text: "change course" }, { idempotencyKey: "stable-steer-id" });
		expect((fetchImpl.mock.calls[0]?.[1] as RequestInit).headers).toBeInstanceOf(Headers);
		expect(((fetchImpl.mock.calls[0]?.[1] as RequestInit).headers as Headers).get("Idempotency-Key")).toBe("stable-message-id");
		expect(fetchImpl.mock.calls[1]?.[0]).toContain("/turns/turn/steer");
		expect(((fetchImpl.mock.calls[1]?.[1] as RequestInit).headers as Headers).get("Idempotency-Key")).toBe("stable-steer-id");
	});
	it("routes a Cloud review conversation and prompt suggestion to their session routes", async () => {
		const fetchImpl = vi.fn().mockImplementation(async () => new Response(JSON.stringify({ events: [], hasMore: false, nextAfter: 0, event: {}, turnId: "t", prompt: "" }), { status: 202, headers: { "Content-Type": "application/json" } }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test", getToken: async () => "token", fetchImpl });
		await client.listReviewChatEvents("org", "session", "run", { after: 7, limit: 500 });
		await client.sendReviewChatMessage("org", "session", "run", "Why?", { idempotencyKey: "message-1" });
		await client.interruptReviewChat("org", "session", "run");
		await client.suggestChatPrompt("org", "session", { turnId: "t", messages: [{ role: "user", text: "Hi" }] });
		const calls = fetchImpl.mock.calls as Array<[string, RequestInit]>;
		expect(calls[0][0]).toBe("https://cloud.example.test/api/cloud/v1/orgs/org/sessions/session/reviews/run/chat-events?after=7&limit=500");
		expect(calls[1][0]).toContain("/orgs/org/sessions/session/reviews/run/chat/messages");
		expect(JSON.parse(calls[1][1].body as string)).toEqual({ text: "Why?" });
		expect(new Headers(calls[1][1].headers).get("Idempotency-Key")).toBe("message-1");
		expect(calls[2][0]).toContain("/orgs/org/sessions/session/reviews/run/chat/interrupt");
		expect(calls[3][0]).toContain("/orgs/org/sessions/session/chat/prompt-suggestion");
		expect(JSON.parse(calls[3][1].body as string)).toEqual({ turnId: "t", messages: [{ role: "user", text: "Hi" }] });
	});
	it("routes an ACP approval decision to the session request", async () => {
		const fetchImpl = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 202, headers: { "Content-Type": "application/json" } }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test/api/cloud/v1", getToken: async () => "token", fetchImpl });
		await client.decideChatApproval("org", "session", "request", "allow-once");
		expect(fetchImpl.mock.calls[0]?.[0]).toContain("/orgs/org/sessions/session/approvals/request/decide");
		expect(JSON.parse((fetchImpl.mock.calls[0]?.[1] as RequestInit).body as string)).toEqual({ decisionId: "allow-once" });
	});

	it("cancels an active interface transition through the Cloud API", async () => {
		const fetchImpl = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({ ok: true }), {
				status: 200,
				headers: { "Content-Type": "application/json" },
			}),
		);
		const client = createCloudCpClient({
			baseUrl: "https://cloud.example.test",
			getToken: async () => "bearer-token",
			fetchImpl,
		});

		await expect(client.cancelInterfaceTransition("org/a", "session b")).resolves.toEqual({ ok: true });
		expect(fetchImpl).toHaveBeenCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2Fa/sessions/session%20b/interface-transition",
			expect.objectContaining({ method: "DELETE" }),
		);
	});

	it("acknowledges an interface transition notice through the Cloud API", async () => {
		const fetchImpl = vi.fn().mockResolvedValue(
			new Response(JSON.stringify({ ok: true }), {
				status: 200,
				headers: { "Content-Type": "application/json" },
			}),
		);
		const client = createCloudCpClient({
			baseUrl: "https://cloud.example.test",
			getToken: async () => "bearer-token",
			fetchImpl,
		});

		await expect(
			client.acknowledgeInterfaceTransitionNotice("org/a", "session b", "transition/c"),
		).resolves.toEqual({ ok: true });
		expect(fetchImpl).toHaveBeenCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2Fa/sessions/session%20b/interface-transition/transition%2Fc/notice-acknowledgement",
			expect.objectContaining({ method: "PUT" }),
		);
	});
});

describe("cloud control-plane Coder template parameters", () => {
	it("reads one template's parameters from the encoded coder-templates route", async () => {
		const parameters = [{ name: "region", type: "string", mutable: true, required: false, defaultValue: "eu" }];
		const fetchImpl = vi.fn().mockResolvedValue(new Response(JSON.stringify({ parameters }), { status: 200, headers: { "Content-Type": "application/json" } }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test", getToken: async () => "token", fetchImpl });
		await expect(client.getCoderTemplateParameters("org/a", "tpl 1")).resolves.toEqual({ parameters });
		expect(fetchImpl).toHaveBeenCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2Fa/coder-templates/tpl%201/parameters",
			expect.objectContaining({ method: "GET" }),
		);
	});

	it("evaluates a template's parameters with draft values", async () => {
		const parameters = [{ name: "other_github_repo", type: "string", mutable: false, required: false, defaultValue: "" }];
		const fetchImpl = vi.fn().mockResolvedValue(new Response(JSON.stringify({ parameters }), { status: 200, headers: { "Content-Type": "application/json" } }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test", getToken: async () => "token", fetchImpl });
		await expect(client.evaluateCoderTemplateParameters("org/a", "tpl 1", { github_repos: "[\"Other\"]" })).resolves.toEqual({ parameters });
		const [url, init] = fetchImpl.mock.calls[0];
		expect(url).toBe("https://cloud.example.test/api/cloud/v1/orgs/org%2Fa/coder-templates/tpl%201/parameters/evaluate");
		expect(init).toMatchObject({ method: "POST" });
		expect(JSON.parse(String(init.body))).toEqual({ values: { github_repos: "[\"Other\"]" } });
	});
});

describe("cloud control-plane harness versions", () => {
	const ok = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

	it("lists the organization's harness versions, refreshing the newest releases on request", async () => {
		const fetchImpl = vi.fn().mockImplementation(async () => ok({ editable: true, agents: [] }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test", getToken: async () => "token", fetchImpl });
		await expect(client.listOrgHarnessVersions("org/a")).resolves.toEqual({ editable: true, agents: [] });
		await client.listOrgHarnessVersions("org/a", { refresh: true });
		expect(fetchImpl.mock.calls[0][0]).toBe("https://cloud.example.test/api/cloud/v1/orgs/org%2Fa/harness-versions");
		expect(fetchImpl.mock.calls[1][0]).toBe("https://cloud.example.test/api/cloud/v1/orgs/org%2Fa/harness-versions?refresh=true");
	});

	it("updates a harness to the version the caller saw, and resets it to the AO-tested one", async () => {
		const fetchImpl = vi.fn().mockImplementation(async () => ok({ agentId: "codex" }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test", getToken: async () => "token", fetchImpl });
		await client.installOrgHarnessVersion("org-1", "codex", "0.167.0");
		const [url, init] = fetchImpl.mock.calls[0];
		expect(url).toBe("https://cloud.example.test/api/cloud/v1/orgs/org-1/harness-versions/codex/install");
		expect(init).toMatchObject({ method: "POST" });
		expect(JSON.parse(String(init.body))).toEqual({ method: "ao-cloud", operation: "update", expectedVersion: "0.167.0" });
		await client.resetOrgHarnessVersion("org-1", "codex");
		expect(fetchImpl.mock.calls[1][0]).toBe("https://cloud.example.test/api/cloud/v1/orgs/org-1/harness-versions/codex");
		expect(fetchImpl.mock.calls[1][1]).toMatchObject({ method: "DELETE" });
	});
});

describe("cloud control-plane new-task model catalogs", () => {
	it("loads the agent's catalog from the encoded organization route", async () => {
		const catalog = { models: [{ id: "claude-sonnet-test", displayName: "Sonnet", default: true }], source: "release", cliVersion: "2.1.287" };
		const fetchImpl = vi.fn().mockResolvedValue(new Response(JSON.stringify(catalog), { status: 200, headers: { "Content-Type": "application/json" } }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test", getToken: async () => "token", fetchImpl });

		await expect(client.listUserAgentModels("org/a", "claude-code")).resolves.toEqual(catalog);
		expect(fetchImpl).toHaveBeenCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2Fa/providers/claude-code/models",
			expect.objectContaining({ method: "GET" }),
		);
	});

	// The composer tells an older control plane (no route, no envelope) from a
	// handler's own 404 and from an agent with no Cloud catalog by these fields.
	it.each([
		{ status: 409, body: JSON.stringify({ error: "Conflict", code: "MODEL_CATALOG_UNAVAILABLE", message: "No catalog" }), code: "MODEL_CATALOG_UNAVAILABLE" },
		{ status: 404, body: JSON.stringify({ error: "Not Found", code: "not_found", message: "Organization not found" }), code: "not_found" },
		{ status: 404, body: "404 page not found", code: undefined },
	])("reports a $status catalog failure with code $code", async ({ status, body, code }) => {
		const fetchImpl = vi.fn().mockResolvedValue(new Response(body, { status }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test", getToken: async () => "token", fetchImpl });

		await expect(client.listUserAgentModels("org", "opencode")).rejects.toMatchObject({ name: "CloudCpError", status, code });
	});
});

describe("cloud control-plane session lifecycle", () => {
	it("uses the cloud review endpoints for an encoded session", async () => {
		const fetchMock = vi.fn(
			async () =>
				new Response(JSON.stringify({ sessionId: "session/1", reviews: [], runs: [] }), {
					status: 201,
					headers: { "Content-Type": "application/json" },
				}),
		);
		const client = createCloudCpClient({
			baseUrl: "https://cloud.example.test/",
			getToken: async () => "token",
			fetchImpl: fetchMock as typeof fetch,
		});

		await client.triggerSessionReviews("org/1", "session/1");

		expect(fetchMock).toHaveBeenCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/reviews/trigger",
			expect.objectContaining({ method: "POST" }),
		);
	});

	it("asks the control plane to deliver a stored review to the worker", async () => {
		const fetchMock = vi.fn(
			async (_input: RequestInfo | URL, _init?: RequestInit) =>
				new Response(JSON.stringify({ event: { sequence: 12 } }), {
					status: 202,
					headers: { "Content-Type": "application/json" },
				}),
		);
		const client = createCloudCpClient({
			baseUrl: "https://cloud.example.test/",
			getToken: async () => "token",
			fetchImpl: fetchMock as typeof fetch,
		});

		await client.sendSessionReviewToWorker("org/1", "session/1", "run/1", {
			idempotencyKey: "review-run-1",
		});

		expect(fetchMock).toHaveBeenCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/reviews/run%2F1/send",
			expect.objectContaining({ method: "POST" }),
		);
		const request = fetchMock.mock.calls[0]?.[1] as RequestInit;
		expect(new Headers(request.headers).get("Idempotency-Key")).toBe("review-run-1");
	});

	it("inspects and installs only through typed cloud harness routes", async () => {
		const fetchMock = vi.fn(
			async () =>
				new Response(JSON.stringify({ harness: "cursor", status: "ready" }), {
					status: 200,
					headers: { "Content-Type": "application/json" },
				}),
		);
		const client = createCloudCpClient({
			baseUrl: "https://cloud.example.test/",
			getToken: async () => "token",
			fetchImpl: fetchMock as typeof fetch,
		});

		await client.installSessionReviewerHarness("org/1", "session/1", "cursor");

		expect(fetchMock).toHaveBeenCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/reviewer-harnesses/cursor/install",
			expect.objectContaining({ method: "POST" }),
		);
	});
	it("pages startup events through JSON without opening an SSE stream", async () => {
		const page = { events: [{ sessionId: "session/1", sequence: 4, type: "worker.ready", payload: {}, createdAt: "2026-09-29T00:00:00Z" }], hasMore: false, nextAfter: 4 };
		const fetchMock = vi.fn(async () => new Response(JSON.stringify(page), {
			status: 200,
			headers: { "Content-Type": "application/json" },
		}));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test", getToken: async () => "token", fetchImpl: fetchMock as typeof fetch });
		await expect(client.listChatEvents("org/1", "session/1", { after: 3, limit: 500 })).resolves.toEqual(page);
		expect(fetchMock).toHaveBeenCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/chat-events?after=3&limit=500",
			expect.objectContaining({ method: "GET" }),
		);
		expect(new Headers((fetchMock.mock.calls as unknown as Array<[string, RequestInit]>)[0]?.[1].headers).get("authorization")).toBe("Bearer token");
		await client.listChatEvents("org/1", "session/1", { after: 0, limit: 500, types: ["workspace.script.step", "agent.ready"] });
		expect(fetchMock).toHaveBeenLastCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/chat-events?after=0&limit=500&types=workspace.script.step%2Cagent.ready",
			expect.objectContaining({ method: "GET" }),
		);
		await client.listChatEvents("org/1", "session/1", { before: 40, since: "2026-11-08T10:00:00.000Z", limit: 500 });
		expect(fetchMock).toHaveBeenLastCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/chat-events?limit=500&before=40&since=2026-11-08T10%3A00%3A00.000Z",
			expect.objectContaining({ method: "GET" }),
		);
	});

	it("reads a Cloud project and patches only supplied settings", async () => {
		const fetchMock = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ project: { id: "project" } }), { headers: { "Content-Type": "application/json" } }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.test", getToken: async () => "token", fetchImpl: fetchMock });
		await client.getProject("org/1", "project/1");
		await client.updateProjectSettings("org/1", "project/1", { config: { autoReview: false } });
		expect(fetchMock.mock.calls[0]?.[0]).toBe("https://cloud.test/api/cloud/v1/orgs/org%2F1/projects/project%2F1");
		expect(fetchMock.mock.calls[1]?.[0]).toBe("https://cloud.test/api/cloud/v1/orgs/org%2F1/projects/project%2F1/settings");
		expect(fetchMock.mock.calls[1]?.[1]).toEqual(expect.objectContaining({ method: "PATCH", body: JSON.stringify({ config: { autoReview: false } }) }));
	});
	it("reads an organization's members from its encoded members route", async () => {
		const members = [{ userId: "u-1", email: "asha@acme.dev", displayName: "Asha Rao", role: "owner", createdAt: "2026-11-01T00:00:00Z" }];
		const fetchMock = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ members }), { headers: { "Content-Type": "application/json" } }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.test", getToken: async () => "token", fetchImpl: fetchMock });
		await expect(client.listOrgMembers("org/1")).resolves.toEqual({ members });
		expect(fetchMock.mock.calls[0]?.[0]).toBe("https://cloud.test/api/cloud/v1/orgs/org%2F1/members");
		expect(fetchMock.mock.calls[0]?.[1]).toEqual(expect.objectContaining({ method: "GET" }));
	});
	it("uses the organization GitHub App installation and repository routes", async () => {
		const responses = [
			{ installationUrl: "https://github.com/apps/ao/installations/new", expiresAt: "2026-09-22T12:00:00Z" },
			{ installations: [] },
			{ items: [], page: { hasMore: false } },
			{ project: { id: "project-1" } },
		];
		const fetchMock = vi.fn(async () =>
			new Response(JSON.stringify(responses.shift()), {
				status: 200,
				headers: { "Content-Type": "application/json" },
			}),
		);
		const client = createCloudCpClient({
			baseUrl: "https://cloud.example.test",
			getToken: async () => "token",
			fetchImpl: fetchMock as typeof fetch,
		});

		await client.startGitHubInstallation("org/1");
		await client.listGitHubInstallations("org/1");
		await client.listGitHubRepositories("org/1");
		await client.createGitHubProject("org/1", {
			githubRepositoryId: "42",
			displayName: "widgets",
			config: { worker: { agent: "codex" } },
		});

		const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit]>;
		expect(calls.map(([url]) => url)).toEqual([
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/github/installations/start",
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/github/installations",
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/github/repositories",
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/github/projects",
		]);
		expect(calls[0]?.[1]).toEqual(expect.objectContaining({ method: "POST" }));
		expect(calls[3]?.[1]).toEqual(expect.objectContaining({
			method: "POST",
			body: JSON.stringify({
				githubRepositoryId: "42",
				displayName: "widgets",
				config: { worker: { agent: "codex" } },
			}),
		}));
	});

	it("creates a repository for a local folder and a workspace project from it", async () => {
		const repository = { githubRepositoryId: "77", name: "notes", fullName: "acme/notes", htmlUrl: "https://github.com/acme/notes", defaultBranch: "main", visibility: "private", isPrivate: true, isArchived: false, access: "write", grantedAt: "" };
		const responses: Array<[number, unknown]> = [
			[201, { repository, created: true, pushPath: "/api/cloud/v1/orgs/org-1/github/git/acme/notes.git" }],
			[201, { project: { id: "project-1", config: { layout: "workspace" } } }],
		];
		const fetchMock = vi.fn(async () => {
			const [status, body] = responses.shift() ?? [500, {}];
			return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
		});
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test", getToken: async () => "token", fetchImpl: fetchMock as typeof fetch });

		await expect(client.createGitHubRepository("org-1", { name: "notes", owner: "acme" })).resolves.toEqual({
			repository,
			created: true,
			pushPath: "/api/cloud/v1/orgs/org-1/github/git/acme/notes.git",
		});
		await client.createGitHubProject("org-1", {
			githubRepositoryId: "77",
			displayName: "Platform",
			config: { layout: "workspace", agentStartsIn: "parent", repos: [{ url: "https://github.com/acme/api" }] },
		}, { idempotencyKey: "create-1" });

		const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit]>;
		expect(calls[0]?.[0]).toBe("https://cloud.example.test/api/cloud/v1/orgs/org-1/github/repositories");
		expect(calls[0]?.[1]).toEqual(expect.objectContaining({ method: "POST", body: JSON.stringify({ name: "notes", owner: "acme" }) }));
		expect(new Headers(calls[1]?.[1].headers).get("Idempotency-Key")).toBe("create-1");
		expect(JSON.parse(calls[1]?.[1].body as string).config.layout).toBe("workspace");
	});

	it("never sends a project's layout in a settings write, and keeps a repository description within the limit", async () => {
		const fetchMock = vi.fn(async () => new Response(JSON.stringify({ project: {} }), { status: 200, headers: { "Content-Type": "application/json" } }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test", getToken: async () => "token", fetchImpl: fetchMock as typeof fetch });

		await client.updateProjectSettings("org-1", "p-1", { config: { layout: "workspace", repos: [{ url: "https://github.com/acme/web" }] } as never });
		await client.createGitHubRepository("org-1", { name: "notes", description: "é".repeat(400) });

		const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit]>;
		expect(JSON.parse(calls[0]?.[1].body as string)).toEqual({ config: { repos: [{ url: "https://github.com/acme/web" }] } });
		expect([...JSON.parse(calls[1]?.[1].body as string).description]).toHaveLength(MAX_GITHUB_REPOSITORY_DESCRIPTION);
	});

	it("surfaces a repository creation refusal with its code and message", async () => {
		const fetchMock = vi.fn(async () => new Response(JSON.stringify({ error: "unprocessable", code: "repository_owner_required", message: "Choose the GitHub account for the new repository." }), { status: 422, headers: { "Content-Type": "application/json" } }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test", getToken: async () => "token", fetchImpl: fetchMock as typeof fetch });

		const error = await client.createGitHubRepository("org-1", { name: "notes" }).catch((caught: unknown) => caught);

		expect(error).toBeInstanceOf(CloudCpError);
		expect(error).toMatchObject({ status: 422, code: "repository_owner_required", message: "Choose the GitHub account for the new repository." });
	});

	it("loads detailed pull requests for a cloud session", async () => {
		const fetchMock = vi.fn(async () =>
			new Response(JSON.stringify({ sessionId: "session/1", pullRequests: [] }), {
				status: 200,
				headers: { "Content-Type": "application/json" },
			}),
		);
		const client = createCloudCpClient({
			baseUrl: "https://cloud.example.test/",
			getToken: async () => "token",
			fetchImpl: fetchMock as typeof fetch,
		});

		const response = await client.listSessionPullRequests("org/1", "session/1");

		expect(response).toEqual({ sessionId: "session/1", pullRequests: [] });
		expect(fetchMock).toHaveBeenCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/pull-requests",
			expect.objectContaining({ method: "GET" }),
		);
	});

	it("merges a cloud pull request with its URL and reviewed head", async () => {
		const fetchMock = vi.fn(async () => new Response(JSON.stringify({ status: "merge_accepted" }), {
			status: 202,
			headers: { "Content-Type": "application/json" },
		}));
		const client = createCloudCpClient({
			baseUrl: "https://cloud.example.test",
			getToken: async () => "token",
			fetchImpl: fetchMock as typeof fetch,
		});
		await expect(client.mergePullRequest("org/1", "session/1", 7, "https://github.com/acme/repo/pull/7", "abc123"))
			.resolves.toEqual({ status: "merge_accepted" });
		expect(fetchMock).toHaveBeenCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/pull-requests/7/merge",
			expect.objectContaining({ method: "POST", body: JSON.stringify({ prUrl: "https://github.com/acme/repo/pull/7", expectedHeadSha: "abc123" }) }),
		);
	});

	it("posts explicit resume intent for one encoded session", async () => {
		const fetchMock = vi.fn(async () =>
			new Response(
				JSON.stringify({
					session: {
						id: "session/1",
						sandboxProvider: "coder",
						desiredState: "running",
						observedState: "stopped",
					},
				}),
				{ status: 202, headers: { "Content-Type": "application/json" } },
			),
		);
		const client = createCloudCpClient({
			baseUrl: "https://cloud.example.test/",
			getToken: async () => "token",
			fetchImpl: fetchMock as typeof fetch,
		});

		const response = await client.resumeSession("org/1", "session/1");

		expect(response.session.desiredState).toBe("running");
		expect(fetchMock).toHaveBeenCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/resume",
			expect.objectContaining({ method: "POST" }),
		);
	});

	it("gets Docker workspace summary and encodes selected diff paths", async () => {
		const fetchMock = vi.fn(async () =>
			new Response(
				JSON.stringify({
					files: [],
					diffBaseRef: "HEAD",
					truncated: { combined: false, stats: false },
				}),
				{ status: 200, headers: { "Content-Type": "application/json" } },
			),
		);
		const client = createCloudCpClient({
			baseUrl: "https://cloud.example.test",
			getToken: async () => "token",
			fetchImpl: fetchMock as typeof fetch,
		});

		await client.getWorkspaceDiff("org/1", "session/1");
		await client.readWorkspaceDiffFile("org/1", "session/1", "notes/one two.txt");

		expect(fetchMock).toHaveBeenNthCalledWith(
			1,
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/workspace/diff",
			expect.anything(),
		);
		expect(fetchMock).toHaveBeenNthCalledWith(
			2,
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/workspace/file/diff?path=notes%2Fone+two.txt",
			expect.anything(),
		);
	});

	it("calls the complete provider-neutral workspace review contract", async () => {
		const fetchMock = vi.fn(async () => new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } }));
		const client = createCloudCpClient({
			baseUrl: "https://cloud.example.test",
			getToken: async () => "token",
			fetchImpl: fetchMock as typeof fetch,
		});

		await client.getWorkspaceReview("org/1", "session/1");
		await client.getWorkspaceReviewTree("org/1", "session/1", "src/lib");
		await client.searchWorkspaceReview("org/1", "session/1", { query: "app", cursor: "next", limit: 20 });
		await client.getWorkspaceReviewFile("org/1", "session/1", { path: "src/App.tsx", scope: "committed", commitSha: "abc" });
		await client.getWorkspaceReviewDiffs("org/1", "session/1", {
			scope: "staged", paths: ["src/App.tsx"], contextLines: 5, ignoreWhitespace: true, workspaceVersion: "v1",
		});
		await client.getWorkspaceReviewRevision("org/1", "session/1", {
			path: "src/App.tsx", scope: "unstaged", side: "before", workspaceVersion: "v1", expectedRevision: "r1",
		});
		await client.updateWorkspaceReviewFile("org/1", "session/1", {
			path: "src/App.tsx", content: "updated\n", expectedFileFingerprint: "fp1",
		});

		const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit]>;
		expect(calls.map(([request]) => request)).toEqual([
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/workspace/review",
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/workspace/tree?path=src%2Flib",
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/workspace/search?query=app&cursor=next&limit=20",
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/workspace/review/file?path=src%2FApp.tsx&scope=committed&commitSha=abc",
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/workspace/review/diffs",
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/workspace/review/revision?path=src%2FApp.tsx&scope=unstaged&side=before&workspaceVersion=v1&expectedRevision=r1",
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/workspace/review/file",
		]);
		expect(fetchMock).toHaveBeenNthCalledWith(5, expect.any(String), expect.objectContaining({ method: "POST", body: JSON.stringify({
			scope: "staged", paths: ["src/App.tsx"], contextLines: 5, ignoreWhitespace: true, workspaceVersion: "v1",
		}) }));
		expect(fetchMock).toHaveBeenNthCalledWith(7, expect.any(String), expect.objectContaining({ method: "PUT", body: JSON.stringify({
			path: "src/App.tsx", content: "updated\n", expectedFileFingerprint: "fp1",
		}) }));
	});

	it("posts restore intent for one deleted, encoded session", async () => {
		const fetchMock = vi.fn(async () =>
			new Response(
				JSON.stringify({
					session: {
						id: "session/1",
						desiredState: "running",
					},
				}),
				{ status: 202, headers: { "Content-Type": "application/json" } },
			),
		);
		const client = createCloudCpClient({
			baseUrl: "https://cloud.example.test/",
			getToken: async () => "token",
			fetchImpl: fetchMock as typeof fetch,
		});

		const response = await client.restoreSession("org/1", "session/1");

		expect(response.session.desiredState).toBe("running");
		expect(fetchMock).toHaveBeenCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/restore",
			expect.objectContaining({ method: "POST" }),
		);
	});

	it("posts a startup retry for one encoded session without a body", async () => {
		const fetchMock = vi.fn(async () =>
			new Response(JSON.stringify({ session: { id: "session/1", runtimeState: "bootstrapping" } }), {
				status: 202,
				headers: { "Content-Type": "application/json" },
			}),
		);
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test/", getToken: async () => "token", fetchImpl: fetchMock as typeof fetch });

		const response = await client.retrySessionStartup("org/1", "session/1");

		expect(response.session.runtimeState).toBe("bootstrapping");
		expect(fetchMock).toHaveBeenCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/startup-retry",
			expect.objectContaining({ method: "POST" }),
		);
		const init = (fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1];
		expect(init.body).toBeUndefined();
	});

	it("surfaces an unavailable startup retry as a typed control-plane error", async () => {
		const fetchMock = vi.fn(async () =>
			new Response(JSON.stringify({ error: "conflict", code: "startup_retry_unavailable", message: "session is not retryable", requestId: "req-1" }), {
				status: 409,
				headers: { "Content-Type": "application/json" },
			}),
		);
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test/", getToken: async () => "token", fetchImpl: fetchMock as typeof fetch });

		await expect(client.retrySessionStartup("org", "session")).rejects.toMatchObject({
			name: "CloudCpError",
			status: 409,
			code: "startup_retry_unavailable",
		});
	});

	it("patches automatic CI feedback for one cloud session", async () => {
		const fetchMock = vi.fn(async () => new Response(JSON.stringify({ session: { id: "session/1", autoInjectCI: false } }), { status: 200, headers: { "Content-Type": "application/json" } }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test/", getToken: async () => "token", fetchImpl: fetchMock as typeof fetch });

		await client.setSessionAutoInjectCI("org/1", "session/1", false);

		expect(fetchMock).toHaveBeenCalledWith(
			"https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/auto-inject-ci",
			expect.objectContaining({ method: "PATCH", body: JSON.stringify({ autoInjectCI: false }) }),
		);
	});

	it.each([
		["review feedback", "setSessionAutoInjectReview", "auto-inject-review", "autoInjectReview"],
		["terminate-on-merge", "setSessionMergePolicy", "merge-policy", "terminateOnPrMerge"],
	] as const)("patches %s for one cloud session", async (_label, method, path, field) => {
		const fetchMock = vi.fn(async () => new Response(JSON.stringify({ session: { id: "session/1", [field]: false } }), { status: 200, headers: { "Content-Type": "application/json" } }));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test/", getToken: async () => "token", fetchImpl: fetchMock as typeof fetch });

		await client[method]("org/1", "session/1", false);

		expect(fetchMock).toHaveBeenCalledWith(
			`https://cloud.example.test/api/cloud/v1/orgs/org%2F1/sessions/session%2F1/${path}`,
			expect.objectContaining({ method: "PATCH", body: JSON.stringify({ [field]: false }) }),
		);
	});
});

describe("Cloud control-plane rate limits", () => {
	it("keeps how long a 429 asks the caller to wait", async () => {
		const fetchImpl = vi.fn().mockImplementation(async () => new Response(JSON.stringify({ code: "rate_limited", message: "Too many searches. Try again in a minute." }), {
			status: 429, headers: { "Content-Type": "application/json", "Retry-After": "60" },
		}));
		const client = createCloudCpClient({ baseUrl: "https://cloud.example.test", getToken: async () => "token", fetchImpl });
		const error = await client.searchChats("org", { query: "login" }).catch((caught: unknown) => caught);
		expect(error).toBeInstanceOf(CloudCpError);
		expect(error).toMatchObject({ status: 429, code: "rate_limited", retryAfterMs: 60_000, message: "Too many searches. Try again in a minute." });
	});

	it("reads Retry-After as seconds or as a date", () => {
		const now = Date.parse("2026-11-01T00:00:00Z");
		expect(retryAfterMs("60", now)).toBe(60_000);
		expect(retryAfterMs("Sun, 01 Nov 2026 00:00:30 GMT", now)).toBe(30_000);
		expect(retryAfterMs("Sat, 31 Oct 2026 00:00:00 GMT", now)).toBe(0);
		expect(retryAfterMs(null, now)).toBeUndefined();
		expect(retryAfterMs("soon", now)).toBeUndefined();
	});
});

describe("Cloud control-plane share links", () => {
	const ORG = "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b";
	const LINK = "0c927f48-7214-4fb1-833a-7c1af5b7bf80";
	const client = (response: () => Response) => {
		const fetchImpl = vi.fn().mockImplementation(async () => response());
		return { fetchImpl, client: createCloudCpClient({ baseUrl: "https://cloud.example.test", getToken: async () => "token", fetchImpl }) };
	};

	it("says how many grants removing who joined by a link changed", async () => {
		const { fetchImpl, client: cp } = client(() => new Response(JSON.stringify({ removedGrants: 3 }), { status: 200, headers: { "Content-Type": "application/json" } }));
		expect(await cp.revokeShareLink(ORG, "project", LINK, { removeJoined: true })).toEqual({ removedGrants: 3 });
		const [url, init] = fetchImpl.mock.calls[0] as [string, RequestInit];
		expect(url).toContain(`/orgs/${ORG}/projects/project/shares/${LINK}/revoke`);
		expect(init).toMatchObject({ method: "POST", body: JSON.stringify({ removeJoined: true }) });
	});

	it("takes a plain revoke's 204, or an older control plane's, with no body", async () => {
		const { fetchImpl, client: cp } = client(() => new Response(null, { status: 204 }));
		expect(await cp.revokeShareLink(ORG, "project", LINK)).toEqual({});
		expect((fetchImpl.mock.calls[0]?.[1] as RequestInit).body).toBeUndefined();
		// An older control plane ignores removeJoined and answers the same way.
		expect(await cp.revokeShareLink(ORG, "project", LINK, { removeJoined: true })).toEqual({});
	});

	it("reports a link already revoked as a 404", async () => {
		const { client: cp } = client(() => new Response(JSON.stringify({ code: "not_found", message: "Not found" }), { status: 404, headers: { "Content-Type": "application/json" } }));
		await expect(cp.revokeShareLink(ORG, "project", LINK)).rejects.toMatchObject({ status: 404 });
	});
});

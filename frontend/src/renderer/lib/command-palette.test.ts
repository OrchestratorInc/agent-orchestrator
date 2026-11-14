import { describe, expect, it } from "vitest";
import {
	buildChatSearchCommands,
	buildCommands,
	buildFileSessionCommands,
	bestMatch,
	buildProjectActions,
	buildSessionActions,
	findSubmenuEntries,
	goToCommands,
	menuEntryCommands,
	buildWorkspaceFileCommands,
	filterCommands,
	matchScore,
	groupCommands,
	displayGroups,
	titleHighlights,
	findSession,
	visibleForQuery,
	MAX_ATTENTION_SEARCH_RESULTS,
	MAX_SEARCH_RESULTS,
	type CommandItem,
} from "./command-palette";
import type { PRReviewState } from "./session-reviews";
import {
	CLOUD_PROJECT_KIND,
	STANDALONE_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
	type PullRequestFacts,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";
import { appI18n } from "../i18n";
import { sessionBranchPresentation } from "./session-branch";
import { buildSettingsCommands, buildShortcutCommands } from "./palette-registry";

function session(overrides: Partial<WorkspaceSession> & { id: string }): WorkspaceSession {
	return {
		workspaceId: "proj-1",
		workspaceName: "app",
		title: overrides.id,
		provider: "codex",
		kind: "worker",
		branch: `feature/${overrides.id}`,
		status: "working",
		updatedAt: "2026-06-10T00:00:00Z",
		prs: [],
		...overrides,
	};
}

const pr = (number: number, url = `https://github.com/o/r/pull/${number}`): PullRequestFacts => ({
	url,
	number,
	state: "open",
	ci: "passing",
	review: "pending",
	mergeability: "clean",
	reviewComments: false,
	updatedAt: "2026-06-10T00:00:00Z",
});

function workspaces(): WorkspaceSummary[] {
	return [
		{
			id: "proj-1",
			name: "app",
			path: "/repos/app",
			type: "main",
			sessions: [
				session({ id: "w-working", title: "refactor loader", status: "working" }),
				session({ id: "w-merge", title: "ship banner", status: "mergeable" }),
				session({ id: "w-action", title: "fix flake", status: "needs_input" }),
				session({ id: "w-pr", title: "add cache", status: "pr_open", prs: [pr(42)] }),
				session({ id: "w-synthetic", title: "scratch", branch: "session/w-synthetic" }),
				session({ id: "orch", title: "orchestrate", kind: "orchestrator" }),
			],
		},
	];
}

const byId = (items: CommandItem[]) => new Map(items.map((item) => [item.id, item]));

describe("session import commands", () => {
	it("offers folder import by search, found by agent or session history", () => {
		const items = buildCommands({ workspaces: workspaces() });
		const command = byId(items).get("global-import-sessions");
		expect(command?.action).toEqual({ kind: "open-session-import" });
		// A project's Import chats leads the blank palette; one import row is enough there.
		expect(filterCommands(items, "")).not.toContain(command);
		for (const query of ["import", "resume claude code", "import codex sessions", "existing terminal chats"]) {
			expect(filterCommands(items, query)).toContain(command);
		}
	});

	it("offers import for the current local project", () => {
		const items = buildCommands({ workspaces: workspaces(), currentProjectId: "proj-1" });
		const command = byId(items).get("current-import-sessions");
		expect(command).toMatchObject({ group: "current", action: { kind: "open-session-import", projectId: "proj-1" } });
		expect(filterCommands(items, "import app")).toContain(command);
	});

	it.each([
		["Scratch", { id: STANDALONE_WORKSPACE_ID, kind: STANDALONE_PROJECT_KIND }],
		["Cloud", { kind: CLOUD_PROJECT_KIND }],
		["shared", { shared: {} }],
		["connected host", { hostId: "host-a" }],
	] satisfies [string, Partial<WorkspaceSummary>][])("omits project import for %s", (_name, overrides) => {
		const workspace = { ...workspaces()[0]!, ...overrides };
		const items = byId(buildCommands({ workspaces: [workspace], currentProjectId: workspace.id, currentHostId: workspace.hostId }));
		expect(items.has("current-import-sessions")).toBe(false);
		expect(items.get("global-import-sessions")?.action).toEqual({ kind: "open-session-import" });
	});
});

describe("findSession", () => {
	it("returns the workspace and session together", () => {
		const result = findSession(workspaces(), "w-pr");

		expect(result?.workspace.id).toBe("proj-1");
		expect(result?.session.id).toBe("w-pr");
		expect(findSession(workspaces(), "missing")).toBeUndefined();
	});
});

describe("workspace file commands", () => {
	it("offers file search for a current worker session and excludes orchestrators", () => {
		const items = buildCommands({ workspaces: workspaces(), currentProjectId: "proj-1", currentSessionId: "w-working" });
		expect(byId(items).get("current-search-files")?.action).toEqual({
			kind: "open-file-search",
			projectId: "proj-1",
			target: { projectId: "proj-1", sessionId: "w-working" },
		});

		const picker = buildFileSessionCommands(workspaces()[0]!);
		expect(picker.some((item) => item.title === "orchestrate")).toBe(false);
	});

	it("sorts active worktrees before terminated ones and omits unready worktrees", () => {
		const workspace: WorkspaceSummary = {
			...workspaces()[0]!,
			sessions: [
				session({ id: "done", status: "terminated" }),
				session({ id: "starting", provisionState: "provisioning" }),
				session({ id: "live", status: "working" }),
			],
		};
		expect(buildFileSessionCommands(workspace).map((item) => item.title)).toEqual(["live", "done"]);
	});

	it("uses basenames as titles, full paths as keywords, and ranks basename matches first", () => {
		const target = { projectId: "proj-1", sessionId: "w-working" };
		const items = buildWorkspaceFileCommands(target, [
			{ path: "docs/app/guide.md" },
			{ path: "src/App.tsx" },
		]);
		expect(items[1]).toMatchObject({
			id: "file:local:w-working:src/App.tsx",
			title: "App.tsx",
			subtitle: "src",
			keywords: expect.arrayContaining(["src/App.tsx"]),
		});
		expect(filterCommands(items, "app").map((item) => item.title)).toEqual(["App.tsx", "guide.md"]);
	});
});

describe("buildCommands grouping", () => {
	it("uses the translator supplied by the reactive caller", () => {
		const items = buildCommands(
			{ workspaces: workspaces(), currentProjectId: "proj-1" },
			appI18n.getFixedT("zh-CN"),
		);
		expect(byId(items).get("current-new-task")?.title).toBe("新建任务");
	});

	it("puts current-scoped actions in the Current group when the project is valid", () => {
		const items = buildCommands({ workspaces: workspaces(), currentProjectId: "proj-1", currentSessionId: "w-pr" });
		const map = byId(items);
		expect(map.get("current-new-task")?.group).toBe("current");
		expect(map.get("current-open-orchestrator")?.group).toBe("current");
		expect(map.get("current-project-settings")?.group).toBe("current");
		expect(map.get("current-copy-branch")?.group).toBe("session");
		expect(map.get("current-copy-branch")?.action).toEqual({ kind: "copy-branch", branch: "feature/w-pr" });
	});

	it("offers Open usage only in Developer Mode", () => {
		expect(byId(buildCommands({ workspaces: workspaces() })).has("global-usage")).toBe(false);
		expect(byId(buildCommands({ workspaces: workspaces(), developerMode: true })).has("global-usage")).toBe(true);
	});

	it("offers Archived sessions for the current project only when it has archived sessions", () => {
		expect(byId(buildCommands({ workspaces: workspaces(), currentProjectId: "proj-1" })).has("current-open-archive")).toBe(false);

		const withArchive = workspaces();
		withArchive[0]!.sessions.push(
			session({ id: "w-ended", status: "terminated" }),
			session({ id: "w-merged-ended", status: "merged", isTerminated: true }),
			session({ id: "orch-ended", kind: "orchestrator", status: "terminated" }),
		);
		const archive = byId(buildCommands({ workspaces: withArchive, currentProjectId: "proj-1" })).get("current-open-archive");
		expect(archive?.group).toBe("current");
		expect(archive?.title).toBe("Archived sessions");
		expect(archive?.subtitle).toBe("2 archived sessions");
		expect(archive?.action).toEqual({ kind: "open-archive", projectId: "proj-1" });
	});

	it("runs New task off a project through ⌘N, which picks where it starts", () => {
		const items = buildCommands({ workspaces: workspaces(), currentProjectId: "missing", currentSessionId: undefined });
		const newTask = byId(items).get("current-new-task");
		expect(newTask?.disabled).toBe(false);
		expect(newTask?.searchOnly).toBeUndefined();
		expect(newTask?.action).toEqual({ kind: "run-shortcut", shortcut: "new-session" });
		expect(byId(items).has("current-open-orchestrator")).toBe(false);
		expect(byId(items).has("current-project-settings")).toBe(false);
	});

	it("disables New task and Open orchestrator while the project orchestrator is restarting", () => {
		const items = buildCommands({
			workspaces: workspaces(),
			currentProjectId: "proj-1",
			restartingProjectIds: new Set(["proj-1"]),
		});
		const map = byId(items);
		expect(map.get("current-new-task")?.disabled).toBe(true);
		expect(map.get("current-new-task")?.disabledReason).toBe("The orchestrator agent is restarting.");
		expect(map.get("current-open-orchestrator")?.disabled).toBe(true);
		expect(map.get("current-open-orchestrator")?.disabledReason).toBe("The orchestrator agent is restarting.");
		expect(map.get("current-project-settings")?.disabled).toBeFalsy();
	});

	it("omits Copy branch for a synthetic (session/<id>) branch and for orchestrators", () => {
		const synthetic = buildCommands({ workspaces: workspaces(), currentSessionId: "w-synthetic" });
		expect(byId(synthetic).has("current-copy-branch")).toBe(false);
		const orch = buildCommands({ workspaces: workspaces(), currentSessionId: "orch" });
		expect(byId(orch).has("current-copy-branch")).toBe(false);
	});

	it("recognises an orchestrator by its id suffix, not just its kind", () => {
		const rows = workspaces();
		rows[0].sessions.push(session({ id: "proj-1-orchestrator", title: "legacy orch", branch: "main" }));
		const items = buildCommands({ workspaces: rows, currentSessionId: "proj-1-orchestrator" });
		expect(byId(items).has("current-copy-branch")).toBe(false);
	});
});

describe("buildCommands global", () => {
	it("offers a Go to home command that navigates to the root route", () => {
		const home = buildCommands({ workspaces: [] }).find((item) => item.id === "global-home");
		expect(home).toMatchObject({ group: "global", action: { kind: "navigate", target: { to: "/" } } });
	});
});

describe("buildCommands attention", () => {
	it("lists what the sidebar flags: needs-you first, then review turns, not ready-to-merge", () => {
		const items = buildCommands({ workspaces: workspaces() });
		const ids = items.filter((item) => item.group === "attention").map((item) => item.id);
		expect(ids).toContain("attention:w-action");
		expect(ids).not.toContain("attention:w-merge");
		expect(ids).not.toContain("attention:w-working");
		const flagged = ["attention:w-action", "attention:w-pr"].filter((id) => ids.includes(id));
		expect(ids.slice(0, flagged.length)).toEqual(flagged);
	});

	it("puts the most recently active first within the same flag", () => {
		const rows = workspaces();
		rows[0].sessions.push(session({ id: "w-newer", status: "needs_input", lastInteractionAt: "2026-06-12T00:00:00Z" }));
		const ids = buildCommands({ workspaces: rows }).filter((item) => item.group === "attention").map((item) => item.id);
		expect(ids.indexOf("attention:w-newer")).toBeLessThan(ids.indexOf("attention:w-action"));
	});

	it("omits the current session from Needs attention (already in view)", () => {
		const items = buildCommands({ workspaces: workspaces(), currentSessionId: "w-action" });
		const ids = new Set(items.map((item) => item.id));
		expect(ids.has("attention:w-action")).toBe(false);
		expect(ids.has("session:w-action")).toBe(false);
	});

	it("leaves archived sessions out of Needs attention", () => {
		const rows = workspaces();
		rows[0].sessions.push(
			session({ id: "w-archived-action", status: "needs_input", isTerminated: true }),
			session({ id: "w-archived-merge", status: "merged", isTerminated: true }),
		);
		const map = byId(buildCommands({ workspaces: rows }));
		expect(map.has("attention:w-archived-action")).toBe(false);
		expect(map.has("attention:w-archived-merge")).toBe(false);
		expect(map.get("session:w-archived-action")?.searchOnly).toBe(true);
	});
});

describe("buildCommands sessions", () => {
	it("does not repeat attention or current sessions in the flat Sessions list", () => {
		const items = buildCommands({ workspaces: workspaces(), currentSessionId: "w-working" });
		const ids = new Set(items.map((item) => item.id));
		expect(ids.has("attention:w-action")).toBe(true);
		expect(ids.has("session:w-action")).toBe(false);
		expect(ids.has("session:w-working")).toBe(false);
		expect(ids.has("session:w-synthetic")).toBe(true);
	});
});

describe("buildCommands pull requests", () => {
	it("creates a per-PR item searchable by number, #number, url, branch and project", () => {
		const items = buildCommands({ workspaces: workspaces() });
		const prItem = byId(items).get("pr:w-pr:42");
		expect(prItem?.group).toBe("prs");
		expect(prItem?.title).toBe("#42");
		const keywords = prItem?.keywords ?? [];
		expect(keywords).toContain("#42");
		expect(keywords).toContain("42");
		expect(keywords).toContain("https://github.com/o/r/pull/42");
		expect(keywords).toContain("feature/w-pr");
		expect(keywords).toContain("app");
	});

	it("excludes merged and closed PRs (open/draft only)", () => {
		const merged: PullRequestFacts = { ...pr(7), state: "merged" };
		const closed: PullRequestFacts = { ...pr(8), state: "closed" };
		const ws: WorkspaceSummary[] = [
			{
				id: "proj-1",
				name: "app",
				path: "/repos/app",
				type: "main",
				sessions: [session({ id: "w-mix", title: "mixed prs", prs: [merged, closed, pr(9)] })],
			},
		];
		const ids = new Set(buildCommands({ workspaces: ws }).map((item) => item.id));
		expect(ids.has("pr:w-mix:9")).toBe(true);
		expect(ids.has("pr:w-mix:7")).toBe(false);
		expect(ids.has("pr:w-mix:8")).toBe(false);
	});
});

type ReviewRun = NonNullable<PRReviewState["latestRun"]>;

const reviewRun = (prNumber: number): ReviewRun => ({
	autoInjectReview: false,
	batchId: "batch-1",
	triggerSource: "manual" as const,
	body: "review body",
	createdAt: "2026-06-10T00:00:00Z",
	githubReviewId: `${prNumber}01`,
	harness: "codex",
	id: `run-${prNumber}`,
	prUrl: `https://github.com/o/r/pull/${prNumber}`,
	reviewId: `review-${prNumber}`,
	sessionId: "w-pr",
	status: "delivered",
	targetSha: "sha",
	verdict: "approved",
});

const reviewState = (
	prNumber: number,
	status: PRReviewState["status"],
	overrides: Partial<PRReviewState> = {},
): PRReviewState => ({
	prNumber,
	prUrl: `https://github.com/o/r/pull/${prNumber}`,
	status,
	targetSha: "sha",
	title: `PR ${prNumber}`,
	...overrides,
});

describe("buildCommands PR actions", () => {
	it("creates Open PR and Copy PR URL items per open PR", () => {
		const items = buildCommands({ workspaces: workspaces() });
		const map = byId(items);
		const open = map.get("pr-open:w-pr:42");
		expect(open?.group).toBe("prs");
		expect(open?.title).toBe("Open PR #42");
		expect(open?.action).toEqual({ kind: "open-pr", url: "https://github.com/o/r/pull/42" });
		expect(open?.searchOnly).toBe(true);
		const copy = map.get("pr-copy:w-pr:42");
		expect(copy?.title).toBe("Copy PR URL #42");
		expect(copy?.action).toEqual({ kind: "copy-pr-url", url: "https://github.com/o/r/pull/42" });
		expect(copy?.searchOnly).toBe(true);
	});

	it("creates an enabled Run review item per open PR when review data has not loaded", () => {
		const review = byId(buildCommands({ workspaces: workspaces() })).get("pr-review:w-pr:42");
		expect(review?.group).toBe("prs");
		expect(review?.title).toBe("Run review #42");
		expect(review?.action).toEqual({ kind: "trigger-review", sessionId: "w-pr" });
		expect(review?.disabled).toBeFalsy();
		expect(review?.disabledReason).toBeUndefined();
		expect(review?.searchOnly).toBe(true);
	});

	it("omits the review item while a session's review data has not yet loaded, but keeps Open PR / Copy PR URL", () => {
		const map = byId(
			buildCommands({
				workspaces: workspaces(),
				reviewStatesBySessionId: {},
			}),
		);
		expect(map.has("pr-review:w-pr:42")).toBe(false);
		expect(map.get("pr-open:w-pr:42")).toBeTruthy();
		expect(map.get("pr-copy:w-pr:42")).toBeTruthy();
	});

	it("labels the review item Re-run review for changes requested or a completed run", () => {
		const changesRequested = buildCommands({
			workspaces: workspaces(),
			reviewStatesBySessionId: { "w-pr": [reviewState(42, "changes_requested")] },
		});
		expect(byId(changesRequested).get("pr-review:w-pr:42")?.title).toBe("Re-run review #42");

		const upToDate = buildCommands({
			workspaces: workspaces(),
			reviewStatesBySessionId: { "w-pr": [reviewState(42, "up_to_date", { latestRun: reviewRun(42) })] },
		});
		const item = byId(upToDate).get("pr-review:w-pr:42");
		expect(item?.title).toBe("Re-run review #42");
		expect(item?.disabled).toBeFalsy();
	});

	it("disables the review item with Review already running when a session review is running", () => {
		const items = buildCommands({
			workspaces: workspaces(),
			reviewStatesBySessionId: { "w-pr": [reviewState(42, "running")] },
		});
		const review = byId(items).get("pr-review:w-pr:42");
		expect(review?.disabled).toBe(true);
		expect(review?.disabledReason).toBe("Review already running");
	});

	it("disables the review item with Not eligible for review for ineligible or draft PRs", () => {
		const ineligible = buildCommands({
			workspaces: workspaces(),
			reviewStatesBySessionId: { "w-pr": [reviewState(42, "ineligible")] },
		});
		const ineligibleItem = byId(ineligible).get("pr-review:w-pr:42");
		expect(ineligibleItem?.disabled).toBe(true);
		expect(ineligibleItem?.disabledReason).toBe("Not eligible for review");

		const draftWorkspaces: WorkspaceSummary[] = [
			{
				id: "proj-1",
				name: "app",
				path: "/repos/app",
				type: "main",
				sessions: [session({ id: "w-draft", title: "draft work", prs: [{ ...pr(5), state: "draft" }] })],
			},
		];
		const draft = buildCommands({
			workspaces: draftWorkspaces,
			reviewStatesBySessionId: { "w-draft": [reviewState(5, "ineligible")] },
		});
		const draftItem = byId(draft).get("pr-review:w-draft:5");
		expect(draftItem?.disabled).toBe(true);
		expect(draftItem?.disabledReason).toBe("Not eligible for review");
	});

	it("emits per-PR action items for every open PR of a multi-PR session, all triggering the session", () => {
		const multiWorkspaces: WorkspaceSummary[] = [
			{
				id: "proj-1",
				name: "app",
				path: "/repos/app",
				type: "main",
				sessions: [session({ id: "w-multi", title: "stacked", prs: [pr(1), pr(2)] })],
			},
		];
		const map = byId(buildCommands({ workspaces: multiWorkspaces }));
		expect(map.get("pr-open:w-multi:1")?.action).toEqual({ kind: "open-pr", url: "https://github.com/o/r/pull/1" });
		expect(map.get("pr-open:w-multi:2")?.action).toEqual({ kind: "open-pr", url: "https://github.com/o/r/pull/2" });
		expect(map.get("pr-review:w-multi:1")?.action).toEqual({ kind: "trigger-review", sessionId: "w-multi" });
		expect(map.get("pr-review:w-multi:2")?.action).toEqual({ kind: "trigger-review", sessionId: "w-multi" });
	});

	it("does not create action items for merged or closed PRs", () => {
		const merged: PullRequestFacts = { ...pr(7), state: "merged" };
		const closed: PullRequestFacts = { ...pr(8), state: "closed" };
		const ws: WorkspaceSummary[] = [
			{
				id: "proj-1",
				name: "app",
				path: "/repos/app",
				type: "main",
				sessions: [session({ id: "w-mix", title: "mixed prs", prs: [merged, closed, pr(9)] })],
			},
		];
		const ids = new Set(buildCommands({ workspaces: ws }).map((item) => item.id));
		expect(ids.has("pr-open:w-mix:9")).toBe(true);
		expect(ids.has("pr-copy:w-mix:9")).toBe(true);
		expect(ids.has("pr-review:w-mix:9")).toBe(true);
		expect(ids.has("pr-open:w-mix:7")).toBe(false);
		expect(ids.has("pr-copy:w-mix:8")).toBe(false);
		expect(ids.has("pr-review:w-mix:7")).toBe(false);
	});

	it("surfaces the action items for open pr / copy pr / review queries but hides them untyped", () => {
		const items = buildCommands({ workspaces: workspaces() });
		expect(filterCommands(items, "open pr").some((item) => item.id === "pr-open:w-pr:42")).toBe(true);
		expect(filterCommands(items, "copy pr").some((item) => item.id === "pr-copy:w-pr:42")).toBe(true);
		expect(filterCommands(items, "review").some((item) => item.id === "pr-review:w-pr:42")).toBe(true);
		expect(filterCommands(items, "").some((item) => item.id === "pr-open:w-pr:42")).toBe(false);
		expect(filterCommands(items, "").some((item) => item.id === "pr-review:w-pr:42")).toBe(false);
	});

	it("discovers Copy branch name via git and copy keywords", () => {
		const items = buildCommands({ workspaces: workspaces(), currentProjectId: "proj-1", currentSessionId: "w-pr" });
		const keywords = byId(items).get("current-copy-branch")?.keywords ?? [];
		expect(keywords).toContain("copy");
		expect(keywords).toContain("git");
	});
});

describe("buildCommands finished sessions", () => {
	function withFinished(): WorkspaceSummary[] {
		return [
			{
				id: "proj-1",
				name: "app",
				path: "/repos/app",
				type: "main",
				sessions: [
					session({ id: "w-live", title: "live one", status: "working" }),
					session({ id: "w-done", title: "archived cleanup", status: "terminated" }),
					session({ id: "w-merged", title: "old merge", status: "merged" }),
				],
			},
		];
	}

	it("indexes merged/terminated sessions as search-only (hidden until typed, then findable)", () => {
		const items = buildCommands({ workspaces: withFinished() });
		const done = byId(items).get("session:w-done");
		expect(done?.searchOnly).toBe(true);
		expect(byId(items).get("session:w-live")?.searchOnly).toBeFalsy();

		const suggested = filterCommands(items, "");
		expect(suggested.some((item) => item.id === "session:w-done")).toBe(false);
		expect(suggested.some((item) => item.id === "session:w-live")).toBe(true);

		const searched = filterCommands(items, "archived");
		expect(searched.some((item) => item.id === "session:w-done")).toBe(true);
	});
});

describe("result caps", () => {
	const manyProjects = (n: number): WorkspaceSummary[] =>
		Array.from({ length: n }, (_, i) => ({
			id: `p${i}`,
			name: `project-${i}`,
			path: `/repos/p${i}`,
			type: "main" as const,
			sessions: [],
		}));

	it("caps the pre-typing suggestion view per group so huge installs never render every row", () => {
		const grouped = displayGroups(buildCommands({ workspaces: manyProjects(50) }), "");
		// Projects wait for a search; the sidebar already lists them.
		expect(grouped.some((g) => g.id === "projects")).toBe(false);
		expect(filterCommands(buildCommands({ workspaces: manyProjects(50) }), "project-7").some((item) => item.id === "project:p7")).toBe(true);
		expect(grouped.find((g) => g.id === "global")?.items.length).toBeGreaterThan(0);
	});

	it("renders a typed search under category headings, capped to MAX_SEARCH_RESULTS overall", () => {
		const groups = displayGroups(buildCommands({ workspaces: manyProjects(50) }), "project");
		expect(groups.every((g) => g.id !== "results")).toBe(true);
		expect(groups.some((g) => g.id === "projects")).toBe(true);
		const total = groups.reduce((sum, g) => sum + g.items.length, 0);
		expect(total).toBe(MAX_SEARCH_RESULTS);
	});

	it("keeps the combined attention + results count within MAX_SEARCH_RESULTS", () => {
		const attentionSessions = Array.from({ length: 25 }, (_, i) =>
			session({ id: `deploy-hot-${i}`, title: `deploy hot ${i}`, status: "needs_input" }),
		);
		const workspaces: WorkspaceSummary[] = [
			{ id: "deploy", name: "deploy proj", path: "/repos/deploy", type: "main", sessions: attentionSessions },
		];
		const groups = displayGroups(buildCommands({ workspaces }), "deploy");
		const total = groups.reduce((n, g) => n + g.items.length, 0);
		expect(total).toBeLessThanOrEqual(MAX_SEARCH_RESULTS);
	});

	it("keeps search hits under category headings, sessions before projects, the best match picked", () => {
		const workspaces: WorkspaceSummary[] = [
			{
				id: "alpha",
				name: "alpha",
				path: "/repos/alpha",
				type: "main",
				sessions: [session({ id: "s-attn", title: "fix alpha bug", status: "needs_input" })],
			},
		];
		const groups = displayGroups(buildCommands({ workspaces }), "alpha");
		const ids = groups.map((g) => g.id);
		expect(ids).toContain("attention");
		expect(ids).toContain("projects");
		// A search lists sessions before projects; Enter still takes the best
		// match, the exact project title.
		expect(ids.indexOf("attention")).toBeLessThan(ids.indexOf("projects"));
		expect(bestMatch(groups.flatMap((g) => g.items), "alpha")?.id).toBe("project:alpha");
		expect(groups.find((g) => g.id === "attention")?.items.some((item) => item.id === "attention:s-attn")).toBe(
			true,
		);
		const rendered = groups.flatMap((g) => g.items);
		expect(rendered.length).toBeLessThanOrEqual(MAX_SEARCH_RESULTS);
	});

	it("floats attention matches into their own category during search", () => {
		const workspaces: WorkspaceSummary[] = [
			{
				id: "alpha",
				name: "alpha",
				path: "/repos/alpha",
				type: "main",
				sessions: [session({ id: "s-attn", title: "fix alpha bug", status: "needs_input" })],
			},
		];
		const groups = displayGroups(buildCommands({ workspaces }), "alpha");
		expect(groups.map((g) => g.id)).toContain("attention");
		expect(groups.find((g) => g.id === "attention")?.items.map((item) => item.id)).toContain("attention:s-attn");
	});

	it("lists general commands first, then sessions, then projects and PRs", () => {
		// "app" is the project title and a weaker hit on General (Appearance) and the session rows.
		const ids = displayGroups(buildCommands({ workspaces: workspaces() }), "app").map((g) => g.id);
		expect(ids[0]).toBe("global");
		expect(ids.indexOf("attention")).toBeLessThan(ids.indexOf("sessions"));
		expect(ids.indexOf("sessions")).toBeLessThan(ids.indexOf("projects"));
		expect(ids.indexOf("projects")).toBeLessThan(ids.indexOf("prs"));
	});

	it("keeps a lower-scoring attention match visible past the ordinary-result cap", () => {
		const manyPrs = Array.from({ length: 30 }, (_, i) => pr(i + 1));
		const workspaces: WorkspaceSummary[] = [
			{
				id: "proj-deploy",
				name: "proj-deploy",
				path: "/repos/deploy",
				type: "main",
				sessions: [
					session({ id: "hot", title: "zzz deploy", status: "needs_input" }),
					session({ id: "pr-host", title: "deploy stack", status: "pr_open", prs: manyPrs }),
				],
			},
		];
		const groups = displayGroups(buildCommands({ workspaces }), "deploy");
		const attention = groups.find((g) => g.id === "attention");
		expect(attention?.items.map((item) => item.id)).toContain("attention:hot");
	});

	it("never lets a flood of attention matches crowd out an exact non-attention match", () => {
		const floodedWorkspace: WorkspaceSummary = {
			id: "proj-deploy",
			name: "proj-deploy",
			path: "/repos/deploy",
			type: "main",
			sessions: Array.from({ length: MAX_SEARCH_RESULTS }, (_, i) =>
				session({ id: `hot-${i}`, title: `zzz deploy ${i}`, status: "needs_input" }),
			),
		};
		const exactMatchWorkspace: WorkspaceSummary = {
			id: "deploy",
			name: "deploy",
			path: "/repos/deploy-exact",
			type: "main",
			sessions: [],
		};
		const items = buildCommands({ workspaces: [floodedWorkspace, exactMatchWorkspace] });
		const attentionMatches = items.filter(
			(item) => item.group === "attention" && item.title.toLowerCase().includes("deploy"),
		);
		expect(attentionMatches.length).toBeGreaterThanOrEqual(MAX_SEARCH_RESULTS);

		const visible = visibleForQuery(items, "deploy");
		expect(visible.slice(0, MAX_ATTENTION_SEARCH_RESULTS).every(isAttentionInZone)).toBe(true);
		expect(visible).toContainEqual(expect.objectContaining({ id: "project:deploy" }));
		expect(visible.findIndex((item) => item.id === "project:deploy")).toBeLessThan(MAX_SEARCH_RESULTS);
	});

	it("still surfaces the exact match when every attention title also prefix-matches (tied score)", () => {
		const floodedWorkspace: WorkspaceSummary = {
			id: "proj-1",
			name: "proj-1",
			path: "/repos/proj-1",
			type: "main",
			sessions: Array.from({ length: MAX_SEARCH_RESULTS }, (_, i) =>
				session({ id: `hot-${i}`, title: `deploy pipeline ${i}`, status: "needs_input" }),
			),
		};
		const exactMatchWorkspace: WorkspaceSummary = {
			id: "deploy",
			name: "deploy",
			path: "/repos/deploy-exact",
			type: "main",
			sessions: [],
		};
		const items = buildCommands({ workspaces: [floodedWorkspace, exactMatchWorkspace] });
		const visible = visibleForQuery(items, "deploy");
		expect(visible).toContainEqual(expect.objectContaining({ id: "project:deploy" }));
		expect(visible.filter((item) => !isAttentionInZone(item))).not.toHaveLength(0);
	});
});

function isAttentionInZone(item: CommandItem): boolean {
	return item.zone === "action" || item.zone === "merge";
}

describe("filterCommands / matchScore", () => {
	it("ranks a title prefix above a keyword-only hit", () => {
		const items = buildCommands({ workspaces: workspaces() });
		const results = filterCommands(items, "app");
		expect(results[0]?.id).toBe("project:proj-1");
	});

	it("matches a PR by its #number", () => {
		const items = buildCommands({ workspaces: workspaces() });
		const results = filterCommands(items, "#42");
		expect(results.some((item) => item.id === "pr:w-pr:42")).toBe(true);
	});
});

describe("groupCommands", () => {
	it("skips empty groups and preserves group order", () => {
		const items = buildCommands({ workspaces: workspaces(), currentProjectId: "proj-1" });
		const grouped = groupCommands(items);
		const order = grouped.map((g) => g.id);
		// Sessions above Projects, as in the sidebar.
		expect(order).toEqual(["current", "attention", "sessions", "projects", "prs", "global"]);
		expect(grouped.every((g) => g.items.length > 0)).toBe(true);
	});
});

describe("session rows jump to the session", () => {
	it("navigates on Enter and keeps the actions panel as the Cmd+Enter alternative", () => {
		const items = buildCommands({ workspaces: workspaces() });
		const map = byId(items);
		expect(map.get("attention:w-action")?.action).toEqual({
			kind: "navigate",
			target: { to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "proj-1", sessionId: "w-action" } },
		});
		expect(map.get("attention:w-action")?.secondaryAction).toEqual({ kind: "open-session-actions", sessionId: "w-action" });
		expect(map.get("pr:w-pr:42")?.action?.kind).toBe("navigate");
		expect(map.get("project:proj-1")?.action?.kind).toBe("navigate");
	});
});

const workspace: WorkspaceSummary = { id: "proj-1", name: "app", path: "/repos/app", type: "main", sessions: [] };
const actionKinds = (items: CommandItem[]) => items.map((item) => item.action?.kind ?? "none");

describe("buildSessionActions", () => {
	it("offers Jump then Copy branch for a live worker", () => {
		const items = buildSessionActions(workspace, session({ id: "live", status: "working" }));
		expect(items.map((i) => i.title)).toEqual(["Jump to session", "Copy branch name"]);
		expect(items[0].action).toEqual({
			kind: "navigate",
			target: { to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "proj-1", sessionId: "live" } },
		});
	});

	it("routes a standalone session through the projectless session route", () => {
		const standaloneWorkspace: WorkspaceSummary = {
			id: STANDALONE_WORKSPACE_ID,
			name: "Standalone agents",
			kind: STANDALONE_PROJECT_KIND,
			path: "",
			sessions: [],
		};
		const items = buildSessionActions(
			standaloneWorkspace,
			session({ id: "standalone-1", workspaceId: "", workspaceName: "Standalone agents", branch: undefined }),
		);
		expect(items[0].action).toEqual({
			kind: "navigate",
			target: { to: "/sessions/$sessionId", params: { sessionId: "standalone-1" } },
		});
	});

	it("adds Resume only for a terminated worker", () => {
		const terminated = buildSessionActions(workspace, session({ id: "gone", status: "terminated" }));
		expect(terminated.find((i) => i.action?.kind === "resume-session")?.action).toEqual({
			kind: "resume-session",
			projectId: "proj-1",
			sessionId: "gone",
		});
		for (const status of ["working", "needs_input", "no_signal", "mergeable"] as const) {
			const items = buildSessionActions(workspace, session({ id: `s-${status}`, status }));
			expect(items.some((i) => i.action?.kind === "resume-session")).toBe(false);
		}
	});

	it("offers Resume for a durably terminated session whose derived status is not 'terminated'", () => {
		const items = buildSessionActions(
			workspace,
			session({ id: "archived-merged", status: "merged", isTerminated: true }),
		);
		expect(items.some((i) => i.action?.kind === "resume-session")).toBe(true);
	});

	it("never offers Resume or Copy branch for an orchestrator", () => {
		const items = buildSessionActions(
			workspace,
			session({ id: "proj-1-orchestrator", kind: "orchestrator", status: "terminated", branch: "main" }),
		);
		expect(actionKinds(items)).toEqual(["navigate"]);
	});

	it("omits Copy branch for a synthetic branch", () => {
		const items = buildSessionActions(workspace, session({ id: "syn", branch: "session/syn" }));
		expect(items.some((i) => i.action?.kind === "copy-branch")).toBe(false);
	});
});


describe("pending branch presentation", () => {
	const pending = session({ id: "delegated", provisionState: "ready", provisionSteps: [{ id: "worktree", status: "running" }] });
	it("keeps synchronous provisional names hidden while naming", () => {
		expect(sessionBranchPresentation(pending)).toEqual({ branch: undefined, naming: true });
	});
	it.each([{ terminalHandleId: "terminal" }, { chatProviderPreserved: true }])("reveals a launched worker's branch despite stale progress: %j", (launched) => {
		expect(sessionBranchPresentation({ ...pending, ...launched })).toEqual({ branch: pending.branch, naming: false });
	});
	it.each<Partial<WorkspaceSession>>([{ activity: { state: "exited", lastActivityAt: "2026-01-01T00:00:00Z" } }, { statusReadiness: "unavailable" }, { isTerminated: true }, { provisionState: "failed" }])("does not shimmer forever on a stopped or failed worker: %j", (stopped) => {
		expect(sessionBranchPresentation({ ...pending, ...stopped })).toEqual({ branch: undefined, naming: false });
	});
	it("hides a task's branch while its generated name is on the way", () => {
		const launched = session({ id: "mer-7", branch: "ao/mer-7/root", chatProviderPreserved: true, branchNaming: true });
		expect(sessionBranchPresentation(launched)).toEqual({ branch: undefined, naming: true });
		expect(sessionBranchPresentation({ ...launched, branch: "ao/login-redirect/root", branchNaming: false })).toEqual({ branch: "ao/login-redirect/root", naming: false });
		expect(sessionBranchPresentation({ ...launched, isTerminated: true })).toEqual({ branch: "ao/mer-7/root", naming: false });
		// A start that failed after launch has its worktree.
		expect(sessionBranchPresentation({ ...launched, provisionState: "failed", provisionSteps: [{ id: "worktree", status: "done" }] })).toEqual({ branch: "ao/mer-7/root", naming: false });
	});
});

describe("Report a problem", () => {
	it.each(["bug", "report"])("is found by typing %s", (query) => {
		const ids = filterCommands(buildCommands({ workspaces: workspaces() }), query).map((item) => item.id);
		expect(ids).toContain("global-report-problem");
	});

	const paletteRows = () => [
		...buildCommands({ workspaces: workspaces() }),
		...buildShortcutCommands(appI18n.t),
		...buildSettingsCommands(appI18n.t, { developerMode: false, diagnostics: false }),
	];

	it.each(["bug", "report", "report a problem", "issue", "feedback"])("is the Enter target for %s", (query) => {
		expect(displayGroups(paletteRows(), query)[0]?.items[0]?.id).toBe("global-report-problem");
	});

	it("has one row, so Enter and a click can't do different things", () => {
		const title = appI18n.t("report.action");
		expect(paletteRows().filter((item) => item.title === title).map((item) => item.id)).toEqual(["global-report-problem"]);
	});
});

describe("whole keyword matches", () => {
	const row = (id: string, keywords: string[]): CommandItem => ({ id, group: "global", title: "Unrelated", keywords, action: { kind: "toggle-theme" } });

	it("never match the same letters inside a longer word", () => {
		const whole = row("whole", ["bug"]);
		const inside = row("inside", ["debug"]);
		expect(matchScore("bug", inside)).toBe(0);
		expect(filterCommands([inside, whole], "bug").map((item) => item.id)).toEqual(["whole"]);
	});

	it("match the start of a keyword word while typing", () => {
		expect(matchScore("dar", row("theme", ["dark mode"]))).toBeGreaterThan(0);
		expect(matchScore("ode", row("theme", ["dark mode"]))).toBe(0);
	});

	it("still rank below a title match", () => {
		const titled: CommandItem = { ...row("titled", []), title: "Fix the bug" };
		expect(matchScore("bug", titled)).toBeGreaterThan(matchScore("bug", row("whole", ["bug"])));
	});

	it("send Enter on keys to Show keyboard shortcuts", () => {
		const rows = [...buildShortcutCommands(appI18n.t), ...buildSettingsCommands(appI18n.t, { developerMode: false, diagnostics: false })];
		expect(displayGroups(rows, "keys")[0]?.items[0]?.id).toBe("shortcut:keyboard-shortcuts");
	});

	it.each(["bug", "review", "keys"])("keep %s from pulling up a page of unrelated settings", (query) => {
		const rows = buildSettingsCommands(appI18n.t, { developerMode: true, diagnostics: true, mac: true });
		expect(filterCommands(rows, query).length).toBeLessThanOrEqual(2);
	});

	it("keep code to the rows that name it", () => {
		const rows = buildSettingsCommands(appI18n.t, { developerMode: true, diagnostics: true, mac: true });
		// Its title, Mobile's "qr code", the Harness page's "codex", Color theme's
		// "VS Code" option, and last, loose letters of "Connected devices".
		const ids = filterCommands(rows, "code").map((item) => item.id);
		expect([...ids].sort()).toEqual(
			["setting:color-theme", "setting:connection-password", "setting:harness", "setting:mobile", "setting:mobile-devices"],
		);
		expect(ids.at(-1)).toBe("setting:mobile-devices");
	});

	it("rank the code keyword above qr code, below a title that says code", () => {
		const rows = [...buildShortcutCommands(appI18n.t, { session: "local" }), ...buildSettingsCommands(appI18n.t, { developerMode: false, diagnostics: false })];
		const byId = (id: string) => rows.find((item) => item.id === id)!;
		// "Pairing code or password" names it in its title, and a title beats a keyword.
		expect(bestMatch(rows, "code")?.id).toBe("setting:connection-password");
		expect(matchScore("code", byId("shortcut:open-in-editor"))).toBeGreaterThan(matchScore("code", byId("setting:mobile")));
		// Mobile's page words stay on the page row: "Connected devices" is found
		// only by its title's loose letters, below the page's "qr code".
		expect(matchScore("code", byId("setting:mobile-devices"))).toBeLessThan(matchScore("code", byId("setting:mobile")));
	});
});

describe("search groups", () => {
	it("keep daemon results below local rows so Enter never moves to them", () => {
		const local = buildCommands({ workspaces: workspaces() });
		const file: CommandItem = { id: "file:x", group: "files", title: "app.ts", action: { kind: "toggle-theme" } };
		const ids = displayGroups([...local, file], "app").map((group) => group.id);
		expect(ids[ids.length - 1]).toBe("files");
		expect(ids[0]).not.toBe("files");
	});

	it("are not cut by the local result cap", () => {
		const projects: WorkspaceSummary[] = Array.from({ length: 50 }, (_, i) => ({
			id: `p${i}`, name: `project-${i}`, path: `/repos/p${i}`, type: "main" as const, sessions: [],
		}));
		const local = buildCommands({ workspaces: projects });
		const chats: CommandItem[] = Array.from({ length: 3 }, (_, index) => ({
			id: `message:${index}`, group: "messages", title: `project chat ${index}`, contentMatch: true,
		}));
		const groups = displayGroups([...local, ...chats], "project");
		expect(groups.find((group) => group.id === "messages")?.items).toHaveLength(3);
	});
});

describe("titleHighlights", () => {
	it("marks the typed text, then each word, then loose letters", () => {
		expect(titleHighlights("side", "Toggle sidebar")).toEqual([{ start: 7, end: 11 }]);
		expect(titleHighlights("toggle bar", "Toggle sidebar")).toEqual([{ start: 0, end: 6 }, { start: 11, end: 14 }]);
		expect(titleHighlights("tgl", "Toggle")).toEqual([{ start: 0, end: 1 }, { start: 2, end: 3 }, { start: 4, end: 5 }]);
		expect(titleHighlights("zz", "Toggle")).toEqual([]);
	});
});

describe("loose keyword matches", () => {
	const setting: CommandItem = { id: "setting:x", group: "settings", title: "Add account", keywords: ["subscriptions", "devices"] };

	it("match a short query across keywords", () => {
		expect(matchScore("sb", setting)).toBe(100);
	});

	it("do not match a longer query whose letters are only scattered across keywords", () => {
		expect(matchScore("side", setting)).toBe(0);
	});
});

describe("exact names", () => {
	it("put a session named exactly what was typed above commands that start with it", () => {
		const items = [
			...buildCommands({ workspaces: [{ ...workspaces()[0]!, sessions: [session({ id: "s-theme", title: "theme" })] }] }),
			...buildSettingsCommands(appI18n.t, { developerMode: false, diagnostics: false }),
		];
		// Enter runs the best match wherever its group sits.
		expect(bestMatch(items, "theme")?.id).toBe("session:s-theme");
	});
});

describe("the blank palette", () => {
	const many = (count: number) => Array.from({ length: count }, (_, index) =>
		session({ id: `s${index}`, status: "working", lastInteractionAt: `2026-06-${String(10 + index).padStart(2, "0")}T00:00:00Z` }));

	it("leaves sessions to a search, most recently worked-in first", () => {
		const items = buildCommands({ workspaces: [{ id: "proj-1", name: "app", path: "/repos/app", type: "main", sessions: many(10) }] });
		expect(displayGroups(items, "").some((group) => group.id === "sessions")).toBe(false);
		expect(goToCommands(items, "sessions").slice(0, 3).map((item) => item.id)).toEqual(["session:s9", "session:s8", "session:s7"]);
		// Typing still searches every one of them.
		expect(filterCommands(items, "s0").some((item) => item.id === "session:s0")).toBe(true);
	});

	it("shows a PR only once someone searches for it", () => {
		const items = buildCommands({ workspaces: workspaces() });
		expect(filterCommands(items, "").some((item) => item.id === "pr:w-pr:42")).toBe(false);
		expect(filterCommands(items, "42").some((item) => item.id === "pr:w-pr:42")).toBe(true);
	});

	it("lists every project and session from Go to project and Go to session", () => {
		const items = buildCommands({ workspaces: workspaces() });
		expect(byId(items).get("global-go-to-project")?.action).toMatchObject({ kind: "open-view", view: { mode: "go-to", target: "projects" } });
		expect(goToCommands(items, "projects").map((item) => item.id)).toEqual(["project:proj-1"]);
		const sessions = goToCommands(items, "sessions");
		// Sessions that need attention are listed too, each once.
		expect(sessions.some((item) => item.id === "attention:w-action")).toBe(true);
		expect(sessions.some((item) => item.id === "session:w-working")).toBe(true);
		expect(new Set(sessions.map((item) => item.id.split(":")[1])).size).toBe(sessions.length);
	});

	it("opens every project's archived sessions, once there are some", () => {
		expect(byId(buildCommands({ workspaces: workspaces() })).has("global-archived-sessions")).toBe(false);
		const withArchived = [{ ...workspaces()[0]!, sessions: [session({ id: "old", isTerminated: true })] }];
		expect(byId(buildCommands({ workspaces: withArchived })).get("global-archived-sessions")?.action).toEqual({ kind: "open-archive" });
	});

	it("reaches the sidebar's Automations and Artifacts pages", () => {
		const items = byId(buildCommands({ workspaces: workspaces() }));
		expect(items.get("global-automations")?.action).toEqual({ kind: "navigate", target: { to: "/automations" } });
		expect(items.get("global-artifacts")?.action).toEqual({ kind: "navigate", target: { to: "/artifacts" } });
	});

	it("marks Cloud sessions and projects", () => {
		const cloud = { ...workspaces()[0]!, id: "cloud-1", name: "web", kind: CLOUD_PROJECT_KIND, sessions: [session({ id: "c1", workspaceId: "cloud-1" })] } as WorkspaceSummary;
		const items = byId(buildCommands({ workspaces: [cloud] }));
		expect(items.get("project:cloud-1")?.subtitle).toBe("Cloud");
		expect(items.get("session:c1")?.subtitle).toBe("web · Cloud");
	});
});

describe("project actions", () => {
	const ctx = { isPinned: false, canPin: true, restarting: false, hasOrchestrator: true, setPinned: () => {}, copyPath: () => {}, remove: () => {} };

	it("offers what the sidebar's project menu offers", () => {
		const project = { ...workspaces()[0]!, sessions: [...workspaces()[0]!.sessions, session({ id: "old", isTerminated: true })] };
		const items = buildProjectActions(project, ctx);
		expect(items.map((item) => item.id)).toEqual([
			"project-action:new-task",
			"project-action:orchestrator",
			"project-action:board",
			"project-action:archive",
			"project-action:copy-path",
			"project-action:pin",
			"project-action:settings",
			"project-action:remove",
		]);
		expect(byId(items).get("project-action:archive")?.state).toBe("1");
	});

	it("leaves out board, settings and remove for Scratchpad, and pin when it can't pin", () => {
		const scratch: WorkspaceSummary = { id: STANDALONE_WORKSPACE_ID, name: "Scratchpad", path: "", type: "main", sessions: [] };
		const ids = buildProjectActions(scratch, { ...ctx, canPin: false }).map((item) => item.id);
		expect(ids).toEqual(["project-action:new-task"]);
	});

	it("opens a project's actions from its row", () => {
		const project = byId(buildCommands({ workspaces: workspaces() })).get("project:proj-1");
		expect(project?.secondaryAction).toEqual({ kind: "open-view", view: { mode: "project-actions", projectId: "proj-1" } });
	});
});

describe("context menu rows", () => {
	const run = () => {};
	const entries = [
		{ kind: "item" as const, id: "session-pin", label: "Pin", onSelect: run },
		{ kind: "item" as const, id: "session-off", label: "Off", disabled: true },
		{ kind: "separator" as const, id: "sep" },
		{ kind: "submenu" as const, id: "session-open-in", label: "Open in", items: [{ kind: "item" as const, id: "open-finder", label: "Finder", onSelect: run }] },
	];

	it("turns items and submenus into rows and skips separators", () => {
		const items = menuEntryCommands(entries, "s1");
		expect(items.map((item) => item.id)).toEqual(["menu:session-pin", "menu:session-off", "menu:session-open-in"]);
		expect(items[0]?.action).toEqual({ kind: "run", run });
		expect(items[1]?.disabled).toBe(true);
		expect(items[2]?.action).toEqual({ kind: "open-view", view: { mode: "menu-submenu", sessionId: "s1", entryId: "session-open-in", title: "Open in" } });
		expect(findSubmenuEntries(entries, "session-open-in")?.map((entry) => entry.id)).toEqual(["open-finder"]);
	});
});

describe("chat search rows", () => {
	const hit = (messageId: string, sessionId: string, text: string) => ({ messageId, sessionId, projectId: "proj-1", title: `Chat ${sessionId}`, text });

	it("shows one row per chat, its best match first, with how many matched", () => {
		const rows = buildChatSearchCommands([
			hit("m1", "s1", "first \x02up\x03"),
			hit("m2", "s1", "second \x02up\x03"),
			hit("m3", "s2", "other \x02up\x03"),
			hit("m4", "s1", "third \x02up\x03"),
		]);
		expect(rows.map((row) => row.id)).toEqual(["message:m1", "message:m3"]);
		expect(rows[0]?.state).toBe("3 matches");
		expect(rows[1]?.state).toBeUndefined();
	});

	it("reads a multi-line match as one line", () => {
		const [row] = buildChatSearchCommands([hit("m1", "s1", "line one\n\n  line \x02two\x03")]);
		expect(row?.snippet?.text).toBe("line one line two");
		expect(row?.snippet?.highlights).toEqual([{ start: 14, end: 17 }]);
	});
});

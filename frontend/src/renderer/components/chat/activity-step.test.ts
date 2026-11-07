import { describe, expect, it } from "vitest";
import type { ConversationActivity } from "../../types/conversation";
import {
	activityCommandStep,
	activityToolStep,
	describeCommandStep,
	isExploratoryTool,
	thoughtHeadline,
	type CommandStep,
} from "./activity-step";

/** The finished row's text, as it reads: verb, then target. */
function done(command: string): string {
	return text(describeCommandStep(command), "done");
}

function live(command: string): string {
	return text(describeCommandStep(command), "live");
}

function text(step: CommandStep, tense: "done" | "live"): string {
	return [step[tense], step.target].filter(Boolean).join(" ");
}

function commandActivity(overrides: Partial<ConversationActivity>): ConversationActivity {
	return {
		kind: "activity",
		id: "a1",
		activityKind: "command",
		status: "completed",
		summary: "",
		sequence: 1,
		revision: 1,
		...overrides,
	} as ConversationActivity;
}

describe("describeCommandStep", () => {
	it("names reads by the file they read", () => {
		expect(done("sed -n '1,40p' README.md")).toBe("Read README.md");
		expect(done("head -n 40 docs/README.md")).toBe("Read README.md");
		expect(describeCommandStep("head -n 40 docs/README.md").targetTitle).toBe("docs/README.md");
		expect(done("cat package.json tsconfig.json")).toBe("Read 2 files package.json, tsconfig.json");
		expect(done("nl -ba src/app.ts")).toBe("Read app.ts");
		expect(live("sed -n '1,40p' README.md")).toBe("Reading README.md");
		expect(done("sed -n '1,80p' src/a.ts && sed -n '1,80p' src/b.ts")).toBe("Read 2 files a.ts, b.ts");
		expect(done("sed -i 's/a/b/' src/a.ts")).toBe("Edited a.ts");
	});

	it("keeps the file when a reader's flag is a switch, not a value", () => {
		expect(done("cat -n src/a.ts")).toBe("Read a.ts");
		expect(done("wc -c a.ts")).toBe("Read a.ts");
		expect(done("jq -r '.version' package.json")).toBe("Read package.json");
		expect(done("tail -n 20 server.log")).toBe("Read server.log");
		expect(done("bat -r 1:40 src/a.ts")).toBe("Read a.ts");
	});

	it("skips BSD sed's backup suffix after a bare -i", () => {
		expect(done("sed -i '' 's/a/b/' src/a.ts")).toBe("Edited a.ts");
		expect(done("sed -i .bak 's/a/b/' src/a.ts")).toBe("Edited a.ts");
		expect(done("sed -i.bak 's/a/b/' src/a.ts")).toBe("Edited a.ts");
	});

	it("names listings, searches and finds", () => {
		expect(done("ls")).toBe("Listed files");
		expect(done("ls -la .")).toBe("Listed files");
		expect(done("ls does-not-exist")).toBe("Listed does-not-exist");
		expect(done('rg -n "TODO" .')).toBe("Searched for TODO");
		expect(done("rg -c TODO --glob '!node_modules'")).toBe("Searched for TODO");
		expect(done("grep -rn -e 'needle' src")).toBe("Searched for needle");
		expect(done('grep ">" notes.md')).toBe("Searched for >");
		expect(done("rg --files src")).toBe("Listed src");
		expect(done('find . -type f -name "*.test.ts" | wc -l')).toBe("Found files *.test.ts");
		expect(done("fd component")).toBe("Found files component");
		expect(live('rg -n "TODO" .')).toBe("Searching for TODO");
	});

	it("names git and GitHub steps", () => {
		expect(done("git status")).toBe("Checked git status");
		expect(done("git -C frontend status --short")).toBe("Checked git status");
		expect(done("git log --oneline -3")).toBe("Viewed recent commits");
		expect(done("git branch --show-current")).toBe("Checked current branch");
		expect(done("git diff src/app.ts")).toBe("Viewed changes app.ts");
		expect(done("git checkout -b feat/labels")).toBe("Created branch feat/labels");
		expect(done("git switch main")).toBe("Switched to main");
		expect(done("git commit -m 'fix: x'")).toBe("Committed changes");
		expect(done("git rev-parse HEAD")).toBe("Checked repository");
		expect(done("gh pr view 7804 --json title,number")).toBe("Read PR #7804");
		expect(done("gh pr view https://github.com/o/r/pull/12")).toBe("Read PR #12");
		expect(done("gh pr list --limit 1 --state open")).toBe("Listed pull requests");
		expect(done("gh issue view 44")).toBe("Read issue #44");
		expect(live("gh pr view 7804")).toBe("Reading PR #7804");
	});

	it("names file operations", () => {
		expect(done("mkdir -p activity-demo")).toBe("Created folder activity-demo");
		expect(done("mkdir a b")).toBe("Created 2 folders a, b");
		expect(done("touch activity-demo/notes.md")).toBe("Created notes.md");
		expect(done("mv activity-demo/hello.js activity-demo/greet.js")).toBe("Renamed hello.js → greet.js");
		expect(done("mv notes.md archive/")).toBe("Moved notes.md → archive");
		expect(done("cp a.txt b.txt")).toBe("Copied a.txt → b.txt");
		expect(done("rm -rf activity-demo")).toBe("Deleted activity-demo");
		expect(done("rm a.txt b.txt")).toBe("Deleted 2 items a.txt, b.txt");
		expect(live("rm -rf activity-demo")).toBe("Deleting activity-demo");
	});

	it("names writes through redirects and heredocs, not the heredoc body", () => {
		expect(done("cat > activity-demo/notes.md <<'EOF'\nline one\nline two\nEOF")).toBe("Wrote notes.md");
		expect(done("cat <<EOF > notes.md\nhello\nEOF")).toBe("Wrote notes.md");
		expect(done("echo hello >> log.txt")).toBe("Appended to log.txt");
		expect(done("printf 'a\\n' | tee out.txt")).toBe("Ran printf");
		expect(done("tee out.txt")).toBe("Wrote out.txt");
		// Routing a stream is not writing a file.
		expect(done("rg TODO 2>/dev/null")).toBe("Searched for TODO");
		expect(done("ls missing 2>&1")).toBe("Listed missing");
	});

	it("names scripts, tests, installs and builds", () => {
		expect(done("node activity-demo/hello.js")).toBe("Ran hello.js");
		expect(live("node activity-demo/hello.js")).toBe("Running hello.js");
		expect(done("python3 scripts/check.py")).toBe("Ran check.py");
		expect(done("./scripts/setup.sh")).toBe("Ran setup.sh");
		expect(done("npm test")).toBe("Ran tests");
		expect(done("pnpm --filter frontend test")).toBe("Ran tests");
		expect(done("npm run test:unit")).toBe("Ran tests");
		expect(done("npx vitest run src")).toBe("Ran tests");
		expect(done("go test ./...")).toBe("Ran tests");
		expect(done("python -m pytest -q")).toBe("Ran tests");
		expect(done("npm install")).toBe("Installed packages");
		expect(done("npm i lodash")).toBe("Installed packages lodash");
		expect(done("npm run build")).toBe("Built project");
		expect(done("npx tsc --noEmit")).toBe("Type-checked");
		expect(done("npm run lint")).toBe("Ran lint");
		expect(done("npx prettier --write src")).toBe("Formatted files");
		expect(done("npm run seed")).toBe("Ran seed");
		expect(done("make test")).toBe("Ran tests");
		expect(done("make release")).toBe("Ran make release");
	});

	it("names network fetches and waits", () => {
		expect(done("curl -s https://nodejs.org/dist/index.json")).toBe("Fetched nodejs.org");
		expect(done("sleep 6 && echo done")).toBe("Waited 6s");
		expect(live("sleep 6 && echo done")).toBe("Waiting 6s");
	});

	it("looks past cd, env assignments, wrappers and shell -c", () => {
		expect(done("cd frontend && rg useState")).toBe("Searched for useState");
		expect(done("(cd frontend && ls src)")).toBe("Listed src");
		expect(done("CI=1 npm test")).toBe("Ran tests");
		expect(done("timeout 30 npm test")).toBe("Ran tests");
		expect(done("bash -lc 'git status'")).toBe("Checked git status");
	});

	it("names any other program instead of saying Ran command", () => {
		expect(done("docker compose up -d")).toBe("Ran docker");
		expect(live("docker compose up -d")).toBe("Running docker");
		expect(done("node -e 'console.log(1)'")).toBe("Ran node");
		// `-` is stdin, usually a heredoc, not a script named "-".
		expect(done("python3 - <<'PY'\nprint(1)\nPY")).toBe("Ran python3");
		expect(done("bash - <<'SH'\necho hi\nSH")).toBe("Ran bash");
		expect(done("sh -")).toBe("Ran sh");
		expect(describeCommandStep("docker ps").icon).toBe("run");
		expect(done("")).toBe("Ran command");
	});
});

describe("program checks", () => {
	it("names the work after a `command -v` check, and a check on its own", () => {
		expect(done("command -v gh && gh pr list --state open --limit 1 --json number,title")).toBe("Listed pull requests");
		expect(done("which node")).toBe("Checked for node");
		expect(done("command -v gh")).toBe("Checked for gh");
	});
});

describe("activityCommandStep", () => {
	it("reads the command from detail when there is one", () => {
		const step = activityCommandStep(commandActivity({ summary: "ls", detail: { command: "rg TODO" } }));
		expect(text(step, "done")).toBe("Searched for TODO");
	});

	it("names a Codex web search, which has only a summary", () => {
		const step = activityCommandStep(commandActivity({ summary: "Searched the web for latest stable Node.js version" }));
		expect(step).toMatchObject({ done: "Searched the web for", live: "Searching the web for", target: "latest stable Node.js version", icon: "web" });
		expect(text(activityCommandStep(commandActivity({ summary: "Searched the web" })), "done")).toBe("Searched the web");
	});

	it("reads a summary that is a known command line", () => {
		expect(text(activityCommandStep(commandActivity({ summary: "`git status`" })), "done")).toBe("Checked git status");
	});

	it("keeps the generic label for a prose summary", () => {
		expect(text(activityCommandStep(commandActivity({ summary: "Ran a command" })), "done")).toBe("Ran command");
		expect(text(activityCommandStep(commandActivity({ summary: "List files in the folder" })), "done")).toBe("Ran command");
	});
});

/** Claude Code's built-in tools over ACP, with the detail its driver records. */
function toolActivity(providerToolName: string | undefined, toolKind: string, input: unknown, summary = ""): ConversationActivity {
	return commandActivity({
		activityKind: "mcp_tool",
		summary,
		detail: { protocol: "acp", providerToolName, toolKind, input } as ConversationActivity["detail"],
	});
}

describe("thoughtHeadline", () => {
	it("takes Codex's newest bold heading", () => {
		const text = "**Planning the layout**\n\nI will sketch it first.\n\n**Designing a seven-day cadence strip for each card**\n\nEach card gets";
		expect(thoughtHeadline(text)).toBe("Designing a seven-day cadence strip for each card");
	});

	it("takes the last whole sentence of Claude's summary, which says what comes next", () => {
		expect(
			thoughtHeadline(
				"Versionlog lists Node.js 26.13.0 as the latest stable release, but it doesn't label 26.x as LTS, so 24.x is still the newest LTS line. I'm creating a fresh to-do list for steps 10 to 17 next.\n\n",
			),
		).toBe("I'm creating a fresh to-do list for steps 10 to 17 next");
		expect(
			thoughtHeadline("Only one React copy exists. I'm restoring the `package-lock.json` that `npm install` rewrote, then rebuilding."),
		).toBe("I'm restoring the package-lock.json that npm install rewrote, then rebuilding");
	});

	it("skips a sentence still being written, and has no gist before one is whole", () => {
		expect(thoughtHeadline("The build passes. Now I'll verify the bui")).toBe("The build passes");
		expect(thoughtHeadline("Looking at the")).toBeUndefined();
		expect(thoughtHeadline("")).toBeUndefined();
	});
});

describe("activityToolStep", () => {
	it("names a web search by its query instead of showing the bare query", () => {
		const search = toolActivity("WebSearch", "fetch", { query: "latest stable Node.js version" }, '"latest stable Node.js version"');
		const step = activityToolStep(search)!;
		expect(text(step, "done")).toBe("Searched the web for latest stable Node.js version");
		expect(text(step, "live")).toBe("Searching the web for latest stable Node.js version");
		expect(step.icon).toBe("web");
	});

	it("names reads, searches and fetches like the commands that do the same", () => {
		const read = activityToolStep(toolActivity("Read", "read", { file_path: "/w/market-place/README.md", limit: 40 }, "Read README.md (1 - 40)"))!;
		expect(read).toMatchObject({ done: "Read", target: "README.md", targetTitle: "/w/market-place/README.md", icon: "read" });
		expect(text(activityToolStep(toolActivity("Grep", "search", { pattern: "TODO" }))!, "done")).toBe("Searched for TODO");
		expect(text(activityToolStep(toolActivity("Glob", "search", { pattern: "**/*.test.ts" }))!, "done")).toBe("Found files **/*.test.ts");
		const fetch = activityToolStep(toolActivity("WebFetch", "fetch", { url: "https://versionlog.com/nodejs/" }))!;
		expect(fetch).toMatchObject({ done: "Fetched", target: "versionlog.com", targetTitle: "https://versionlog.com/nodejs/" });
		expect(text(activityToolStep(toolActivity("ToolSearch", "other", { query: "select:WebSearch,TaskCreate" }))!, "done")).toBe(
			"Loaded tools WebSearch, TaskCreate",
		);
	});

	it("keeps an MCP server's tool and an unknown tool's title", () => {
		const mcp = commandActivity({ activityKind: "mcp_tool", summary: "search_issues", detail: { server: "linear", toolName: "search_issues" } });
		expect(activityToolStep(mcp)).toBeUndefined();
		expect(activityToolStep(toolActivity("SubagentHandback", "other", {}, "SubagentHandback"))).toBeUndefined();
		// Another ACP agent's read keeps its own title and gains only the icon.
		expect(activityToolStep(toolActivity(undefined, "read", {}, "ReadFile src/app.ts"))).toMatchObject({ done: "ReadFile src/app.ts", icon: "read" });
	});

	it("counts reads, searches and fetches as exploring", () => {
		expect(isExploratoryTool(toolActivity("Read", "read", {}))).toBe(true);
		expect(isExploratoryTool(toolActivity("WebSearch", "fetch", {}))).toBe(true);
		expect(isExploratoryTool(toolActivity("ToolSearch", "other", {}))).toBe(false);
		expect(isExploratoryTool(commandActivity({ summary: "cat a.ts", detail: { command: "cat a.ts" } }))).toBe(false);
	});
});

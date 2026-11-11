import { describe, expect, it } from "vitest";
import type { ConversationActivity } from "../../types/conversation";
import {
	activityCommandStep,
	activityToolStep,
	describeCommandStep,
	isExploratoryTool,
	isOrchestratorPlumbing,
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
		// A depth or an ignore pattern is the option's value, not the folder.
		expect(done("tree -L 2")).toBe("Listed files");
		expect(done("tree -L 2 -I node_modules src")).toBe("Listed src");
		expect(done("eza --level 3 docs")).toBe("Listed docs");
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
		expect(done("gh api -X POST repos/o/r/issues")).toBe("Called the GitHub API repos/o/r/issues");
		expect(done("gh pr review 12 --approve")).toBe("Approved PR #12");
		expect(done("gh pr review 12 --comment -b ok")).toBe("Commented on PR #12");
	});

	it("says what a git subcommand's action did", () => {
		expect(done("git stash")).toBe("Stashed changes");
		expect(done("git stash list")).toBe("Listed stashes");
		expect(done("git stash pop")).toBe("Restored stash");
		expect(done("git stash drop")).toBe("Dropped stash");
		expect(done("git merge --abort")).toBe("Aborted merge");
		expect(done("git merge feat/x")).toBe("Merged feat/x");
		expect(done("git rebase --continue")).toBe("Continued rebase");
		expect(done("git checkout .")).toBe("Restored files");
		expect(done("git checkout -- src/a.ts")).toBe("Restored a.ts");
		expect(done("git checkout feat/x")).toBe("Switched to feat/x");
	});

	it("names file operations", () => {
		expect(done("mkdir -p activity-demo")).toBe("Created folder activity-demo");
		expect(done("mkdir a b")).toBe("Created 2 folders a, b");
		expect(done("touch activity-demo/notes.md")).toBe("Created notes.md");
		expect(done("mv activity-demo/hello.js activity-demo/greet.js")).toBe("Renamed hello.js → greet.js");
		expect(done("mv notes.md archive/")).toBe("Moved notes.md → archive");
		// Without the slash `archive` may be a folder: only a clear rename says Renamed.
		expect(done("mv notes.md archive")).toBe("Moved notes.md → archive");
		expect(done("cp a.txt b.txt")).toBe("Copied a.txt → b.txt");
		expect(done("rm -rf activity-demo")).toBe("Deleted activity-demo");
		expect(done("rm a.txt b.txt")).toBe("Deleted 2 items a.txt, b.txt");
		expect(live("rm -rf activity-demo")).toBe("Deleting activity-demo");
	});

	it("names writes through redirects and heredocs, not the heredoc body", () => {
		expect(done("cat > activity-demo/notes.md <<'EOF'\nline one\nline two\nEOF")).toBe("Wrote notes.md");
		expect(done("cat <<EOF > notes.md\nhello\nEOF")).toBe("Wrote notes.md");
		expect(done("echo hello >> log.txt")).toBe("Appended to log.txt");
		expect(done("tee out.txt")).toBe("Wrote out.txt");
		// `tee` writes the file wherever it sits in the pipeline.
		expect(done("printf 'a\\n' | tee out.txt")).toBe("Wrote out.txt");
		expect(done("echo hi | tee notes.md")).toBe("Wrote notes.md");
		expect(done("cat <<EOF | tee notes.md\nhello\nEOF")).toBe("Wrote notes.md");
		expect(done("echo 127.0.0.1 | sudo tee /etc/hosts")).toBe("Wrote hosts");
		expect(done("echo hi | tee -a log.txt")).toBe("Appended to log.txt");
		// Routing a stream is not writing a file.
		expect(done("rg TODO 2>/dev/null")).toBe("Searched for TODO");
		expect(done("ls missing 2>&1")).toBe("Listed missing");
	});

	it("keeps the work a `tee` only logs", () => {
		expect(done("npm test 2>&1 | tee /tmp/test.log")).toBe("Ran tests");
		expect(done("go test ./... | tee out.txt")).toBe("Ran tests");
		expect(done("make build | tee build.log")).toBe("Built project");
	});

	it("names a chain by the last segment that does something", () => {
		expect(done("git add -A && git commit -m x && git push")).toBe("Pushed changes");
		expect(done("rm -rf dist && npm run build")).toBe("Built project");
		expect(done("npm ci && npm test")).toBe("Ran tests");
		expect(done("sleep 30 && gh pr checks 12")).toBe("Checked CI #12");
		expect(done("cat a.ts && rm -rf src")).toBe("Deleted src");
		// A trailing look or report does not take the name from the work before it.
		expect(done("npm test && echo done")).toBe("Ran tests");
		expect(done("git add . && git status")).toBe("Staged changes");
		// With nothing but looking, the first one names it.
		expect(done("git status && git diff")).toBe("Checked git status");
	});

	it("reads every line of a multi-line command", () => {
		expect(done("git add .\ngit commit -m x")).toBe("Committed changes");
		expect(done("cd frontend\nnpm test")).toBe("Ran tests");
		expect(done("npm test |\n  tee log.txt")).toBe("Ran tests");
		expect(done('git commit -m "first line\nsecond line"')).toBe("Committed changes");
		expect(done("git commit -m \"$(cat <<'EOF'\nrm -rf everything\nEOF\n)\"")).toBe("Committed changes");
		expect(done("cat > a.sh <<'EOF'\nrm -rf everything\nEOF\nnpm test")).toBe("Ran tests");
	});

	it("leaves comments out", () => {
		expect(done("# Check status\ngit status")).toBe("Checked git status");
		expect(done("rm a.txt # don't keep it")).toBe("Deleted a.txt");
		expect(done("#!/bin/bash\nnpm test")).toBe("Ran tests");
		expect(done('grep "# heading" notes.md')).toBe("Searched for # heading");
	});

	it("reads `&>` as a redirect and `$(...)` as part of its word", () => {
		expect(done("command -v gh &>/dev/null && gh pr list")).toBe("Listed pull requests");
		expect(done("npm test &> out.log")).toBe("Ran tests");
		expect(done("cd $(git rev-parse --show-toplevel) && npm test")).toBe("Ran tests");
		expect(done("FOO=$(date) npm test")).toBe("Ran tests");
		expect(done("rm -rf $(find . -name x)")).toBe("Deleted $(find . -name x)");
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
		// make's folder, file and job count are option values, not the target.
		expect(done("make -C frontend test")).toBe("Ran tests");
		expect(done("make -f ci.mk -j 4 release")).toBe("Ran make release");
		expect(done("make -j test")).toBe("Ran tests");
		// `go run` names a file, and a package as typed.
		expect(done("go run main.go")).toBe("Ran main.go");
		expect(done("go run .")).toBe("Ran go run .");
		expect(done("go run ./cmd/server")).toBe("Ran go run ./cmd/server");
	});

	it("skips deno's and tsx's subcommand before the script", () => {
		expect(done("deno run -A main.ts")).toBe("Ran main.ts");
		expect(done("deno main.ts")).toBe("Ran main.ts");
		expect(done("deno test")).toBe("Ran tests");
		expect(describeCommandStep("deno test").icon).toBe("test");
		expect(done("deno task dev")).toBe("Started dev");
		expect(done("deno eval 'console.log(1)'")).toBe("Ran deno eval");
		expect(done("tsx watch src/index.ts")).toBe("Ran index.ts");
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
		// A wrapper's option values are not the program.
		expect(done("sudo -u postgres psql")).toBe("Ran psql");
		expect(done("timeout -s KILL 30 npm test")).toBe("Ran tests");
		expect(done("env -u DEBUG node server.js")).toBe("Ran server.js");
	});

	it("looks past shell grammar to the program it runs", () => {
		expect(done('for f in src/*.ts; do wc -l "$f"; done')).toBe("Read file");
		const lookup = 'for p in AGENTS.md ../AGENTS.md; do if [ -f "$p" ]; then printf "\\n%s\\n" "$p"; cat "$p"; fi; done';
		expect(done(lookup)).toBe("Looked for AGENTS.md");
		expect(live(lookup)).toBe("Looking for AGENTS.md");
		const prefixedLookup = '"ls -la; rg --files --hidden; for p in /AGENTS.md /Users/AGENTS.md; do if [ -f \\"$p" ]; then printf "\\n%s\\n" "$p"; cat "$p"; fi; done\'';
		expect(done(prefixedLookup)).toBe("Looked for AGENTS.md");
		expect(done('for p in AGENTS.md ../AGENTS.md; do if [ -f "$p" ]; then cat "$p"; rm "$p"; fi; done')).not.toBe("Looked for AGENTS.md");
		expect(done("if [ -f a.ts ]; then cat a.ts; fi")).toBe("Read a.ts");
		expect(done("if ! grep -q TODO notes.md; then echo none; fi")).toBe("Searched for TODO");
		expect(done("while true; do sleep 5; done")).toBe("Waited 5s");
		expect(done("test -f a.ts && cat a.ts")).toBe("Read a.ts");
	});

	it("names files in any script and Windows paths by their name", () => {
		expect(done("cat ファイル.md")).toBe("Read ファイル.md");
		expect(done("cat C:\\repo\\src\\a.ts")).toBe("Read a.ts");
		expect(describeCommandStep("cat C:\\repo\\src\\a.ts").targetTitle).toBe("C:\\repo\\src\\a.ts");
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

	it("names a line that only sets up the shell", () => {
		expect(done("cd frontend")).toBe("Changed folder frontend");
		expect(live("cd /w/app/src")).toBe("Changing folder src");
		expect(done("export CI=1 FOO=2")).toBe("Set CI, FOO");
		expect(done("set -euo pipefail")).toBe("Set shell options");
		expect(done("source .env")).toBe("Loaded .env");
		expect(done("test -f a.ts")).toBe("Checked condition");
		expect(done("[ -f a.ts ]")).toBe("Checked condition");
		// Setup in front of real work does not name it.
		expect(done("cd frontend && npm test")).toBe("Ran tests");
	});
});

describe("Orchestrator CLI", () => {
	it("names ao commands for what they do, never as ao", () => {
		expect(done("ao preview /data/artifacts/s-1/hello.html")).toBe("Previewed hello.html");
		expect(done("ao browser console | grep -c 'Button clicked'")).toBe("Checked the browser console for Button clicked");
		expect(done("ao browser console --json | python3 -c 'print(1)'")).toBe("Checked the browser console");
		expect(done("ao browser console; rg TODO src | head")).toBe("Checked the browser console");
		expect(done("ao browser tab new http://localhost:3000")).toBe("Opened http://localhost:3000");
		expect(done("ao browser errors | head -20")).toBe("Checked the page for errors");
		expect(done('ao browser act "Click me button" --expect-dom-change')).toBe("Interacted with the page");
		expect(done("ao artifact add /data/artifacts/s-1/hello.html")).toBe("Saved deliverable hello.html");
		expect(done('ao report --checkpoint --note "tests pass"')).toBe("Shared progress");
		expect(done('ao report --done --note "shipped"')).toBe("Marked the task done");
		expect(done("ao session claim-pr https://github.com/o/r/pull/12")).toBe("Took over PR #12");
		expect(done("/Users/me/app/daemon/ao frobnicate")).toBe("Used Orchestrator");
		expect(live("ao preview README.md")).toBe("Previewing README.md");
	});

	it("names a chain of browser steps by the step that acts", () => {
		expect(done('ao preview a.html && ao browser wait --load && ao browser act "Click me" ; ao browser console')).toBe(
			"Interacted with the page",
		);
	});

	it("treats reading Orchestrator's guides and ao --help as plumbing", () => {
		const guide = "/Users/me/.ao/data/skills/using-ao/commands/preview.md";
		expect(isOrchestratorPlumbing(commandActivity({ detail: { command: `cat ${guide}` } }))).toBe(true);
		expect(isOrchestratorPlumbing(commandActivity({ detail: { command: `sed -n 1,80p ${guide}` } }))).toBe(true);
		expect(isOrchestratorPlumbing(commandActivity({ detail: { command: "ao browser --help" } }))).toBe(true);
		expect(isOrchestratorPlumbing(toolActivity("Read", "read", { file_path: "/Users/me/.ao/data/skills/using-ao/SKILL.md" }))).toBe(true);
		expect(isOrchestratorPlumbing(commandActivity({ detail: { command: `cat ${guide} README.md` } }))).toBe(false);
		expect(isOrchestratorPlumbing(commandActivity({ detail: { command: `cat ${guide} notes` } }))).toBe(false);
		expect(isOrchestratorPlumbing(commandActivity({ detail: { command: `cat ${guide} | cat > guide-copy.md` } }))).toBe(false);
		expect(isOrchestratorPlumbing(commandActivity({ detail: { command: `cat ${guide} | head -40` } }))).toBe(false);
		expect(isOrchestratorPlumbing(commandActivity({ detail: { command: `source ./setup.sh && cat ${guide}` } }))).toBe(false);
		expect(isOrchestratorPlumbing(commandActivity({ detail: { command: `. ./setup.sh; cat ${guide}` } }))).toBe(false);
		expect(isOrchestratorPlumbing(commandActivity({ detail: { command: `cd /tmp && cat ${guide}` } }))).toBe(true);
		expect(isOrchestratorPlumbing(commandActivity({ detail: { command: `sed -e 'w copy.md' ${guide}` } }))).toBe(false);
		expect(isOrchestratorPlumbing(commandActivity({ detail: { command: `sed 'w copy.md' ${guide}` } }))).toBe(false);
		expect(isOrchestratorPlumbing(commandActivity({ detail: { command: "ao browser --help" }, status: "failed" }))).toBe(false);
		expect(isOrchestratorPlumbing(commandActivity({ detail: { command: `cat ${guide}` }, status: "running" }))).toBe(true);
		expect(isOrchestratorPlumbing(commandActivity({ detail: { command: "ao preview README.md" } }))).toBe(false);
		expect(isOrchestratorPlumbing(toolActivity("Read", "read", { file_path: "/w/app/README.md" }))).toBe(false);
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

	it("shows a provider's title as written when there is no command line", () => {
		expect(text(activityCommandStep(commandActivity({ summary: "List files in the folder" })), "done")).toBe("List files in the folder");
		// Prose can start with a program's name; only a backticked title is a command.
		expect(text(activityCommandStep(commandActivity({ summary: "make the build pass" })), "done")).toBe("make the build pass");
		expect(text(activityCommandStep(commandActivity({ summary: "touch up README" })), "done")).toBe("touch up README");
		expect(text(activityCommandStep(commandActivity({ summary: "`touch README`" })), "done")).toBe("Created README");
		expect(text(activityCommandStep(commandActivity({ summary: "`docker ps`" })), "done")).toBe("Ran docker");
	});

	it("keeps the generic label for a title that names nothing", () => {
		expect(text(activityCommandStep(commandActivity({ summary: "Ran a command" })), "done")).toBe("Ran command");
		expect(text(activityCommandStep(commandActivity({ summary: "Terminal" })), "done")).toBe("Ran command");
		expect(text(activityCommandStep(commandActivity({ summary: "" })), "done")).toBe("Ran command");
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
		const windowsRead = activityToolStep(toolActivity("Read", "read", { file_path: "C:\\repo\\src\\a.ts" }))!;
		expect(windowsRead).toMatchObject({ target: "a.ts", targetTitle: "C:\\repo\\src\\a.ts" });
		expect(text(activityToolStep(toolActivity("LS", "search", { path: "." }))!, "done")).toBe("Listed files");
		expect(activityToolStep(toolActivity("LS", "search", { path: "/w/app/src" }))).toMatchObject({ target: "src", targetTitle: "/w/app/src" });
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
		// Claude Code names an MCP tool in full, with no separate server field.
		expect(activityToolStep(toolActivity("mcp__linear__get_issue", "read", {}, "mcp__linear__get_issue"))).toBeUndefined();
		expect(isExploratoryTool(toolActivity("mcp__linear__get_issue", "read", {}, "mcp__linear__get_issue"))).toBe(false);
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

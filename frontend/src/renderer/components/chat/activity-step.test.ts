import { describe, expect, it } from "vitest";
import type { ConversationActivity } from "../../types/conversation";
import { activityCommandStep, describeCommandStep, type CommandStep } from "./activity-step";

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
		expect(describeCommandStep("docker ps").icon).toBe("run");
		expect(done("")).toBe("Ran command");
	});
});

describe("activityCommandStep", () => {
	it("reads the command from detail when there is one", () => {
		const step = activityCommandStep(commandActivity({ summary: "ls", detail: { command: "rg TODO" } }));
		expect(text(step, "done")).toBe("Searched for TODO");
	});

	it("names a Codex web search, which has only a summary", () => {
		const step = activityCommandStep(commandActivity({ summary: "Searched the web for latest stable Node.js version" }));
		expect(step).toMatchObject({ done: "Searched the web", live: "Searching the web", target: "latest stable Node.js version", icon: "web" });
		expect(activityCommandStep(commandActivity({ summary: "Searched the web" })).target).toBeUndefined();
	});

	it("reads a summary that is a known command line", () => {
		expect(text(activityCommandStep(commandActivity({ summary: "`git status`" })), "done")).toBe("Checked git status");
	});

	it("keeps the generic label for a prose summary", () => {
		expect(text(activityCommandStep(commandActivity({ summary: "Ran a command" })), "done")).toBe("Ran command");
		expect(text(activityCommandStep(commandActivity({ summary: "List files in the folder" })), "done")).toBe("Ran command");
	});
});

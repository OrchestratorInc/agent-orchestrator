import { describe, expect, it } from "vitest";
import { commandCategory } from "./activity-command";

describe("commandCategory", () => {
	it("groups a command by the program its row is named by", () => {
		expect(commandCategory("cd repo && rg TODO .")).toBe("search");
		expect(commandCategory("FOO=1 cat a.ts")).toBe("read");
		expect(commandCategory("command -v gh && gh pr view 1")).toBe("vcs");
		expect(commandCategory("/bin/sed -n '1,40p' a.ts")).toBe("read");
		expect(commandCategory("ls -la")).toBe("search");
		expect(commandCategory("timeout 30 npm test")).toBe("run");
		expect(commandCategory("")).toBe("run");
	});

	it("does not count a command named as a write as exploring", () => {
		expect(commandCategory("cat > notes.md <<'EOF'\nhello\nEOF")).toBe("run");
		expect(commandCategory("sed -i 's/a/b/' a.ts")).toBe("run");
		expect(commandCategory("sed -n '1,40p' a.ts")).toBe("read");
	});

	it("groups a chain with the segment that names it", () => {
		expect(commandCategory("cat a.ts && rm -rf src")).toBe("run");
		expect(commandCategory("npm test | tee log.txt")).toBe("run");
		expect(commandCategory('for p in AGENTS.md ../AGENTS.md; do if [ -f "$p" ]; then cat "$p"; fi; done')).toBe("search");
	});

	it("counts a Codex web search as exploring", () => {
		expect(commandCategory("Searched the web for latest Node.js")).toBe("search");
	});
});

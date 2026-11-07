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
});

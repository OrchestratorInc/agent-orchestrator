// @vitest-environment node
import { EventEmitter } from "node:events";
import { expect, it, vi } from "vitest";
import { findArtifactIssues, publishIssueArtifact } from "./github-issue-artifact";

const exec = vi.hoisted(() => vi.fn());
vi.mock("node:child_process", () => ({ execFile: exec }));

function reply(stdout: string, error: Error | null = null, stderr = "") {
	const stdin = Object.assign(new EventEmitter(), { end: vi.fn() });
	exec.mockImplementationOnce((_file, _args, _opts, callback) => {
		queueMicrotask(() => callback(error, stdout, stderr));
		return { stdin };
	});
	return stdin;
}

const destination = { repository: "acme/web", issue: 12, body: "## Demo\n" };

it.each([["report.html", "embedded_html"], ["demo.HTM", "embedded_html"], ["design.md", "generic"], ["notes.Markdown", "generic"]])("posts %s as %s using gh credentials", async (name, type) => {
	const stdin = reply("https://github.com/acme/web/issues/12/artifacts/1\n");
	await expect(publishIssueArtifact({ ...destination, name }, {})).resolves.toEqual({ status: "created", url: "https://github.com/acme/web/issues/12/artifacts/1" });
	const [file, args] = exec.mock.lastCall!;
	expect(file).toBe("gh");
	expect(args).toContain("repos/acme/web/issues/12/artifacts");
	expect(args).toContain("--input");
	expect(stdin.end).toHaveBeenCalledWith(JSON.stringify({ type, name, body: destination.body }));
});

it("rejects other files and invalid destinations before invoking gh", async () => {
	exec.mockClear();
	for (const change of [{ name: "demo.png" }, { repository: "acme/.." }, { issue: 0 }]) {
		await expect(publishIssueArtifact({ ...destination, name: "demo.html", ...change }, {})).rejects.toThrow();
	}
	expect(exec).not.toHaveBeenCalled();
});

it("excludes a pull request entered by number", async () => {
	reply(JSON.stringify({ number: 12, title: "PR", repository_url: "https://api.github.com/repos/acme/web", pull_request: {} }));
	await expect(findArtifactIssues("acme/web", "#12", {})).resolves.toEqual([]);
});

it("matches issue numbers only within the selected repository, ignoring case", async () => {
	const issue = { number: 12, title: "Bug" };
	reply(JSON.stringify({ ...issue, repository_url: "https://api.github.com/repos/Acme/web" }));
	await expect(findArtifactIssues("acme/web", "#12", {})).resolves.toEqual([issue]);
	reply(JSON.stringify({ ...issue, repository_url: "https://api.github.com/repos/acme/moved" }));
	await expect(findArtifactIssues("acme/web", "12", {})).resolves.toEqual([]);
});

it("strips parentheses from the posted name while preserving Markdown content", async () => {
	const stdin = reply("https://github.com/acme/web/issues/12/artifacts/1");
	await publishIssueArtifact({ ...destination, name: "plan (1).md" }, {});
	expect(JSON.parse(stdin.end.mock.calls[0]![0])).toEqual({ type: "generic", name: "plan 1.md", body: destination.body });
});

it.each([
	[Object.assign(new Error("terminated"), { killed: true }), ""],
	[new Error("exit 1"), "gh: Internal Server Error (HTTP 500)"],
	[new Error("exit 1"), "Post: context deadline exceeded"],
])("returns the original issue when GitHub may have stored the upload", async (error, stderr) => {
	reply("", error, stderr);
	await expect(publishIssueArtifact({ ...destination, name: "notes.md" }, {})).resolves.toEqual({
		status: "unconfirmed", url: "https://github.com/acme/web/issues/12",
	});
	expect(exec.mock.lastCall?.[2].timeout).toBeGreaterThan(34_000);
});

it("reports definite GitHub rejections", async () => {
	reply("", new Error("exit 1"), "gh: Validation Failed (HTTP 422)");
	await expect(publishIssueArtifact({ ...destination, name: "notes.md" }, {})).rejects.toThrow("HTTP 422");
});

it("accepts the UTF-8 byte limit and rejects one extra byte before invoking gh", async () => {
	const body = "€".repeat(16_777_215 / 3);
	reply("https://github.com/acme/web/issues/12/artifacts/1");
	await expect(publishIssueArtifact({ ...destination, name: "notes.md", body }, {})).resolves.toMatchObject({ status: "created" });
	exec.mockClear();
	await expect(publishIssueArtifact({ ...destination, name: "notes.md", body: body + "x" }, {})).rejects.toThrow("16,777,215 UTF-8 bytes");
	expect(exec).not.toHaveBeenCalled();
});

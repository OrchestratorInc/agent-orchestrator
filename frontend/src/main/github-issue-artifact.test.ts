// @vitest-environment node
import { EventEmitter } from "node:events";
import { expect, it, vi } from "vitest";
import { findArtifactIssues, publishIssueArtifact } from "./github-issue-artifact";

const exec = vi.hoisted(() => vi.fn());
vi.mock("node:child_process", () => ({ execFile: exec }));

function reply(stdout: string) {
	const stdin = Object.assign(new EventEmitter(), { end: vi.fn() });
	exec.mockImplementationOnce((_file, _args, _opts, callback) => {
		queueMicrotask(() => callback(null, stdout, ""));
		return { stdin };
	});
	return stdin;
}

const destination = { repository: "acme/web", issue: 12, body: "## Demo\n" };

it.each([["report.html", "embedded_html"], ["demo.HTM", "embedded_html"], ["design.md", "generic"], ["notes.Markdown", "generic"]])("posts %s as %s using gh credentials", async (name, type) => {
	const stdin = reply("https://github.com/acme/web/issues/12/artifacts/1\n");
	await expect(publishIssueArtifact({ ...destination, name }, {})).resolves.toBe("https://github.com/acme/web/issues/12/artifacts/1");
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

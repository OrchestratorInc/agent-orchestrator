import { execFile } from "node:child_process";
import { issueArtifactType, type GitHubIssueArtifact, type GitHubIssueArtifactResult, type GitHubIssueChoice } from "../shared/github-issue-artifact";

function issuePath(repo: string) {
	if (typeof repo !== "string" || !/^[a-z\d-]+\/[\w.-]+$/i.test(repo) || /\/\.{1,2}$/.test(repo)) {
		throw new Error("Select a GitHub repository.");
	}
	return `repos/${repo}/issues`;
}

class UploadUnconfirmed extends Error {}

const ambiguousUploadError = /\bHTTP (?:5\d\d|408)\b|\b(?:timeout|timed out|context deadline exceeded|unexpected EOF|connection reset by peer)\b/i;

function ghApi(endpoint: string, env: NodeJS.ProcessEnv, body?: object): Promise<string> {
	const args = ["api", "--hostname", "github.com", "-H", "X-GitHub-Api-Version: 2026-03-10", endpoint];
	if (body) args.push("--method", "POST", "--input", "-", "--jq", ".html_url");
	return new Promise((resolve, reject) => {
		const child = execFile("gh", args, { env, timeout: body ? 120_000 : 30_000, maxBuffer: 4 * 1024 * 1024 }, (error, stdout, stderr) => {
			if (body && error && (error.killed || ambiguousUploadError.test(stderr))) {
				reject(new UploadUnconfirmed());
			} else if (error) reject(new Error(stderr.trim() || "GitHub request failed. Check your GitHub CLI sign-in and repository access."));
			else resolve(stdout);
		});
		child.stdin?.on("error", (error: NodeJS.ErrnoException) => {
			if (error.code !== "EPIPE") reject(error);
		});
		child.stdin?.end(body ? JSON.stringify(body) : undefined);
	});
}

export async function findArtifactIssues(repo: string, query: string, env: NodeJS.ProcessEnv): Promise<GitHubIssueChoice[]> {
	const path = issuePath(repo);
	const term = query.trim();
	const belongsToRepository = (row: { repository_url?: string; pull_request?: unknown }) =>
		!row.pull_request && row.repository_url?.toLowerCase() === `https://api.github.com/repos/${repo.toLowerCase()}`;
	if (/^#?\d+$/.test(term)) {
		const row = JSON.parse(await ghApi(`${path}/${term.replace(/^#/, "")}`, env));
		return belongsToRepository(row) ? [{ number: row.number, title: row.title }] : [];
	}
	const params = new URLSearchParams({ q: `repo:${repo} is:issue ${term}`, sort: "updated", per_page: "20" });
	const data = JSON.parse(await ghApi(`search/issues?${params}`, env));
	// Search qualifiers in a user's query can broaden the result set.
	return data.items.filter(belongsToRepository).map(({ number, title }: GitHubIssueChoice) => ({ number, title }));
}

export async function publishIssueArtifact(input: GitHubIssueArtifact, env: NodeJS.ProcessEnv): Promise<GitHubIssueArtifactResult> {
	const path = issuePath(input.repository);
	const type = issueArtifactType(input.name);
	if (!type || !Number.isSafeInteger(input.issue) || input.issue < 1 || typeof input.body !== "string") {
		throw new Error("Select an issue and an HTML or Markdown file.");
	}
	if (Buffer.byteLength(input.body, "utf8") > 16_777_215) {
		throw new Error("The file exceeds the GitHub artifact limit of 16,777,215 UTF-8 bytes.");
	}
	try {
		const result = await ghApi(`${path}/${input.issue}/artifacts`, env, { type, name: input.name.replace(/[()]/g, ""), body: input.body });
		return { status: "created", url: result.trim() };
	} catch (error) {
		if (error instanceof UploadUnconfirmed) return { status: "unconfirmed", url: `https://github.com/${input.repository}/issues/${input.issue}` };
		throw error;
	}
}

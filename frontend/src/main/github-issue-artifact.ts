import { execFile } from "node:child_process";
import { issueArtifactType, type GitHubIssueArtifact, type GitHubIssueChoice } from "../shared/github-issue-artifact";

function issuePath(repo: string) {
	if (typeof repo !== "string" || !/^[a-z\d-]+\/[\w.-]+$/i.test(repo) || /\/\.{1,2}$/.test(repo)) {
		throw new Error("Select a GitHub repository.");
	}
	return `repos/${repo}/issues`;
}

function ghApi(endpoint: string, env: NodeJS.ProcessEnv, body?: object): Promise<string> {
	const args = ["api", "--hostname", "github.com", "-H", "X-GitHub-Api-Version: 2026-03-10", endpoint];
	if (body) args.push("--method", "POST", "--input", "-", "--jq", ".html_url");
	return new Promise((resolve, reject) => {
		const child = execFile("gh", args, { env, timeout: 30_000, maxBuffer: 4 * 1024 * 1024 }, (error, stdout, stderr) => {
			if (error) reject(new Error(stderr.trim() || "GitHub request failed. Check your GitHub CLI sign-in and repository access."));
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
	if (/^#?\d+$/.test(term)) {
		const row = JSON.parse(await ghApi(`${path}/${term.replace(/^#/, "")}`, env));
		return row.pull_request ? [] : [{ number: row.number, title: row.title }];
	}
	const params = new URLSearchParams({ q: `repo:${repo} is:issue ${term}`, sort: "updated", per_page: "20" });
	const data = JSON.parse(await ghApi(`search/issues?${params}`, env));
	// Search qualifiers in a user's query can broaden the result set.
	return data.items.filter((row: { repository_url: string; pull_request?: unknown }) =>
		!row.pull_request && row.repository_url.toLowerCase() === `https://api.github.com/repos/${repo.toLowerCase()}`,
	).map(({ number, title }: GitHubIssueChoice) => ({ number, title }));
}

export async function publishIssueArtifact(input: GitHubIssueArtifact, env: NodeJS.ProcessEnv): Promise<string> {
	const path = issuePath(input.repository);
	const type = issueArtifactType(input.name);
	if (!type || !Number.isSafeInteger(input.issue) || input.issue < 1 || typeof input.body !== "string") {
		throw new Error("Select an issue and an HTML or Markdown file.");
	}
	const result = await ghApi(`${path}/${input.issue}/artifacts`, env, { type, name: input.name, body: input.body });
	return result.trim();
}

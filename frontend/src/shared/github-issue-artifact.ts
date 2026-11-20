export type GitHubIssueChoice = { number: number; title: string };

export type GitHubIssueArtifactResult = { status: "created" | "unconfirmed"; url: string };

export type GitHubIssueArtifact = {
	repository: string;
	issue: number;
	name: string;
	body: string;
};

export function issueArtifactType(filename: string) {
	if (/\.html?$/i.test(filename)) return "embedded_html";
	if (/\.(md|markdown)$/i.test(filename)) return "generic";
	return undefined;
}

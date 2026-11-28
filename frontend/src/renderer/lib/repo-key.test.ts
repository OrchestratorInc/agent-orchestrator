import { describe, expect, it } from "vitest";
import { repoKey } from "./repo-key";

describe("repoKey", () => {
	it("names one GitHub repository the same however its remote is written", () => {
		for (const url of [
			"git@github.com:Acme/App.git",
			"https://github.com/acme/app",
			"https://github.com/Acme/App.git/",
			"ssh://git@github.com/acme/app",
			// The same repository through www., GitHub's SSH-over-HTTPS host and an explicit port, as AO Cloud names it.
			"https://www.github.com/Acme/App",
			"ssh://git@ssh.github.com:443/acme/app.git",
			"https://github.com:443/acme/app",
			"http://WWW.GitHub.com/acme/app.git",
		]) {
			expect(repoKey(url)).toBe("github.com/acme/app");
		}
	});

	it("drops a port and www. on other hosts too, keeping their case", () => {
		expect(repoKey("ssh://git@www.GitLab.example.com:2222/Group/Repo")).toBe("gitlab.example.com/Group/Repo");
	});

	it("keeps case and nested groups on GitLab", () => {
		expect(repoKey("https://gitlab.example.com/Group/Sub/Repo.git")).toBe("gitlab.example.com/Group/Sub/Repo");
	});

	it("returns nothing for a local path or a malformed URL", () => {
		expect(repoKey("/Users/me/code/app")).toBe("");
		expect(repoKey("https://github.com/acme")).toBe("");
		expect(repoKey("https://github.com/acme/app/tree/main")).toBe("");
	});
});

import { describe, expect, it } from "vitest";
import type { ImportValidation } from "./api";
import { importState, isValidRemoteUrl, nextAction, normalizeGitHubName, orderedActions, preparationFailureMessage, suggestedGitHubName, WORKSPACE_ONLY } from "./importRules";

function validation(over: Partial<ImportValidation> & { required?: ImportValidation["root"]["requiredActions"] } = {}): ImportValidation {
	return {
		isValid: true,
		blockingErrors: [],
		nextStep: "continue",
		root: { repoPath: "/h/app", isRepo: true, hasCommit: true, hasOrigin: true, requiredActions: over.required ?? [], blockingErrors: [] },
		...over,
	};
}

describe("the desktop's import rules", () => {
	it("orders Git setup the desktop's way", () => {
		expect(orderedActions(["create_remote_repository", "git_init", "git_commit"])).toEqual(["git_init", "git_commit", "create_remote_repository"]);
		expect(nextAction(validation({ nextStep: "prepare_git", required: ["git_commit", "git_init"] }))).toBe("git_init");
		expect(nextAction(validation())).toBeUndefined();
	});

	it("turns a validation into what the form can do", () => {
		expect(importState(validation())).toEqual({ kind: "ready" });
		expect(importState(validation({ nextStep: "prepare_git", required: ["create_remote_repository"] }))).toEqual({ kind: "setup", actions: ["create_remote_repository"] });
		expect(importState(validation({ isValid: false, nextStep: "error", blockingErrors: ["DETACHED_HEAD", "IMPORT_PATH_UNSAFE"] }))).toEqual({
			kind: "blocked",
			message: "This repository is checked out in detached HEAD state. Check out a named branch before importing it. Choose a specific project folder outside Orchestrator's own state directories.",
		});
		expect(importState(validation({ nextStep: "choose_import_kind" }))).toEqual({ kind: "blocked", message: WORKSPACE_ONLY });
	});

	it("accepts the remotes the desktop accepts", () => {
		for (const ok of ["https://github.com/o/r.git", "git@github.com:o/r.git", "ssh://git@host/o/r", "file:///srv/r.git"]) expect(isValidRemoteUrl(ok)).toBe(true);
		for (const bad of ["", "github.com/o/r", "-oProxyCommand=x", "https://github.com", "https://x/a b"]) expect(isValidRemoteUrl(bad)).toBe(false);
	});

	it("names GitHub repositories the desktop's way", () => {
		expect(normalizeGitHubName(" my app.git ")).toBe("my-app");
		expect(suggestedGitHubName("/Users/me/notes app")).toBe("notes-app");
		expect(suggestedGitHubName("")).toBe("repository");
	});

	it("says which step failed and why", () => {
		expect(preparationFailureMessage({ action: "create_remote_repository", repoPath: "/h/app", state: "error", error: "gh: not logged in" })).toBe("Create remote repository failed: gh: not logged in");
		expect(preparationFailureMessage({ action: "git_init", repoPath: "/h/app", state: "error" })).toBe("Git initialization failed.");
	});
});

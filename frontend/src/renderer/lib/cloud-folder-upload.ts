// A local folder becomes a cloud workspace's repository in two steps: the
// control plane makes an empty private GitHub repository for it
// (createGitHubRepository), then the main process pushes the folder there
// (aoBridge.cloud.pushFolderToRepository), holding the token itself. A folder
// whose origin is a repository the org's GitHub App already grants is used as
// that repository instead of being uploaded again.

import type { PushFolderResult } from "../../preload";
import { aoBridge } from "./bridge";
import type { CloudCpClient, CloudCpGitHubAppRepository } from "./cloud-cp";
import { canonicalGitHubUrl } from "./cloud-project-repos";

/** A repository the control plane made for a folder, and where to push it. */
export type CreatedFolderRepository = {
	repository: CloudCpGitHubAppRepository;
	pushPath: string;
};

/** owner/name of a github.com remote (https, ssh, or scp-like), or undefined. */
export function gitHubFullNameFromRemote(remote: string): string | undefined {
	const value = remote.trim();
	const match = /^(?:https:\/\/(?:[^@/]+@)?(?:www\.)?github\.com\/|ssh:\/\/git@github\.com(?::\d+)?\/|git@github\.com:)([A-Za-z0-9-]+)\/([A-Za-z0-9._-]+?)(?:\.git)?\/?$/i.exec(value);
	return match ? `${match[1]}/${match[2]}` : undefined;
}

/** The granted repository a folder's origin names, if the org's GitHub App grants it. */
export function grantedRepositoryForRemote(remote: string, granted: readonly CloudCpGitHubAppRepository[]): CloudCpGitHubAppRepository | undefined {
	const fullName = gitHubFullNameFromRemote(remote);
	if (!fullName) return undefined;
	return granted.find((repo) => repo.fullName.toLowerCase() === fullName.toLowerCase());
}

/** The account a repository belongs to: "acme" for acme/widgets. */
export function repositoryOwner(fullName: string): string {
	return fullName.split("/")[0] ?? "";
}

export async function createFolderRepository(
	client: Pick<CloudCpClient, "createGitHubRepository">,
	orgId: string,
	input: { name: string; owner?: string },
): Promise<CreatedFolderRepository> {
	const { repository, pushPath } = await client.createGitHubRepository(orgId, {
		name: input.name,
		...(input.owner ? { owner: input.owner } : {}),
		description: "Uploaded from AO",
	});
	return { repository, pushPath };
}

/** A folder upload the main process refused or that failed, with its reason. */
export class FolderPushError extends Error {
	constructor(readonly code: Extract<PushFolderResult, { status: "error" }>["code"], message: string) {
		super(message);
		this.name = "FolderPushError";
	}
}

/** Pushes the folder into its new repository's default branch; throws a FolderPushError. */
export async function pushFolderToCreatedRepository(folder: string, created: CreatedFolderRepository): Promise<void> {
	const result = await aoBridge.cloud.pushFolderToRepository({
		folder,
		pushPath: created.pushPath,
		branch: created.repository.defaultBranch || "main",
	});
	if (result.status !== "pushed") throw new FolderPushError(result.code, result.message);
}

export function createdRepositoryUrl(created: CreatedFolderRepository): string {
	return canonicalGitHubUrl(created.repository.fullName);
}

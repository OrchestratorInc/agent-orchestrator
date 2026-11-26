import type { ChatFileReference } from "../../shared/chat-file-reference";
import type { FilesSource } from "../hooks/useSessionWorkspaceFiles";

/** Project a supported view into the existing durable reference contract. */
export function fileReferenceSource(source?: FilesSource, commitSha?: string): { source: ChatFileReference["source"]; commitSha?: string } | undefined {
	if (!source) return { source: { kind: "workspace" }, commitSha };
	if (source.kind !== "snapshot") return { source, commitSha };
	const shas = source.commitShas;
	if (shas?.length !== 1 || source.view?.branch || !/^(?:[a-f0-9]{40}|[a-f0-9]{64})$/i.test(shas[0])) return undefined;
	return {
		commitSha: shas[0],
		source: source.view?.prNumber
			? { kind: "pull_request", number: source.view.prNumber, url: source.view.sourceUrl, snapshot: source.view.expectedTipSha }
			: { kind: "workspace" },
	};
}

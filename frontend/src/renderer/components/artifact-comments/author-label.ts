import { useTranslation } from "react-i18next";
import type { ArtifactCommentAuthor } from "../../../shared/artifact-comments";

/**
 * A commenter as the panel names them, by the contract's rule
 * (artifactCommentAuthorLabel) in the app's language: their display name,
 * "Orchestrator user" without one, "Former user" once the account is gone.
 */
export function useArtifactCommentAuthorLabel(): (author: ArtifactCommentAuthor | null) => string {
	const { t } = useTranslation();
	return (author) => {
		if (author === null) return t("artifactComments.formerUser");
		return author.name.trim() || t("artifactComments.orchestratorUser");
	};
}

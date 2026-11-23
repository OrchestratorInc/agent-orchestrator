import { useTranslation } from "react-i18next";
import type { ArtifactCommentAuthor } from "../../../shared/artifact-comments";

/**
 * A commenter as the panel names them. The server fills the name; when it
 * is empty this follows the contract's rule (artifactCommentAuthorLabel) in
 * the app's language: "Former user" once the account is gone (id null, or a
 * resolver of null), otherwise "Orchestrator user".
 */
export function useArtifactCommentAuthorLabel(): (author: ArtifactCommentAuthor | null) => string {
	const { t } = useTranslation();
	return (author) => {
		const name = author?.name.trim() ?? "";
		if (name) return name;
		return !author || author.id === null ? t("artifactComments.formerUser") : t("artifactComments.orchestratorUser");
	};
}

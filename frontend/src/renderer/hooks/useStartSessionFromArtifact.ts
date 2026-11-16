import { useCallback } from "react";
import { useTranslation } from "react-i18next";
import { apiClient } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { newChatDraftScope, readChatSessionDraft, writeChatComposerText } from "../lib/chat-drafts";
import type { ArtifactRow } from "../lib/artifacts";
import { useUiStore } from "../stores/ui-store";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";

/** Joins an artifact's path, always with "/", to its directory in that directory's own style. */
export function artifactFilePath(dir: string, path: string): string {
	const windows = dir.includes("\\") && !dir.includes("/");
	const trimmed = dir.replace(/[\\/]+$/, "");
	return `${trimmed}${windows ? "\\" : "/"}${windows ? path.replaceAll("/", "\\") : path}`;
}

/**
 * Starts a new session from a kept artifact: the new chat page opens on the
 * artifact's project, or on no project once it has none, with the file's
 * path above anything already typed. The person writes the rest and sends it.
 */
export function useStartSessionFromArtifact() {
	const { t } = useTranslation();
	return useCallback(async (row: Pick<ArtifactRow, "sessionId" | "path" | "projectId">) => {
		const ui = useUiStore.getState();
		try {
			const { data, error } = await apiClient.GET("/api/v1/desktop/sessions/{sessionId}/artifacts", {
				params: { path: { sessionId: row.sessionId } },
			});
			if (error) throw apiErrorException(error, t("artifacts.startSessionFailed"));
			if (!data?.artifactDir) throw new Error(t("artifacts.startSessionFailed"));
			const prompt = t("artifacts.startSessionPrompt", { path: artifactFilePath(data.artifactDir, row.path) });
			const scope = newChatDraftScope();
			const draft = readChatSessionDraft(scope).composer.text;
			if (!draft.includes(prompt)) {
				const written = writeChatComposerText(scope, draft.trim() ? `${prompt}\n\n${draft}` : prompt);
				if (!written.ok) throw new Error(t("artifacts.startSessionFailed"));
			}
			useUiStore.setState((state) => ({ newChatDraftRevision: state.newChatDraftRevision + 1 }));
			ui.requestNewTask(row.projectId ?? STANDALONE_WORKSPACE_ID);
		} catch (error) {
			ui.showGlobalToast(error instanceof Error ? error.message : t("artifacts.startSessionFailed"), undefined, "error");
		}
	}, [t]);
}

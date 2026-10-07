import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { formatFileAnnotationMessages } from "../../shared/file-annotations";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { fileAnnotationKey, type ActiveFileAnnotationTarget, type FileAnnotationModel, type FileAnnotationStatus } from "../components/WorkspaceDiffView";

type AnnotationEntry = { target: ActiveFileAnnotationTarget; draft: string };

type UseFileAnnotationOptions = {
	hostId?: string;
	source?: string;
	sendMessage?: (message: string) => Promise<void>;
};

export function useFileAnnotation(sessionId: string, options: UseFileAnnotationOptions = {}): FileAnnotationModel {
	const { hostId, source, sendMessage } = options;
	const { t } = useTranslation();
	// The ref is the source of truth so a draft handed over on blur is already
	// there when the click that caused the blur opens or sends a comment.
	const entriesRef = useRef<AnnotationEntry[]>([]);
	const [entries, setEntries] = useState<AnnotationEntry[]>([]);
	const [status, setStatus] = useState<FileAnnotationStatus>("idle");
	const statusRef = useRef<FileAnnotationStatus>("idle");
	const [error, setError] = useState("");
	const generationRef = useRef(0);
	const sentTimerRef = useRef<number | null>(null);

	const commitEntries = (next: AnnotationEntry[]) => {
		entriesRef.current = next;
		setEntries(next);
	};
	const commitStatus = (next: FileAnnotationStatus, nextError = "") => {
		statusRef.current = next;
		setStatus(next);
		setError(nextError);
	};
	const clearSentTimer = () => {
		if (sentTimerRef.current !== null) window.clearTimeout(sentTimerRef.current);
		sentTimerRef.current = null;
	};
	const reset = () => {
		generationRef.current += 1;
		clearSentTimer();
		commitEntries([]);
		commitStatus("idle");
	};

	useEffect(() => {
		reset();
	}, [hostId, sessionId, source]);
	useEffect(() => clearSentTimer, []);

	const begin = (nextTarget: ActiveFileAnnotationTarget) => {
		// The comments in flight keep their boxes until the send settles.
		if (statusRef.current === "sending") return;
		const key = fileAnnotationKey(nextTarget);
		// A finished send leaves its boxes up for a moment; a new comment replaces them.
		const current = statusRef.current === "sent" ? [] : entriesRef.current;
		generationRef.current += 1;
		clearSentTimer();
		commitStatus("idle");
		if (current.some((entry) => fileAnnotationKey(entry.target) === key)) {
			commitEntries(current.filter((entry) => fileAnnotationKey(entry.target) !== key));
			return;
		}
		// Boxes left empty close, so only comments with text stay open alongside the new one.
		commitEntries([...current.filter((entry) => entry.draft.trim()), { target: { ...nextTarget, source }, draft: "" }]);
	};
	const setDraft = (target: ActiveFileAnnotationTarget, draft: string) => {
		const key = fileAnnotationKey(target);
		const current = entriesRef.current;
		if (!current.some((entry) => fileAnnotationKey(entry.target) === key && entry.draft !== draft)) return;
		commitEntries(current.map((entry) => (fileAnnotationKey(entry.target) === key ? { ...entry, draft } : entry)));
	};
	const cancel = (target?: ActiveFileAnnotationTarget) => {
		if (!target) {
			reset();
			return;
		}
		if (statusRef.current === "sending") return;
		const key = fileAnnotationKey(target);
		const next = entriesRef.current.filter((entry) => fileAnnotationKey(entry.target) !== key);
		if (next.length === entriesRef.current.length) return;
		if (next.length === 0 || statusRef.current === "sent") {
			reset();
			return;
		}
		commitEntries(next);
		commitStatus("idle");
	};
	const send = async (message: string) => {
		if (sendMessage) {
			await sendMessage(message);
			return;
		}
		const { error: responseError } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/send", {
			params: { path: { sessionId } },
			body: { message, userAuthored: true },
		});
		if (responseError) throw new Error(apiErrorMessage(responseError, t("files.feedbackError")));
	};
	const submit = async (target?: ActiveFileAnnotationTarget, text?: string) => {
		if (statusRef.current === "sending" || statusRef.current === "sent") return;
		if (target && text !== undefined) setDraft(target, text);
		const pending = entriesRef.current.filter((entry) => entry.draft.trim());
		if (pending.length === 0) return;
		generationRef.current += 1;
		const generation = generationRef.current;
		commitStatus("sending");
		const messages = formatFileAnnotationMessages(pending.map((entry) => ({ target: entry.target, feedback: entry.draft })));
		let delivered = 0;
		try {
			for (const { message, count } of messages) {
				await send(message);
				delivered += count;
			}
			if (generation !== generationRef.current) return;
			commitStatus("sent");
			sentTimerRef.current = window.setTimeout(() => {
				sentTimerRef.current = null;
				reset();
			}, 1_200);
		} catch (submitError) {
			if (generation !== generationRef.current) return;
			// Comments that already reached the agent close; the rest stay to retry.
			const deliveredKeys = new Set(pending.slice(0, delivered).map((entry) => fileAnnotationKey(entry.target)));
			if (deliveredKeys.size > 0) commitEntries(entriesRef.current.filter((entry) => !deliveredKeys.has(fileAnnotationKey(entry.target))));
			commitStatus("error", apiErrorMessage(submitError, t("files.feedbackError")));
		}
	};
	const draftFor = (target: ActiveFileAnnotationTarget) => {
		const key = fileAnnotationKey(target);
		return entriesRef.current.find((entry) => fileAnnotationKey(entry.target) === key)?.draft ?? "";
	};
	// Saving a draft replaces its entry but not its target, so the list the
	// diffs key their open boxes on only changes when a box opens or closes.
	const targetsRef = useRef<ActiveFileAnnotationTarget[]>([]);
	if (targetsRef.current.length !== entries.length || entries.some((entry, index) => entry.target !== targetsRef.current[index])) {
		targetsRef.current = entries.map((entry) => entry.target);
	}

	return { targets: targetsRef.current, status, error, begin, draftFor, setDraft, cancel, submit };
}

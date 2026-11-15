import { apiErrorException } from "../lib/api-error-exception";
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
	const entriesRef = useRef<AnnotationEntry[]>([]);
	const [entries, setEntries] = useState<AnnotationEntry[]>([]);
	const [status, setStatus] = useState<FileAnnotationStatus>("idle");
	const statusRef = useRef<FileAnnotationStatus>("idle");
	const [error, setError] = useState("");
	const generationRef = useRef(0);
	const activeKeysRef = useRef(new Set<string>());
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
		activeKeysRef.current = new Set();
		commitEntries([]);
		commitStatus("idle");
	};

	useEffect(() => {
		reset();
	}, [hostId, sessionId, source]);
	useEffect(() => clearSentTimer, []);

	const settleSent = () => {
		if (statusRef.current !== "sent") return;
		clearSentTimer();
		const sentKeys = activeKeysRef.current;
		activeKeysRef.current = new Set();
		commitEntries(entriesRef.current.filter((entry) => !sentKeys.has(fileAnnotationKey(entry.target))));
		commitStatus("idle");
	};

	const begin = (nextTarget: ActiveFileAnnotationTarget) => {
		if (statusRef.current === "sending") return;
		settleSent();
		const key = fileAnnotationKey(nextTarget);
		const current = entriesRef.current;
		activeKeysRef.current = new Set();
		commitStatus("idle");
		if (current.some((entry) => fileAnnotationKey(entry.target) === key)) {
			commitEntries(current.filter((entry) => fileAnnotationKey(entry.target) !== key));
			return;
		}
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
		settleSent();
		const key = fileAnnotationKey(target);
		const next = entriesRef.current.filter((entry) => fileAnnotationKey(entry.target) !== key);
		if (next.length === entriesRef.current.length) return;
		commitEntries(next);
		activeKeysRef.current = new Set();
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
		if (responseError) throw apiErrorException(responseError, t("files.feedbackError"));
	};
	const submit = async (target?: ActiveFileAnnotationTarget, text?: string) => {
		if (statusRef.current === "sending") return;
		settleSent();
		if (target && text !== undefined) setDraft(target, text);
		const targetKey = target ? fileAnnotationKey(target) : null;
		const pending = entriesRef.current.filter((entry) => entry.draft.trim() && (targetKey === null || fileAnnotationKey(entry.target) === targetKey));
		if (pending.length === 0) return;
		generationRef.current += 1;
		const generation = generationRef.current;
		activeKeysRef.current = new Set(pending.map((entry) => fileAnnotationKey(entry.target)));
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
				settleSent();
			}, 1_200);
		} catch (submitError) {
			if (generation !== generationRef.current) return;
			const deliveredKeys = new Set(pending.slice(0, delivered).map((entry) => fileAnnotationKey(entry.target)));
			if (deliveredKeys.size > 0) commitEntries(entriesRef.current.filter((entry) => !deliveredKeys.has(fileAnnotationKey(entry.target))));
			activeKeysRef.current = new Set([...activeKeysRef.current].filter((key) => !deliveredKeys.has(key)));
			commitStatus("error", apiErrorMessage(submitError, t("files.feedbackError")));
		}
	};
	const statusFor = (target: ActiveFileAnnotationTarget): FileAnnotationStatus =>
		activeKeysRef.current.has(fileAnnotationKey(target)) ? statusRef.current : "idle";
	const draftFor = (target: ActiveFileAnnotationTarget) => {
		const key = fileAnnotationKey(target);
		return entriesRef.current.find((entry) => fileAnnotationKey(entry.target) === key)?.draft ?? "";
	};
	const targetsRef = useRef<ActiveFileAnnotationTarget[]>([]);
	if (targetsRef.current.length !== entries.length || entries.some((entry, index) => entry.target !== targetsRef.current[index])) {
		targetsRef.current = entries.map((entry) => entry.target);
	}

	return { targets: targetsRef.current, status, statusFor, error, begin, draftFor, setDraft, cancel, submit };
}

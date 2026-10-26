import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { clientForSessionHost } from "../lib/host-clients";
import { toActivity } from "./useConversation";
import type { ConversationActivity } from "../types/conversation";

type DetailSource = {
	key: string;
	load: (activity: ConversationActivity) => Promise<ConversationActivity>;
};
const ActivityDetailSource = createContext<DetailSource | undefined>(undefined);

/** Cache belongs to the mounted chat, including its host; revisions never alias. */
export function ActivityDetailProvider({ sessionId, hostId, children }: {
	sessionId: string;
	hostId?: string;
	children: ReactNode;
}) {
	const source = useMemo<DetailSource>(() => {
		const cache = new Map<string, Promise<ConversationActivity>>();
		return {
			key: JSON.stringify([hostId, sessionId]),
			load(activity) {
				const key = JSON.stringify([sessionId, activity.id, activity.revision]);
				const existing = cache.get(key);
				if (existing) return existing;
				const request = (async () => {
					const { data, error } = await clientForSessionHost(hostId).GET(
						"/api/v1/sessions/{sessionId}/conversation/activities/{activityId}",
						{ params: { path: { sessionId, activityId: activity.id } } },
					);
					if (error) throw error;
					const full = toActivity(data!);
					if (full.id !== activity.id || full.revision !== activity.revision || full.detailOmitted) {
						throw new Error("Activity changed; refresh the conversation and try again.");
					}
					return full;
				})();
				cache.set(key, request);
				// Bound expanded-body retention; failed requests remain retryable.
				if (cache.size > 64) cache.delete(cache.keys().next().value!);
				void request.catch(() => { if (cache.get(key) === request) cache.delete(key); });
				return request;
			},
		};
	}, [sessionId, hostId]);
	return <ActivityDetailSource.Provider value={source}>{children}</ActivityDetailSource.Provider>;
}

export function useActivityDetail(activity: ConversationActivity, visible: boolean) {
	const source = useContext(ActivityDetailSource);
	const key = JSON.stringify([source?.key, activity.id, activity.revision]);
	const [result, setResult] = useState<{ key: string; activity?: ConversationActivity; error?: boolean }>();
	const [attempt, setAttempt] = useState(0);
	const full = result?.key === key ? result.activity : undefined;
	const error = result?.key === key && result.error;
	useEffect(() => {
		if (!visible || !activity.detailOmitted || !source) return;
		let active = true;
		setResult({ key });
		void source.load(activity).then(
			(row) => { if (active) setResult({ key, activity: row }); },
			() => { if (active) setResult({ key, error: true }); },
		);
		return () => { active = false; };
	}, [key, visible, activity.detailOmitted, source, attempt]);
	return {
		activity: full ?? activity,
		pending: Boolean(activity.detailOmitted && !full),
		error: Boolean(error || (activity.detailOmitted && !source)),
		retry: () => setAttempt((value) => value + 1),
	};
}

export function ActivityDetailStatus({ error, retry }: { error: boolean; retry: () => void }) {
	const { t } = useTranslation();
	return (
		<p className="px-2 py-1 text-caption text-muted-foreground" role="status">
			{error ? <>{t("chat.activity.loadFailed")} <button type="button" onClick={retry}>{t("chat.activity.retry")}</button></> : t("chat.activity.loading")}
		</p>
	);
}

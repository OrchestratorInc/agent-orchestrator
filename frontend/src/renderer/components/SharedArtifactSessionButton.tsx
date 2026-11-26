import { MessageSquarePlus } from "@/components/app-icons";
import { useQuery } from "@tanstack/react-query";
import { useCallback, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { SharedArtifactSessionKey } from "../../preload";
import { useCloudCp } from "../hooks/useCloudCp";
import { useRestoreSession } from "../hooks/useRestoreSession";
import { apiClient, apiErrorCode } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { aoBridge } from "../lib/bridge";
import { newChatDraftScope, readChatSessionDraft, writeChatComposerText } from "../lib/chat-drafts";
import { CloudCpError } from "../lib/cloud-cp";
import { LOCAL_HOST } from "../lib/hosts";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { checkForUpdate, nextSharedArtifactStep, type LinkedSession, type SharedArtifactSessionDeps, type SharedArtifactSessionStep } from "../lib/shared-artifact-session";
import { dropSharedArtifactDraft, rememberSharedArtifactPrompt } from "../lib/shared-artifact-draft";
import { useSharedArtifactDraftStore } from "../stores/shared-artifact-draft-store";
import { useUiStore } from "../stores/ui-store";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { ConfirmDialog } from "./ConfirmDialog";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

/** The latest "Start a session with this" draft request, across every preview. */
let latestDraftRequest = 0;

type Dialog =Extract<SharedArtifactSessionStep, { kind: "archived" } | { kind: "update" }>;

async function linkedSession(sessionId: string): Promise<LinkedSession> {
	const { data, error, response } = await apiClient.GET("/api/v1/sessions/{sessionId}", { params: { path: { sessionId } } });
	if (response?.status === 404) return { exists: false };
	if (error || !data) throw apiErrorException(error);
	return { exists: true, projectId: data.session.projectId ?? "", terminated: data.session.isTerminated, busy: data.session.status === "working" };
}

/**
 * "Start a session with this" for a link someone shared with the person. The
 * first time it copies the artifact into a new standalone draft; the session
 * that draft starts is remembered, so later it opens that session again,
 * offering to unarchive it, or to bring in the owner's newer version.
 */
export function SharedArtifactSessionButton({ artifact, onClose }: { artifact: { linkId: string; name: string }; onClose: () => void }) {
	const { t } = useTranslation();
	const { client, ready, baseUrl, userId } = useCloudCp();
	const navigateToSession = useNavigateToSession();
	const restoreSession = useRestoreSession();
	const [busy, setBusy] = useState(false);
	const [dialog, setDialog] = useState<Dialog | null>(null);
	const running = useRef(false);
	// The same view the frame shows: the link's own owner opens their original instead.
	const view = useQuery({
		queryKey: ["shared-artifact-view", baseUrl, userId, artifact.linkId],
		enabled: ready,
		staleTime: 5 * 60_000,
		gcTime: 0,
		retry: (count, error) => !(error instanceof CloudCpError && error.status === 404) && count < 1,
		queryFn: ({ signal }) => client.viewArtifactShare(artifact.linkId, { signal }),
	});
	const key: SharedArtifactSessionKey = useMemo(() => ({ baseUrl, userId, linkId: artifact.linkId }), [baseUrl, userId, artifact.linkId]);
	const deps: SharedArtifactSessionDeps = useMemo(() => ({
		getRecord: (k) => aoBridge.sharedArtifact.getSession(k),
		setRecord: (k, record) => aoBridge.sharedArtifact.setSession(k, record),
		removeRecord: (k) => aoBridge.sharedArtifact.removeSession(k),
		getSession: linkedSession,
		view: (linkId) => client.viewArtifactShare(linkId),
		copy: (input) => aoBridge.sharedArtifact.copy(input),
	}), [client]);

	const fail = useCallback((message?: string) => {
		useUiStore.getState().showGlobalToast(message ?? t("artifacts.startSessionFailed"), undefined, "error");
	}, [t]);

	const openSession = useCallback((projectId: string, sessionId: string, entry: string) => {
		onClose();
		useUiStore.getState().requestWorkspaceFileOpen(sessionId, entry, undefined, "artifact");
		navigateToSession(projectId || undefined, sessionId, LOCAL_HOST);
	}, [navigateToSession, onClose]);

	// Page files the link did not serve: the copy went ahead without them.
	const reportMissing = useCallback((missing: string[]) => {
		if (missing.length > 0) useUiStore.getState().showGlobalToast(t("artifacts.shared.missingFiles", { files: missing.slice(0, 5).join(", ") }), undefined, "info");
	}, [t]);

	const startDraft = useCallback(async () => {
		// Only the latest request, across every preview, may set the draft: a slow
		// copy that finishes after a newer one is dropped.
		const request = ++latestDraftRequest;
		// A draft already holding this link's copy, not sent yet, is opened again as it is.
		const held = useSharedArtifactDraftStore.getState().pending;
		let pending = held && JSON.stringify(held.key) === JSON.stringify(key) ? held : null;
		if (!pending) {
			let share;
			try {
				share = await client.viewArtifactShare(artifact.linkId);
			} catch (error) {
				fail(error instanceof CloudCpError && error.status === 404 ? t("artifacts.sharedUnavailable") : undefined);
				return;
			}
			// The owner works on the original, never a copy of their own link.
			if (share.you?.owner) {
				fail();
				return;
			}
			const result = await aoBridge.sharedArtifact.copy({ contentUrl: share.contentUrl, name: share.name || artifact.name });
			if (request !== latestDraftRequest) return;
			if (!result.ok) {
				fail(result.code === "too_large" ? t("artifacts.shared.tooLarge") : undefined);
				return;
			}
			// Only now, with this copy in hand, does another link's copy (and the
			// prompt naming it) give way: a failed copy leaves that draft alone.
			dropSharedArtifactDraft();
			const copy = result.copy;
			pending = { key, name: share.name || artifact.name, updatedAt: share.updatedAt, copy, prompt: t("artifacts.shared.startSessionPrompt", { path: copy.entry }) };
			useSharedArtifactDraftStore.getState().setPending(pending);
		}
		// The prompt as first written, so a change of language since does not add a second one.
		const { copy, prompt } = pending;
		const scope = newChatDraftScope();
		const draft = readChatSessionDraft(scope).composer.text;
		if (!draft.includes(prompt) && !writeChatComposerText(scope, draft.trim() ? `${prompt}\n\n${draft}` : prompt).ok) {
			fail();
			return;
		}
		rememberSharedArtifactPrompt(prompt);
		onClose();
		useUiStore.setState((state) => ({ newChatDraftRevision: state.newChatDraftRevision + 1 }));
		useUiStore.getState().requestNewTask(STANDALONE_WORKSPACE_ID);
		reportMissing(copy.missing);
	}, [artifact.linkId, artifact.name, client, fail, key, onClose, reportMissing, t]);

	const act = useCallback(async (work: () => Promise<void>) => {
		if (running.current) return;
		running.current = true;
		setBusy(true);
		try {
			await work();
		} catch {
			fail();
		} finally {
			running.current = false;
			setBusy(false);
		}
	}, [fail]);

	const follow = useCallback(async (step: SharedArtifactSessionStep) => {
		if (step.kind === "draft") await startDraft();
		else if (step.kind === "open") openSession(step.projectId, step.sessionId, step.entry);
		else setDialog(step);
	}, [openSession, startDraft]);

	const start = () => {
		if (!ready) {
			fail(t("artifacts.sharedSignIn"));
			return;
		}
		void act(async () => follow(await nextSharedArtifactStep(deps, key, artifact.name)));
	};

	const unarchive = (record: Extract<Dialog, { kind: "archived" }>["record"]) => void act(async () => {
		const restored = await restoreSession(record.sessionId);
		if (restored.status !== "success") {
			setDialog(null);
			fail(t("artifacts.shared.unarchiveFailed"));
			return;
		}
		setDialog(null);
		const session = await linkedSession(record.sessionId);
		const projectId = session.exists ? session.projectId : "";
		await follow(await checkForUpdate(deps, key, artifact.name, record, { projectId, busy: session.exists && session.busy }));
	});

	const continueWithNewDraft = () => void act(async () => {
		setDialog(null);
		await startDraft();
	});

	const updateCopy = (step: Extract<Dialog, { kind: "update" }>) => void act(async () => {
		const session = await linkedSession(step.sessionId);
		if (session.exists && session.busy) {
			fail(t("artifacts.shared.sessionBusy"));
			return;
		}
		const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/artifact-imports", {
			params: { path: { sessionId: step.sessionId } },
			body: { entry: step.copy.entry, files: step.copy.files },
		});
		if (error) {
			// The daemon refuses while the agent works, and when a file changed under the update.
			const code = apiErrorCode(error);
			if (code === "ARTIFACT_IMPORT_INCOMPLETE") {
				// Some files are new and some old; the originals are kept beside them, hidden.
				console.warn("AO: shared artifact update left incomplete", error);
				setDialog(null);
				fail(t("artifacts.shared.updateIncomplete"));
				return;
			}
			fail(code === "SESSION_BUSY" || code === "ARTIFACT_FILE_CHANGED" || code === "ARTIFACT_FILE_STALE" ? t("artifacts.shared.sessionBusy") : t("artifacts.shared.updateFailed"));
			return;
		}
		// The copy is updated either way: a record that fails to save only means the next click checks again.
		await deps.setRecord(key, { sessionId: step.sessionId, dir: step.copy.dir, entry: step.copy.entry, updatedAt: step.updatedAt, fingerprint: step.copy.fingerprint }).catch(() => undefined);
		setDialog(null);
		openSession(step.projectId, step.sessionId, step.copy.entry);
		reportMissing(step.copy.missing);
	});

	const keepMine = (step: Extract<Dialog, { kind: "update" }>) => void act(async () => {
		// The owner's date moves too, so later clicks open the session without
		// downloading the declined version again.
		await deps.setRecord(key, { ...step.record, updatedAt: step.updatedAt, declinedFingerprint: step.copy.fingerprint }).catch(() => undefined);
		setDialog(null);
		openSession(step.projectId, step.sessionId, step.record.entry);
	});

	// The link's owner has the original; there is nothing to copy. Shown only
	// once the view says who is looking, so the owner never sees it meanwhile.
	// When the view can't load (offline, no access) it shows anyway: it still
	// opens a session already linked to the link, and a new copy fails plainly.
	if (view.data?.you?.owner || (!view.data && !view.isError)) return null;

	return (
		<>
			<Tooltip>
				<TooltipTrigger asChild>
					<Button aria-label={t("artifacts.startSession")} aria-busy={busy || undefined} className="shrink-0" disabled={busy} onClick={start} size="icon-sm" type="button" variant="quiet">
						<MessageSquarePlus aria-hidden="true" className="size-icon-sm" />
					</Button>
				</TooltipTrigger>
				<TooltipContent side="bottom">{t("artifacts.startSession")}</TooltipContent>
			</Tooltip>
			<ConfirmDialog
				open={dialog?.kind === "archived"}
				title={t("artifacts.shared.archivedTitle")}
				description={t("artifacts.shared.archivedBody")}
				confirmLabel={t("artifacts.shared.unarchive")}
				cancelLabel={t("artifacts.shared.continue")}
				busy={busy}
				onConfirm={() => { if (dialog?.kind === "archived") unarchive(dialog.record); }}
				onCancel={continueWithNewDraft}
				onOpenChange={(open) => { if (!open && !busy) setDialog(null); }}
			/>
			<ConfirmDialog
				open={dialog?.kind === "update"}
				title={t("artifacts.shared.updateTitle")}
				description={t("artifacts.shared.updateBody")}
				confirmLabel={t("artifacts.shared.updateCopy")}
				cancelLabel={t("artifacts.shared.keepMine")}
				busy={busy}
				onConfirm={() => { if (dialog?.kind === "update") updateCopy(dialog); }}
				onCancel={() => { if (dialog?.kind === "update") keepMine(dialog); }}
				onOpenChange={(open) => { if (!open && !busy) setDialog(null); }}
			/>
		</>
	);
}

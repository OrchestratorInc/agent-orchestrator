import { createContext, useContext, useEffect, useRef, useState } from "react";
import { QueryClientContext, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { FILE_REFERENCE_RESOLVE_UNSUPPORTED, type ChatFileReference, type FileAnnotationResolution, type OpenFileAnnotation } from "../../../shared/chat-file-reference";
import { CapturedFileAnnotation } from "./CapturedFileAnnotation";
import { Button } from "../ui/button";
import { clientForSessionHost } from "../../lib/host-clients";
import { subscribeWorkspaceFileChanges } from "../../lib/workspace-file-events";

export type FileAnnotationContextValue = {
	sessionId: string;
	hostId?: string;
	onOpen?: OpenFileAnnotation;
	generation?: { current: number };
	/**
	 * Resolves a reference somewhere other than the session's daemon (a Cloud
	 * session's control plane). Its caller refreshes `queryKey` when the
	 * workspace changes. Throwing FILE_REFERENCE_RESOLVE_UNSUPPORTED shows the
	 * capture alone, as without a resolver.
	 */
	resolver?: {
		queryKey: readonly unknown[];
		resolve: (reference: ChatFileReference, signal?: AbortSignal) => Promise<FileAnnotationResolution>;
	};
};

export const FileAnnotationContext = createContext<FileAnnotationContextValue | undefined>(undefined);

export function FileAnnotationPreview({ reference, index, onNavigate }: { reference: ChatFileReference; index?: number; onNavigate?: () => void }) {
	const context = useContext(FileAnnotationContext);
	const queryClient = useContext(QueryClientContext);
	if (context && queryClient) return <ResolvedPreview reference={reference} index={index} context={context} onNavigate={onNavigate} />;
	return <CapturedPreview reference={reference} index={index} />;
}
type FileAnnotationStatus =
 | "chat.fileAnnotationInvalid"
 | "chat.fileAnnotationCheckFailed"
 | "chat.fileAnnotationChanged"
 | "chat.fileAnnotationUnavailable"
 | "chat.fileAnnotationMoved"
 | "chat.fileAnnotationAmbiguous"
 | "chat.fileAnnotationTruncated";

function CapturedPreview({ reference, index, status, onOpen, opening }: { reference: ChatFileReference; index?: number; status?: FileAnnotationStatus; onOpen?: () => void; opening?: boolean }) {
	const { t } = useTranslation();
	return (
		<CapturedFileAnnotation reference={reference} index={index} onOpen={onOpen} opening={opening}>
			{status ? (
				<span className="block text-muted-foreground" role="status">
					{t(status)} {t("chat.fileAnnotationCapturedPreview")}
				</span>
			) : null}
		</CapturedFileAnnotation>
	);
}
function ResolvedPreview({
	reference,
	index,
	context,
	onNavigate,
}: {
	reference: ChatFileReference;
	index?: number;
	context: NonNullable<React.ContextType<typeof FileAnnotationContext>>;
	onNavigate?: () => void;
}) {
	const { t } = useTranslation();
	const mounted = useRef(true);
	const [opening, setOpening] = useState(false);
	useEffect(() => {
		mounted.current = true;
		return () => {
			mounted.current = false;
		};
	}, []);
	const queryClient = useQueryClient();
	const resolver = context.resolver;
	const query = useQuery({
		queryKey: resolver ? [...resolver.queryKey, reference.id]
			: context.hostId
			? ["file-annotation-source", context.hostId, context.sessionId, reference.id]
			: ["file-annotation-source", context.sessionId, reference.id],
		queryFn: async ({ signal }): Promise<FileAnnotationResolution> => {
			if (reference.sessionId !== context.sessionId) throw new Error("CHAT_FILE_REFERENCE_INVALID");
			if (resolver) return resolver.resolve(reference, signal);
			const { data, error } = await clientForSessionHost(context.hostId).POST("/api/v1/sessions/{sessionId}/conversation/file-references/resolve", {
				params: { path: { sessionId: context.sessionId } },
				body: reference,
				signal,
			});
			if (error || !data) throw new Error(error?.code ?? "FILE_REFERENCE_CHECK_FAILED");
			return data;
		},
		retry: false,
		staleTime: 0,
		refetchOnWindowFocus: false,
	});
	useEffect(() => (resolver ? undefined : subscribeWorkspaceFileChanges(context.sessionId, queryClient, context.hostId)), [context.sessionId, context.hostId, queryClient, resolver]);
	if (query.error?.message === FILE_REFERENCE_RESOLVE_UNSUPPORTED) return <CapturedPreview reference={reference} index={index} />;
	const invalid = query.error?.message === "CHAT_FILE_REFERENCE_INVALID";
	const state = query.isError ? undefined : query.data?.state;
	return (
		<>
			<CapturedPreview
				index={index}
				reference={reference}
				opening={opening}
				onOpen={context.onOpen && !reference.truncated && (state === "unchanged" || state === "moved" || state === "changed") ? async () => {
						const generation = context.generation;
						const token = generation ? ++generation.current : 0;
						setOpening(true);
						try {
							const fresh = await query.refetch();
							if (
								!mounted.current ||
								(generation && generation.current !== token) ||
								fresh.error ||
								(fresh.data?.state !== "unchanged" && fresh.data?.state !== "moved" && fresh.data?.state !== "changed")
							)
								return;
							context.onOpen?.(reference, fresh.data);
							onNavigate?.();
						} finally {
							if (mounted.current) setOpening(false);
						}
					} : undefined}
				status={
					query.isError
						? (invalid ? "chat.fileAnnotationInvalid" : "chat.fileAnnotationCheckFailed")
						: state === "moved"
							? "chat.fileAnnotationMoved"
							: query.data?.reason === "ambiguous"
								? "chat.fileAnnotationAmbiguous"
								: query.data?.reason === "truncated"
									? "chat.fileAnnotationTruncated"
									: state === "changed"
										? "chat.fileAnnotationChanged"
										: state === "unavailable"
											? "chat.fileAnnotationUnavailable"
											: undefined
				}
			/>
			{state === "unavailable" || (query.isError && !invalid) ? (
				<Button
					type="button"
					variant="link"
					className="mt-1 text-xs"
					disabled={query.isFetching}
					onClick={() => {
						void query.refetch();
					}}
				>
					{t("chat.fileAnnotationRetry")}
				</Button>
			) : null}
		</>
	);
}

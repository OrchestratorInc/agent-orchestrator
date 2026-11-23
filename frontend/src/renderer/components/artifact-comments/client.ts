import { createContext, useContext, useMemo } from "react";
import { useCloudCp } from "../../hooks/useCloudCp";
import { CloudCpError } from "../../lib/cloud-cp";
import type {
	ArtifactComment,
	ArtifactCommentsSnapshot,
	ArtifactCommentThread,
	ArtifactCommentThreadStatus,
	CreateArtifactCommentThreadRequest,
	ReplyArtifactCommentThreadRequest,
} from "./contract";

/** A comments read: the full snapshot with its ETag, or "no change" (304) for the ETag sent. */
export type ArtifactCommentsListResult =
	| { kind: "unchanged" }
	| { kind: "snapshot"; snapshot: ArtifactCommentsSnapshot; etag: string | null };

type Options = { signal?: AbortSignal };

/**
 * The comment routes the app uses, by link slug. A small seam over the
 * control-plane client so the views don't depend on its exact method shapes
 * while the contract settles, and so tests can hand in a fake.
 */
export interface ArtifactCommentsClient {
	list(slug: string, options?: Options & { version?: number; etag?: string | null }): Promise<ArtifactCommentsListResult>;
	createThread(slug: string, body: CreateArtifactCommentThreadRequest, options?: Options): Promise<ArtifactCommentThread>;
	reply(slug: string, threadId: string, body: ReplyArtifactCommentThreadRequest, options?: Options): Promise<ArtifactComment>;
	setStatus(slug: string, threadId: string, status: ArtifactCommentThreadStatus, options?: Options): Promise<ArtifactCommentThread>;
	edit(slug: string, commentId: string, body: string, options?: Options): Promise<ArtifactComment>;
	remove(slug: string, commentId: string, options?: Options): Promise<void>;
	/** Owner only: records a successful Send to agent on the thread. */
	markSent(slug: string, threadId: string, options?: Options): Promise<ArtifactCommentThread>;
}

/**
 * The control-plane client's comment methods, as the design names them
 * (section 9). PROVISIONAL: typed loosely until lib/cloud-cp/client.ts gains
 * them; then this adapter calls them directly.
 */
type CloudCommentMethods = {
	listArtifactShareComments?: (slug: string, options?: Options & { version?: number; ifNoneMatch?: string }) => Promise<ArtifactCommentsListResult>;
	createArtifactShareThread?: (slug: string, body: CreateArtifactCommentThreadRequest, options?: Options) => Promise<ArtifactCommentThread>;
	replyArtifactShareThread?: (slug: string, threadId: string, body: ReplyArtifactCommentThreadRequest, options?: Options) => Promise<ArtifactComment>;
	setArtifactShareThreadStatus?: (slug: string, threadId: string, body: { status: ArtifactCommentThreadStatus }, options?: Options) => Promise<ArtifactCommentThread>;
	editArtifactShareComment?: (slug: string, commentId: string, body: { body: string }, options?: Options) => Promise<ArtifactComment>;
	deleteArtifactShareComment?: (slug: string, commentId: string, options?: Options) => Promise<void>;
	markArtifactShareThreadSent?: (slug: string, threadId: string, options?: Options) => Promise<ArtifactCommentThread>;
};

function unavailable(): never {
	throw new CloudCpError("Comments aren't available from this control plane yet.", { status: 0, code: "comments_unavailable" });
}

/** The comments client over the signed-in control-plane client. */
export function cloudArtifactCommentsClient(cloud: object): ArtifactCommentsClient {
	const cp = cloud as CloudCommentMethods;
	return {
		list: async (slug, options) => (cp.listArtifactShareComments ?? unavailable)(slug, {
			signal: options?.signal,
			version: options?.version,
			...(options?.etag ? { ifNoneMatch: options.etag } : {}),
		}),
		createThread: async (slug, body, options) => (cp.createArtifactShareThread ?? unavailable)(slug, body, options),
		reply: async (slug, threadId, body, options) => (cp.replyArtifactShareThread ?? unavailable)(slug, threadId, body, options),
		setStatus: async (slug, threadId, status, options) => (cp.setArtifactShareThreadStatus ?? unavailable)(slug, threadId, { status }, options),
		edit: async (slug, commentId, body, options) => (cp.editArtifactShareComment ?? unavailable)(slug, commentId, { body }, options),
		remove: async (slug, commentId, options) => (cp.deleteArtifactShareComment ?? unavailable)(slug, commentId, options),
		markSent: async (slug, threadId, options) => (cp.markArtifactShareThreadSent ?? unavailable)(slug, threadId, options),
	};
}

/** Overrides the comments client below it; tests use this to hand in a fake. */
export const ArtifactCommentsClientContext = createContext<ArtifactCommentsClient | null>(null);

export function useArtifactCommentsClient(): ArtifactCommentsClient {
	const override = useContext(ArtifactCommentsClientContext);
	const { client } = useCloudCp();
	return useMemo(() => override ?? cloudArtifactCommentsClient(client), [client, override]);
}

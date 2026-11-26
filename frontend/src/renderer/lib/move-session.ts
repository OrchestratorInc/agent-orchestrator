import type { components } from "../../api/schema";
import { apiErrorCode, apiErrorMessage } from "./api-client";
import { apiErrorException } from "./api-error-exception";
import { aoBridge } from "./bridge";
import type { clientForSessionHost } from "./host-clients";
import { HOST_UNREACHABLE_CODES, LOCAL_HOST } from "./hosts";

type Client = ReturnType<typeof clientForSessionHost>;
type Transfer = components["schemas"]["SessionTransfer"];
type Destination = { hostId: string; label: string; projectId: string };

export type MoveStep = "prepare" | "copy" | "receive" | "commit";

export type MoveInput = {
	source: { client: Client; hostId: string; sessionId: string; label: string };
	target: { client: Client; hostId: string; label: string; projectId: string };
	transferId?: string;
	/** End a turn in progress instead of refusing to move. */
	stopTurn?: boolean;
	onStep?: (step: MoveStep) => void;
};

export type MoveResult = {
	sessionId: string;
	warnings: string[];
	/** False when the old session could not be marked as moved; retry with commitMove. */
	committed: boolean;
	commitError?: string;
	transferId: string;
	/** The destination as the source records it. */
	destination: Destination;
	/** False when no conversation was copied or it could not be resumed, so the agent started from a summary. */
	resumed: boolean;
};

/** A move that failed before the destination took the session. */
export class MoveError extends Error {
	constructor(message: string, readonly step: MoveStep, readonly resumedSource: boolean, readonly code?: string, readonly transferId?: string) {
		super(message);
		this.name = "MoveError";
	}
}

export function newTransferId(): string {
	return "tr" + crypto.randomUUID().replaceAll("-", "").slice(0, 24);
}

/**
 * Moves a session from one AO daemon to another: prepare on the source, copy
 * its one file of work and conversation, receive on the destination, then mark the source
 * moved. Until the destination accepts the session, a failure cancels the move
 * and resumes the source agent, so nothing is lost. After that, only marking
 * the source is left, which commitMove can retry. Running it again after an
 * interrupted move to the same destination finishes that move: the destination
 * may have started it, so it is asked before anything is copied or cancelled.
 */
export async function moveSession(input: MoveInput): Promise<MoveResult> {
	const { source, target } = input;
	let transferId = input.transferId ?? newTransferId();
	let step: MoveStep = "prepare";
	let sessionId: string;
	let warnings: string[];
	let resumed: boolean;
	input.onStep?.("prepare");
	await checkDestination(target);
	const destination = await recordedDestination(target);
	try {
		const prepared = await reach(source.label, source.client.POST("/api/v1/sessions/{sessionId}/transfer/prepare", {
			params: { path: { sessionId: source.sessionId } },
			// "This computer" means nothing on the destination; empty makes the source daemon use its hostname.
			body: {
				transferId,
				sourceLabel: source.hostId === LOCAL_HOST ? "" : source.label,
				destination: { ...destination, sessionId: "" },
				...(input.stopTurn ? { stopTurn: true } : {}),
			},
		}));
		if (prepared.error || !prepared.data) throw apiErrorException(prepared.error, "Could not prepare the move");
		const transfer: Transfer = prepared.data.transfer;
		// An unfinished earlier move to this destination comes back instead of a new one.
		const earlier = transfer.transferId !== transferId;
		transferId = transfer.transferId;
		step = "receive";
		let received = earlier ? await receiveUnlessNotCopied(target.client, target.projectId, transfer, target.label) : undefined;
		if (!received) {
			if (transfer.archive) {
				step = "copy";
				input.onStep?.("copy");
				await copyArchive(source, target, transferId);
			}
			step = "receive";
			input.onStep?.("receive");
			received = await receive(target.client, target.projectId, transfer, target.label);
		}
		({ sessionId, warnings, resumed } = received);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		const code = error instanceof Error ? apiErrorCode(error.cause) : undefined;
		// The destination may have started the session before it stopped answering.
		// Leave the move pending so Retry finishes it instead of running it twice.
		if (step === "receive" && (error instanceof Unreachable || code === "TRANSFER_UNCONFIRMED")) throw new MoveError(message, step, false, "RECEIVE_UNCONFIRMED", transferId);
		throw new MoveError(message, step, await cancelMove(source, transferId), code, transferId);
	}
	input.onStep?.("commit");
	const commitError = await commitMove(source, destination, transferId, sessionId);
	return { sessionId, warnings, committed: !commitError, commitError, transferId, destination, resumed };
}

/** Refuses a destination whose AO has no receive route, before the source agent stops. */
async function checkDestination(target: MoveInput["target"]): Promise<void> {
	const res = await target.client.POST("/api/v1/sessions/transfer/receive", { body: {} as never }).catch(() => undefined);
	if (apiErrorCode(res?.error) === "TRANSFER_INCOMPLETE") return;
	const message = res && !HOST_UNREACHABLE_CODES.has(apiErrorCode(res.error))
		? `${target.label} runs an AO version that cannot receive sessions; update AO there first`
		: `${target.label} is not reachable`;
	throw new MoveError(message, "prepare", true);
}

/**
 * The source keeps the destination's machine ID and name: "local" and "This
 * computer" would point at whichever computer opens the old session.
 */
async function recordedDestination(target: MoveInput["target"]): Promise<Destination> {
	if (target.hostId !== LOCAL_HOST) return { hostId: target.hostId, label: target.label, projectId: target.projectId };
	const [identity, name] = await Promise.all([
		target.client.GET("/api/v1/identity").catch(() => undefined),
		aoBridge.remotes.computerName().catch(() => ""),
	]);
	if (!identity?.data) throw new MoveError(`${target.label} is not reachable`, "prepare", true);
	return { hostId: identity.data.hostId, label: name || target.label, projectId: target.projectId };
}

/** Forgets the move on the source and resumes its agent. True when the agent runs there again. */
export async function cancelMove(source: MoveInput["source"], transferId: string): Promise<boolean> {
	const path = { params: { path: { sessionId: source.sessionId } } };
	await source.client.POST("/api/v1/sessions/{sessionId}/transfer/cancel", { ...path, body: { transferId } }).catch(() => undefined);
	return source.client.POST("/api/v1/sessions/{sessionId}/resume-agent", path).then(
		// A refusal before the agent stopped leaves it running.
		(res) => !res.error || apiErrorCode(res.error) === "AGENT_NOT_EXITED",
		() => false,
	);
}

class Unreachable extends Error {}

/**
 * The other computer did not answer: fetch rejects with a TypeError, and the app's
 * own proxy to a remote host answers with one of HOST_UNREACHABLE_CODES instead.
 */
async function reach<T extends { error?: unknown }>(label: string, call: Promise<T>): Promise<T> {
	const res = await call.catch((error: unknown): never => {
		throw error instanceof TypeError ? new Unreachable(`${label} is not reachable`) : error;
	});
	if (HOST_UNREACHABLE_CODES.has(apiErrorCode(res.error))) throw new Unreachable(`${label} is not reachable`);
	return res;
}

async function copyArchive(source: MoveInput["source"], target: MoveInput["target"], transferId: string): Promise<void> {
	const archive = await reach(source.label, source.client.GET("/api/v1/sessions/{sessionId}/transfer/{transferId}/archive", {
		params: { path: { sessionId: source.sessionId, transferId } },
		parseAs: "blob",
	}));
	if (archive.error || !archive.data) throw apiErrorException(archive.error, "Could not read the work to move");
	const res = await reach(target.label, target.client.PUT("/api/v1/sessions/transfer/{transferId}/archive", {
		params: { path: { transferId } },
		// A Blob reaches Electron's request hooks by reference; a byte array is copied into the main process.
		body: archive.data as never,
		bodySerializer: (body: unknown) => body as BodyInit,
		headers: { "Content-Type": "application/octet-stream" },
	}));
	if (res.error) throw apiErrorException(res.error, "Could not copy the work to the other computer");
}

/** Receive is idempotent per transfer, so a dropped response is retried once
 *  before the source is resumed; otherwise both computers could run it. */
async function receive(target: Client, projectId: string, transfer: Transfer, label: string): Promise<{ sessionId: string; warnings: string[]; resumed: boolean }> {
	for (let attempt = 0; ; attempt++) {
		try {
			const res = await reach(label, target.POST("/api/v1/sessions/transfer/receive", { body: { projectId, transfer } }));
			// An answer that is not the daemon's own, such as a tunnel's timeout page, says nothing about the session.
			if (!res.data && !apiErrorCode(res.error)) throw new Unreachable(`${label} is not reachable`);
			if (res.error || !res.data) throw apiErrorException(res.error, "The other computer could not start the session");
			return { sessionId: res.data.session.id, warnings: res.data.warnings ?? [], resumed: res.data.resumed ?? false };
		} catch (error) {
			if (attempt >= 1 || !(error instanceof Unreachable)) throw error;
		}
	}
}

/** Undefined when the destination has neither the session nor the copied file. */
async function receiveUnlessNotCopied(...args: Parameters<typeof receive>) {
	try {
		return await receive(...args);
	} catch (error) {
		if (error instanceof Error && apiErrorCode(error.cause) === "TRANSFER_ARCHIVE_INVALID") return undefined;
		throw error;
	}
}

/** Marks the source session as moved. Returns the error message, or undefined. */
export async function commitMove(
	source: MoveInput["source"],
	destination: Destination,
	transferId: string,
	sessionId: string,
): Promise<string | undefined> {
	try {
		const res = await source.client.POST("/api/v1/sessions/{sessionId}/transfer/commit", {
			params: { path: { sessionId: source.sessionId } },
			body: { transferId, destination: { ...destination, sessionId } },
		});
		return res.error ? apiErrorMessage(res.error, "Could not mark the old session as moved") : undefined;
	} catch (error) {
		return error instanceof Error ? error.message : String(error);
	}
}

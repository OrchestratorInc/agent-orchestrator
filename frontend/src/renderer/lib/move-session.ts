import type { components } from "../../api/schema";
import { apiErrorCode, apiErrorMessage } from "./api-client";
import { apiErrorException } from "./api-error-exception";
import type { clientForSessionHost } from "./host-clients";
import { LOCAL_HOST } from "./hosts";

type Client = ReturnType<typeof clientForSessionHost>;
type Transfer = components["schemas"]["SessionTransfer"];

/** Archive parts stay well under a tunnel's request size limit. */
export const MOVE_PART_BYTES = 8 * 1024 * 1024;

export type MoveStep = "prepare" | "copy" | "receive" | "commit";

export type MoveInput = {
	source: { client: Client; hostId: string; sessionId: string; label: string };
	target: { client: Client; hostId: string; label: string; projectId: string };
	transferId?: string;
	onStep?: (step: MoveStep) => void;
};

export type MoveResult = {
	sessionId: string;
	warnings: string[];
	/** False when the old session could not be marked as moved; retry with commitMove. */
	committed: boolean;
	commitError?: string;
	transferId: string;
	/** The destination's name as the source records it. */
	destinationLabel: string;
};

/** A move that failed before the destination took the session. */
export class MoveError extends Error {
	constructor(message: string, readonly step: MoveStep, readonly resumedSource: boolean) {
		super(message);
		this.name = "MoveError";
	}
}

export function newTransferId(): string {
	return "tr" + crypto.randomUUID().replaceAll("-", "").slice(0, 24);
}

/**
 * Moves a session from one AO daemon to another: prepare on the source, copy
 * the conversation archive, receive on the destination, then mark the source
 * moved. Until the destination accepts the session, a failure resumes the
 * source agent, so nothing is lost. After that, only marking the source is
 * left, which commitMove can retry.
 */
export async function moveSession(input: MoveInput): Promise<MoveResult> {
	const { source, target } = input;
	const transferId = input.transferId ?? newTransferId();
	let step: MoveStep = "prepare";
	let sessionId: string;
	let warnings: string[];
	let hostLabel: string;
	try {
		input.onStep?.("prepare");
		const prepared = await source.client.POST("/api/v1/sessions/{sessionId}/transfer/prepare", {
			params: { path: { sessionId: source.sessionId } },
			// "This computer" means nothing on the destination; empty makes the source daemon use its hostname.
			body: { transferId, sourceLabel: source.hostId === LOCAL_HOST ? "" : source.label },
		});
		// A busy agent is refused before anything stops, so there is nothing to resume.
		if (apiErrorCode(prepared.error) === "TRANSFER_AGENT_BUSY") throw new MoveError(apiErrorMessage(prepared.error), "prepare", true);
		if (prepared.error || !prepared.data) throw apiErrorException(prepared.error, "Could not prepare the move");
		const transfer: Transfer = prepared.data.transfer;
		if (transfer.native) {
			step = "copy";
			input.onStep?.("copy");
			await copyArchive(source, target.client, transferId);
		}
		step = "receive";
		input.onStep?.("receive");
		({ sessionId, warnings, hostLabel } = await receive(target.client, target.projectId, transfer));
	} catch (error) {
		if (error instanceof MoveError) throw error;
		const message = error instanceof Error ? error.message : String(error);
		const resumed = await source.client
			.POST("/api/v1/sessions/{sessionId}/resume-agent", { params: { path: { sessionId: source.sessionId } } })
			.then((res) => !res.error, () => false);
		throw new MoveError(message, step, resumed);
	}
	input.onStep?.("commit");
	const destinationLabel = target.hostId === LOCAL_HOST && hostLabel ? hostLabel : target.label;
	const commitError = await commitMove(source, { hostId: target.hostId, label: destinationLabel }, transferId, sessionId);
	return { sessionId, warnings, committed: !commitError, commitError, transferId, destinationLabel };
}

async function copyArchive(source: MoveInput["source"], target: Client, transferId: string): Promise<void> {
	const archive = await source.client.GET("/api/v1/sessions/{sessionId}/transfer/{transferId}/native", {
		params: { path: { sessionId: source.sessionId, transferId } },
		parseAs: "arrayBuffer",
	});
	if (archive.error || !archive.data) throw apiErrorException(archive.error, "Could not read the conversation");
	const bytes = new Uint8Array(archive.data as ArrayBuffer);
	for (let offset = 0, part = 0; offset < bytes.length; offset += MOVE_PART_BYTES, part++) {
		const res = await target.PUT("/api/v1/sessions/transfer/{transferId}/native/{part}", {
			params: { path: { transferId, part } },
			body: bytes.subarray(offset, offset + MOVE_PART_BYTES) as never,
			bodySerializer: (body: unknown) => body as BodyInit,
			headers: { "Content-Type": "application/octet-stream" },
		});
		if (res.error) throw apiErrorException(res.error, "Could not copy the conversation");
	}
}

/** Receive is idempotent per transfer, so a dropped response is retried once
 *  before the source is resumed; otherwise both computers could run it. */
async function receive(target: Client, projectId: string, transfer: Transfer): Promise<{ sessionId: string; warnings: string[]; hostLabel: string }> {
	for (let attempt = 0; ; attempt++) {
		try {
			const res = await target.POST("/api/v1/sessions/transfer/receive", { body: { projectId, transfer } });
			if (res.error || !res.data) throw apiErrorException(res.error, "The other computer could not start the session");
			return { sessionId: res.data.session.id, warnings: res.data.warnings ?? [], hostLabel: res.data.hostLabel ?? "" };
		} catch (error) {
			if (attempt >= 1 || !(error instanceof TypeError)) throw error;
		}
	}
}

/** Marks the source session as moved. Returns the error message, or undefined. */
export async function commitMove(
	source: MoveInput["source"],
	target: Pick<MoveInput["target"], "hostId" | "label">,
	transferId: string,
	sessionId: string,
): Promise<string | undefined> {
	try {
		const res = await source.client.POST("/api/v1/sessions/{sessionId}/transfer/commit", {
			params: { path: { sessionId: source.sessionId } },
			body: { transferId, destination: { hostId: target.hostId, label: target.label, sessionId } },
		});
		return res.error ? apiErrorMessage(res.error, "Could not mark the old session as moved") : undefined;
	} catch (error) {
		return error instanceof Error ? error.message : String(error);
	}
}

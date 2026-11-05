import { apiErrorCode, apiErrorRequestId } from "./api-client";

/** Keep correlation facts without copying arbitrary server messages into auth reports. */
export class AgentCheckError extends Error {
	constructor(stage: string, error: unknown, status?: number) {
		const code = apiErrorCode(error);
		const request = apiErrorRequestId(error);
		const safe = (value?: string) => value && /^[a-zA-Z0-9_-]{1,128}$/.test(value) ? value : undefined;
		super([
			`${stage} request failed`,
			status ? `HTTP ${status}` : undefined,
			safe(code) ? `code ${code}` : undefined,
			safe(request) ? `request ${request}` : undefined,
		].filter(Boolean).join(" · "));
	}
}

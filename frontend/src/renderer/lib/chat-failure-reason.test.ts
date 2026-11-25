import { describe, expect, it } from "vitest";
import { CHAT_FAILURE_REASONS, chatFailureReason } from "./chat-failure-reason";
import { ERROR_CODES } from "./ui-usage-registry";

describe("chatFailureReason", () => {
	it.each([
		["You exceeded your current quota, please check your plan and billing details. (429 insufficient_quota)", "quota_exceeded"],
		["Your credit balance is too low to access the Anthropic API.", "quota_exceeded"],
		["Billing hard limit has been reached (billing_hard_limit_reached)", "quota_exceeded"],
		["Billing quota exceeded", "quota_exceeded"],
		["429: billing account quota has been exceeded", "quota_exceeded"],
		["401 Unauthorized: billing quota endpoint requires authentication", "auth_required"],
		["401 Unauthorized: billing account authentication token has expired.", "auth_required"],
		["429 Too Many Requests: see your billing settings for higher limits", "rate_limited"],
		["429 Too Many Requests: rate limit reached for requests", "rate_limited"],
		['API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}', "provider_overloaded"],
		["prompt is too long: 215000 tokens > 200000 maximum", "context_too_long"],
		["This model's maximum context length is 128000 tokens.", "context_too_long"],
		["The model `gpt-9` does not exist or you do not have access to it.", "model_unavailable"],
		["401 Unauthorized: invalid x-api-key", "auth_required"],
		["Not logged in. Please run /login", "auth_required"],
		["request failed: read ECONNRESET", "network_error"],
		["TypeError: fetch failed", "network_error"],
		["Request timed out.", "timed_out"],
		["500 Internal Server Error", "provider_server_error"],
		["502 Bad Gateway", "provider_server_error"],
		["spawn codex ENOENT", "agent_not_installed"],
		["'claude' is not recognized as an internal or external command", "agent_not_installed"],
		["agent process exited with code 1", "agent_exited"],
		["Your request was flagged as potentially violating our usage policy.", "content_refused"],
	] as const)("reads %j as %s", (error, reason) => {
		expect(chatFailureReason(error)).toBe(reason);
	});

	it("has no reason for an error it does not know, or none at all", () => {
		expect(chatFailureReason("Something went wrong")).toBeUndefined();
		expect(chatFailureReason("")).toBeUndefined();
		expect(chatFailureReason(undefined)).toBeUndefined();
	});

	it("only produces codes the UI error event may send", () => {
		const allowed = new Set<string>(ERROR_CODES);
		expect(CHAT_FAILURE_REASONS.filter((reason) => !allowed.has(reason.toUpperCase()))).toEqual([]);
	});
});

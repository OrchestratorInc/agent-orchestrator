import type { ServerConfig } from "../config";
import type { ConversationActivity, ConversationSnapshot, DecisionOption } from "../chat/types";

export type ActionContext = {
	kind: "approval" | "input" | "message";
	scope: string;
	requestId?: string;
	decisions?: DecisionOption[];
	field?: string;
	minLength?: number;
	maxLength?: number;
};
export type WatchActionResult = {
	status: "context" | "sent" | "already_handled" | "open_phone" | "error" | "uncertain";
	message?: string;
	code?: string;
	httpStatus?: number;
	context?: ActionContext & { token: string; expiresAt: number };
};
type Dependencies = {
	configForHost(hostId: string): ServerConfig | null;
	read(config: ServerConfig, sessionId: string): Promise<ConversationSnapshot>;
	approve(config: ServerConfig, sessionId: string, requestId: string, decisionId: string): Promise<unknown>;
	input(config: ServerConfig, sessionId: string, requestId: string, action: "accept", content: Record<string, unknown>): Promise<unknown>;
	message(config: ServerConfig, sessionId: string, input: { text: string; clientMessageId: string }): Promise<unknown>;
	isActive(nativeRequestId: string): Promise<boolean>;
	now(): number;
	newId(): string;
};
const openPhone: WatchActionResult = { status: "open_phone", message: "Open AO on iPhone. Refresh and review the full request there." };
const notOffered: WatchActionResult = { status: "error", httpStatus: 400, code: "CHAT_DECISION_NOT_OFFERED", message: "That decision is not offered. Refresh on iPhone." };
const handled: WatchActionResult = { status: "already_handled", httpStatus: 409, code: "CHAT_REQUEST_NOT_PENDING", message: "Already handled or no longer pending. This does not confirm a Watch approval." };
const short = (value: unknown, max = 120): value is string => typeof value === "string" && value.trim().length > 0 && value.length <= max && !/[\u0000-\u001f\u007f\u200b-\u200f\u202a-\u202e\u2066-\u2069]|…|\.{3}/u.test(value);
const onlyKeys = (value: object, keys: string[]) => Object.keys(value).every((key) => keys.includes(key));

function permissionContext(request: ConversationActivity): ActionContext | null {
	const d = request.detail;
	// The adapter projects a subset of the provider request; scopeComplete is its proof nothing else was dropped.
	if (!d || !onlyKeys(d, ["method", "command", "rawCommand", "cwd", "reason", "itemId", "decisions", "scopeComplete"]) ||
		d.scopeComplete !== true || d.method !== "item/commandExecution/requestApproval" || !short(d.command) || !short(d.cwd, 80) ||
		(d.reason !== undefined && !short(d.reason)) || (d.rawCommand !== undefined && d.rawCommand !== d.command)) return null;
	// shortcut: only literal read-only commands, expand with structured scope evidence, not shell heuristics.
	if (!["pwd", "git status", "git status --short", "git status --short --branch", "git log -1 --oneline"].includes(d.command)) return null;
	const scope = `${d.rawCommand ?? d.command}\nIn: ${d.cwd}${d.reason ? `\n${d.reason}` : ""}`;
	if (scope.length > 240) return null;
	if (new Set(request.decisions?.map((option) => option.id)).size !== request.decisions?.length) return null;
	const decisions = request.decisions?.filter((option) =>
		(option.kind === "allow_once" || option.kind === "reject_once") && short(option.id, 160) && short(option.label, 48));
	if (!decisions?.length || decisions.length > 4 || new Set(decisions.map((d) => d.id)).size !== decisions.length) return null;
	return { kind: "approval", requestId: request.requestId, scope, decisions };
}

function inputContext(request: ConversationActivity): ActionContext | null {
	const d = request.detail;
	const schema = d?.schema;
	if (!d || d.inputMode !== "form" || !schema || !short(d.message) ||
		(d.url !== undefined && d.url !== "") ||
		!onlyKeys(d, ["inputMode", "message", "schema", "elicitationId", "url"]) ||
		!onlyKeys(schema, ["type", "title", "description", "properties", "required", "additionalProperties"]) ||
		(schema.type !== undefined && schema.type !== "object")) return null;
	const fields = Object.entries(schema.properties ?? {});
	if (fields.length !== 1) return null;
	const [field, property] = fields[0];
	if (!short(field, 48) || property.type !== "string" ||
		!onlyKeys(property, ["type", "title", "description", "minLength", "maxLength"]) ||
		(schema.required && schema.required.some((key) => key !== field))) return null;
	const labels = [schema.title, schema.description, property.title, property.description].filter((value) => value !== undefined);
	if (labels.some((value) => !short(value))) return null;
	const scope = [d.message, ...labels, `Reply: ${field}`].join("\n");
	const minLength = Math.max(1, property.minLength ?? 1);
	const maxLength = Math.min(500, property.maxLength ?? 500);
	if (scope.length > 240 || !Number.isInteger(minLength) || !Number.isInteger(maxLength) || minLength > maxLength) return null;
	return { kind: "input", requestId: request.requestId, scope, field, minLength, maxLength };
}

/** No inferred consent, truncated scope, multi-field forms, or permission-as-message fallback. */
export function actionContext(snapshot: ConversationSnapshot): ActionContext | null {
	if (snapshot.mode !== "chat" || !["ready", "busy"].includes(snapshot.controller.state)) return null;
	const pending = snapshot.items.filter((item): item is ConversationActivity => item.kind === "activity" &&
		(item.activityKind === "approval" || item.activityKind === "user_input") && item.status === "pending");
	if (pending.length > 1) return null;
	if (!pending.length) {
		// A paged read cannot prove there is no older pending permission.
		if (snapshot.hasMoreBefore) return null;
		return { kind: "message", scope: "Send a message to this worker (not a permission decision).", minLength: 1, maxLength: 500 };
	}
	const request = pending[0];
	if (!short(request.requestId, 160)) return null;
	return request.activityKind === "approval" ? permissionContext(request) : inputContext(request);
}

/** Volatile confirmation tokens, never an offline queue. Every mutation re-reads daemon context. */
export function createWatchActionHandler(deps: Dependencies) {
	const contexts = new Map<string, { hostId: string; sessionId: string; config: ServerConfig; context: ActionContext; expiresAt: number }>();
	return async (payload: unknown, nativeRequestId: string, deadline: number): Promise<WatchActionResult> => {
		let writing = false;
		try {
			if (!payload || typeof payload !== "object" || Array.isArray(payload)) return openPhone;
			const p = payload as Record<string, unknown>;
			if (!short(p.hostId, 256) || !short(p.sessionId, 256) || !["inspect", "decide", "reply"].includes(String(p.kind))) return openPhone;
			const hostId = p.hostId, sessionId = p.sessionId;
			const cfg = deps.configForHost(hostId);
			if (!cfg || cfg.hostId !== hostId) return openPhone;
			const active = async () => await deps.isActive(nativeRequestId) && deps.now() < deadline && deps.configForHost(hostId) === cfg;
			if (!await active()) return openPhone;
			for (const [token, value] of contexts) if (value.expiresAt <= deps.now()) contexts.delete(token);
			const issued = typeof p.token === "string" ? contexts.get(p.token) : undefined;
			if (p.kind !== "inspect" && (!issued || issued.hostId !== hostId || issued.sessionId !== sessionId || issued.config !== cfg)) return openPhone;
			const snapshot = await deps.read(cfg, sessionId);
			if (!await active() || snapshot.sessionId !== sessionId) return openPhone;
			const context = actionContext(snapshot);
			if (p.kind === "inspect") {
				if (!context) return openPhone;
				if (contexts.size >= 20) contexts.delete(contexts.keys().next().value!);
				const token = deps.newId(), expiresAt = deps.now() + 60_000;
				contexts.set(token, { hostId, sessionId, config: cfg, context, expiresAt });
				return { status: "context", context: { ...context, token, expiresAt } };
			}
			if (!issued || issued.expiresAt <= deps.now()) return openPhone;
			if (issued.context.requestId && !snapshot.items.some((item) => item.kind === "activity" && item.requestId === issued.context.requestId && item.status === "pending")) return handled;
			if (!context || JSON.stringify(context) !== JSON.stringify(issued.context)) return openPhone;
			if (p.kind === "decide") {
				if (context.kind !== "approval" || !context.requestId) return openPhone;
				if (typeof p.decisionId !== "string" || !context.decisions?.some((option) => option.id === p.decisionId)) return notOffered;
				writing = true;
				await deps.approve(cfg, sessionId, context.requestId, p.decisionId);
			} else {
				if (context.kind === "approval") return openPhone;
				if (typeof p.text !== "string" || !p.text.trim() || Array.from(p.text).length < (context.minLength ?? 1) || Array.from(p.text).length > (context.maxLength ?? 500) || !short(p.clientMessageId, 160)) return { status: "error", message: "Reply length is invalid. Review it again." };
				writing = true;
				if (context.kind === "input" && context.requestId && context.field) await deps.input(cfg, sessionId, context.requestId, "accept", { [context.field]: p.text });
				else await deps.message(cfg, sessionId, { text: p.text, clientMessageId: p.clientMessageId });
			}
			return { status: "sent", message: "Sent to AO." };
		} catch (error) {
			const e = error as { status?: number; code?: string } | null;
			if (e?.status === 409 && e.code === "CHAT_REQUEST_NOT_PENDING") return handled;
			if (e?.status === 400 && e.code === "CHAT_DECISION_NOT_OFFERED") return notOffered;
			if (e?.status === 401 || e?.status === 403) return { ...openPhone, message: "Authorization failed. Reconnect in AO on iPhone." };
			if (e?.status && e.status < 500) return { status: "error", httpStatus: e.status, code: e.code, message: "AO rejected this request. Review on iPhone." };
			return writing ? { status: "uncertain", message: "Delivery unknown. Check AO on iPhone before trying again." } : openPhone;
		}
	};
}

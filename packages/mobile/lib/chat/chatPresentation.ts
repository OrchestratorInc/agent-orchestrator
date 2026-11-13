import type { Feather } from "../icons";
import type { ConversationTurn } from "./types";

export type ChatTone = "accent" | "attention" | "danger" | "muted" | "success" | "working";

export type ChatPresentation = {
	icon: keyof typeof Feather.glyphMap;
	label: string;
	tone: ChatTone;
};

export function requestPresentation(kind: "approval" | "input", pending: boolean): Omit<ChatPresentation, "label"> & { title: string; badge: string } {
	if (!pending) return { icon: "check-circle", title: kind === "approval" ? "Approval resolved" : "Input resolved", badge: "Resolved", tone: "muted" };
	return kind === "approval"
		? { icon: "shield", title: "Approval needed", badge: "Needs approval", tone: "attention" }
		: { icon: "message-circle", title: "Agent needs input", badge: "Needs input", tone: "accent" };
}

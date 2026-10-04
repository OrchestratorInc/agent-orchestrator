export function isConcreteModelID(id: string): boolean {
	return id !== "" && id.toLowerCase() !== "default";
}

export function isDefaultPlaceholderLabel(label: string): boolean {
	return /^default(?:\s*\([^)]*\))?$/i.test(label.trim());
}

export function modelChoiceLabel(choice: { id: string; label: string }): string {
	return isDefaultPlaceholderLabel(choice.label) ? choice.id : choice.label || choice.id;
}

export function agentModelDisplayLabel(agentId: string | undefined, label: string): string {
	return agentId === "claude-code" ? label.replace(/^Claude\s+/i, "") : label;
}

export function supportsModelEffortAtLaunch(
	harness: string,
	defaultSessionMode: "chat" | "tui" | undefined,
	chatHarnesses: readonly string[],
): boolean {
	if (harness === "codex" || harness === "claude-code") return true;
	if (harness === "unreal-agent") return true;
	return defaultSessionMode === "chat" && chatHarnesses.includes(harness) &&
		["pi", "opencode", "opencode-v2", "deepseek-harness"].includes(harness);
}

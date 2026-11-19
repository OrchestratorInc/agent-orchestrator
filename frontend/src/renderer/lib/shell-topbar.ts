/**
 * Whether the page in the center panel draws its own top bar. Home and new chat
 * do not, so their panel reclaims the band above it; an orchestrator that is
 * still starting shows its chat, which does.
 */
export function pageHasTopbar({
	isHomeRoute,
	isNewChatRoute,
	startingOrchestrator,
}: {
	isHomeRoute: boolean;
	isNewChatRoute: boolean;
	startingOrchestrator: boolean;
}): boolean {
	return (!isHomeRoute && !isNewChatRoute) || startingOrchestrator;
}

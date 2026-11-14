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

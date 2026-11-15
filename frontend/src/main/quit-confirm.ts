export function createQuitConfirmController(options: {
	canConfirm(): boolean;
	requestConfirm(): void;
	quit(): void;
}) {
	let pending = false;

	return {
		menuQuit(fromShortcut: boolean): void {
			if (!fromShortcut || pending || !options.canConfirm()) {
				pending = false;
				options.quit();
				return;
			}
			pending = true;
			options.requestConfirm();
		},
		resolve(confirmed: boolean): void {
			pending = false;
			if (confirmed) options.quit();
		},
		reset(): void {
			pending = false;
		},
	};
}

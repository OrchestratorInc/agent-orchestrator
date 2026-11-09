// A machine is forgotten from its own page, which then closes; the Settings
// list it returns to is what says so. One message waits here for the list to
// take when it next comes into focus.
let pending: string | undefined;

export function postEnvironmentNotice(text: string): void {
	pending = text;
}

export function takeEnvironmentNotice(): string | undefined {
	const text = pending;
	pending = undefined;
	return text;
}

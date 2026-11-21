import type * as DemoMachine from "./index";
import type * as DemoTransport from "./transport";

// The only way code outside this folder reaches the demo machine. A release
// build never loads it: Metro inlines __DEV__ as false, folds each condition
// below to null and drops the require with it, so the fake daemon, its
// projects and its chats are not in the release bundle at all. Static imports
// of ./index or ./transport from shipped code would put them back.

/** The demo machine's pairing, settings and manager, in a development build only. */
export function demoMachine(): typeof DemoMachine | null {
	return typeof __DEV__ !== "undefined" && __DEV__ === true ? (require("./index") as typeof DemoMachine) : null;
}

/** The demo machine's transport, in a development build only. */
export function demoTransport(): typeof DemoTransport | null {
	return typeof __DEV__ !== "undefined" && __DEV__ === true ? (require("./transport") as typeof DemoTransport) : null;
}

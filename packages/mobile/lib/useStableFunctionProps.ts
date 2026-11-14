import { useLayoutEffect, useRef } from "react";

/**
 * The same props, with every function replaced by one that keeps its identity
 * for the life of the component and always calls the newest function passed.
 *
 * This lets a memoized child skip renders that only an inline handler's new
 * identity would cause, without ever calling a stale handler. A prop that goes
 * from a function to undefined (or back) still changes, so a child that shows
 * a control only when its handler is present still updates.
 *
 * The forwarders call the handlers of the last committed render, so they are
 * for events and effects, not for calling during render.
 */
export function useStableFunctionProps<P extends object>(props: P): P {
	const latest = useRef(props);
	// Taken up once the render commits, not while rendering: a render React
	// throws away (concurrent rendering, StrictMode's second pass) must not
	// leave its handlers behind for the forwarders to call.
	useLayoutEffect(() => { latest.current = props; });
	const forwarders = useRef(new Map<string, (...args: unknown[]) => unknown>());
	const stable: Record<string, unknown> = {};
	for (const [key, value] of Object.entries(props)) {
		if (typeof value !== "function") {
			stable[key] = value;
			continue;
		}
		let forward = forwarders.current.get(key);
		if (!forward) {
			forward = (...args: unknown[]) => (latest.current as Record<string, (...a: unknown[]) => unknown>)[key]?.(...args);
			forwarders.current.set(key, forward);
		}
		stable[key] = forward;
	}
	return stable as P;
}

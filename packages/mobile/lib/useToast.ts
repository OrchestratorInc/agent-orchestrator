import { useCallback, useMemo, useRef, useState } from "react";
import type { ToastController, ToastMessage, ToastOptions } from "./Toast.types";

/**
 * The state behind one screen's transient messages. Pair it with a
 * `ToastHost` on the same screen:
 *
 *   const toast = useToast();
 *   toast.show("Path copied.");
 *   <ToastHost toast={toast} />
 *
 * The host takes each message down when it has been read; `hide` is only for
 * taking one down early.
 */
export function useToast(): ToastController {
	const [current, setCurrent] = useState<ToastMessage>();
	const lastId = useRef(0);
	const show = useCallback((text: string, options: ToastOptions = {}) => {
		lastId.current += 1;
		setCurrent({ id: lastId.current, text, duration: options.duration ?? "short", tone: options.tone ?? "neutral", dismissible: options.dismissible ?? false });
	}, []);
	// A message that times out after a newer one replaced it must not take the
	// newer one down with it, so the host hides by id.
	const hide = useCallback((id?: number) => {
		setCurrent((shown) => (shown && (id === undefined || shown.id === id) ? undefined : shown));
	}, []);
	return useMemo(() => ({ current, show, hide }), [current, show, hide]);
}

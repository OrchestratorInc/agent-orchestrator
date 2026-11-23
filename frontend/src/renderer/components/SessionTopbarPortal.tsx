import { createContext, useContext, useLayoutEffect, useMemo, useRef, useState, type ComponentProps, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { finishReplayedAnimationsBeforePaint } from "../lib/quiet-reveal";
import { cn } from "../lib/utils";

type SessionTopbarContextValue = {
	host: HTMLDivElement | null;
	setHost: (host: HTMLDivElement | null) => void;
};

const SessionTopbarContext = createContext<SessionTopbarContextValue | undefined>(undefined);

export function SessionTopbarProvider({ children }: { children: ReactNode }) {
	const [host, setHost] = useState<HTMLDivElement | null>(null);
	const value = useMemo(() => ({ host, setHost }), [host]);

	return <SessionTopbarContext.Provider value={value}>{children}</SessionTopbarContext.Provider>;
}

export function SessionTopbarHost({ className, ref, ...props }: ComponentProps<"div">) {
	const context = useContext(SessionTopbarContext);
	if (!context) throw new Error("SessionTopbarHost must be used within SessionTopbarProvider");

	return (
		<div
			className={cn("workspace-topbar-container", className)}
			ref={(element) => {
				context.setHost(element);
				if (typeof ref === "function") ref(element);
				else if (ref) ref.current = element;
			}}
			{...props}
		/>
	);
}

export function SessionTopbarPortal({ children }: { children: ReactNode }) {
	const context = useContext(SessionTopbarContext);
	if (!context) return children;
	if (!context.host) return null;
	return createPortal(children, context.host);
}

/**
 * Gives a view kept alive by KeepRecentAlive its own container in the session
 * top bar, attached only while the view is shown. A hidden Activity hides the
 * view's DOM but not what it portals, so without this every recent chat's
 * header stayed in the top bar beside the current one. Hiding a view cleans up
 * its layout effects, which detaches the container in the same commit; showing
 * it again re-attaches it before the view's own layout effects measure the bar.
 */
export function SessionTopbarScope({ children }: { children: ReactNode }) {
	const parent = useContext(SessionTopbarContext);
	const [container] = useState(() => {
		const element = document.createElement("div");
		element.style.display = "contents";
		element.dataset.sessionTopbarScope = "";
		return element;
	});
	const value = useMemo(() => (parent ? { host: container, setHost: parent.setHost } : undefined), [container, parent]);
	if (!parent) return children;
	return (
		<SessionTopbarContext.Provider value={value}>
			{/* Before the view, so the container is in the bar when the view's layout effects run. */}
			<AttachTopbarScope container={container} host={parent.host} />
			{children}
		</SessionTopbarContext.Provider>
	);
}

function AttachTopbarScope({ container, host }: { container: HTMLDivElement; host: HTMLDivElement | null }) {
	const attachedBefore = useRef(false);
	useLayoutEffect(() => {
		if (!host) return;
		host.append(container);
		// Back in the bar after its view was hidden: the header's own entrance
		// (sliding in on arrival from the new chat page) already played.
		const cancelFinish = attachedBefore.current ? finishReplayedAnimationsBeforePaint(container) : undefined;
		attachedBefore.current = true;
		return () => {
			cancelFinish?.();
			container.remove();
		};
	}, [container, host]);
	return null;
}

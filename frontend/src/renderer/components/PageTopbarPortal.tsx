import { createContext, useContext, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { cn } from "../lib/utils";

const TopbarHostContext = createContext<HTMLDivElement | null | undefined>(undefined);
const TopbarVisibilityContext = createContext(true);

export function PageTopbarPortalProvider({ host, children }: { host: HTMLDivElement | null; children: ReactNode }) {
	return <TopbarHostContext.Provider value={host}>{children}</TopbarHostContext.Provider>;
}

/** Hidden routes retain their state without leaving their portalled headers visible. */
export function PageTopbarVisibility({ visible, children }: { visible: boolean; children: ReactNode }) {
	const parentVisible = useContext(TopbarVisibilityContext);
	return <TopbarVisibilityContext.Provider value={parentVisible && visible}>{children}</TopbarVisibilityContext.Provider>;
}

/** Keep a header aligned with its pane while rendering above the rounded surface. */
export function PageTopbarPortal({ children, region = "page", active = true }: {
	children: ReactNode;
	region?: "page" | "session" | "inspector";
	active?: boolean;
}) {
	const host = useContext(TopbarHostContext);
	const visible = useContext(TopbarVisibilityContext) && active;
	const sourceRef = useRef<HTMLDivElement>(null);
	const [width, setWidth] = useState(0);
	useLayoutEffect(() => {
		const source = sourceRef.current;
		if (!source || host === undefined) return;
		const measure = () => setWidth(source.getBoundingClientRect().width);
		measure();
		const observer = new ResizeObserver(measure);
		observer.observe(source);
		return () => observer.disconnect();
	}, [host]);

	if (host === undefined) return children;
	return <>
		<div aria-hidden="true" className="h-0 w-full shrink-0" ref={sourceRef} />
		{host ? createPortal(
			<div
				className={cn("center-panel-topbar-region", `center-panel-topbar-region--${region}`)}
				hidden={!visible || width === 0}
				style={{ width }}
			>
				{children}
			</div>,
			host,
		) : null}
	</>;
}

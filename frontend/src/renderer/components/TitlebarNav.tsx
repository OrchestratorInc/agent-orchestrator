import { useCanGoBack, useNavigate, useRouter } from "@tanstack/react-router";
import { ArrowLeft, ArrowRight, PanelLeft } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { isLinuxPlatform, isMacPlatform } from "../lib/platform";
import { sidebarIsVisible, useUiStore } from "../stores/ui-store";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const isMac = isMacPlatform();
const isLinux = isLinuxPlatform();
const noDragStyle = isMac
  ? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties)
  : undefined;

// Sidebar chrome cluster (sidebar toggle + history arrows). It stays fixed while
// the sidebar expands or collapses. macOS pins it beside the traffic lights;
// Linux has no traffic lights, so it sits at the sidebar's top-left. (Windows
// keeps these controls in its own titlebar.)
// The installed router has no useCanGoForward, and deriving one as
// `__TSR_index < history.length - 1` (the upstream hook's approach) is wrong
// here: window.history.length also counts entries the router never created —
// the WebContents' initial blank entry, pre-router loads — so the tip of the
// stack still reads as "forward available" and the arrow no-ops. Instead,
// track the highest router index reachable on the live stack: a PUSH discards
// the forward entries (the new index is the tip); BACK/FORWARD/GO only move
// within it. After a mid-stack reload the tip resets to the current entry —
// forward greys out rather than dangle on entries we can no longer see.
export function useCanGoForward(): boolean {
  const router = useRouter();
  const [canGoForward, setCanGoForward] = useState(false);
  useEffect(() => {
    let tip = router.history.location.state.__TSR_index;
    return router.history.subscribe(({ location, action }) => {
      const index = location.state.__TSR_index;
      tip = action.type === "PUSH" ? index : Math.max(tip, index);
      setCanGoForward(index < tip);
    });
  }, [router]);
  return canGoForward;
}

// Reveals the history arrows while the pointer is over the sidebar body or the
// button cluster. The rest of the titlebar band stays a window-drag region
// (double-click to maximize), so it cannot report hover. A short leave delay
// absorbs the one-frame gap when the pointer crosses between the two.
const REVEAL_LEAVE_DELAY_MS = 60;

function useSidebarReveal(isSidebarOpen: boolean) {
  const [revealed, setRevealed] = useState(false);
  const inside = useRef({ zone: false, sidebar: false });
  const timer = useRef<number | undefined>(undefined);

  const update = useCallback((source: "zone" | "sidebar", value: boolean) => {
    inside.current[source] = value;
    window.clearTimeout(timer.current);
    if (inside.current.zone || inside.current.sidebar) {
      setRevealed(true);
    } else {
      timer.current = window.setTimeout(() => setRevealed(false), REVEAL_LEAVE_DELAY_MS);
    }
  }, []);

  useEffect(() => {
    if (!isSidebarOpen) {
      inside.current = { zone: false, sidebar: false };
      window.clearTimeout(timer.current);
      setRevealed(false);
      return;
    }
    let el: HTMLElement | null = null;
    const enter = () => update("sidebar", true);
    const leave = () => update("sidebar", false);
    const frame = requestAnimationFrame(() => {
      el = document.querySelector<HTMLElement>('[data-slot="sidebar-container"]');
      if (!el) return;
      el.addEventListener("pointerenter", enter);
      el.addEventListener("pointerleave", leave);
    });
    return () => {
      cancelAnimationFrame(frame);
      window.clearTimeout(timer.current);
      el?.removeEventListener("pointerenter", enter);
      el?.removeEventListener("pointerleave", leave);
    };
  }, [isSidebarOpen, update]);

  return {
    revealed,
    onZoneEnter: () => update("zone", true),
    onZoneLeave: () => update("zone", false),
  };
}

export function TitlebarNav({
  historyLocked = false,
  isFullScreen = false,
}: {
  historyLocked?: boolean;
  isFullScreen?: boolean;
}) {
  const { t } = useTranslation();
  const toggleSidebar = useUiStore((state) => state.toggleSidebar);
  const isSidebarOpen = useUiStore(sidebarIsVisible);
  const router = useRouter();
  const navigate = useNavigate();
  const canGoBack = useCanGoBack();
  const canGoForward = useCanGoForward();
  const { revealed, onZoneEnter, onZoneLeave } =
    useSidebarReveal(isSidebarOpen);

  if (!isMac && !isLinux) return null;
  // Native fullscreen changes only the horizontal traffic-light reserve.
  // Sidebar and route state must never move the navigation centerline.
  const leftClass = !isMac
    ? isSidebarOpen
      ? "left-titlebar-cluster-left-linux"
      : "left-titlebar-cluster-left-linux-panel"
    : isFullScreen
      ? "left-titlebar-cluster-left-fullscreen"
      : "left-titlebar-cluster-left";
  const topClass = isMac ? "top-0" : "top-0.75";
  const heightClass = "h-traffic-light-clearance";

  return (
    <div
      className={`group/nav fixed ${topClass} ${leftClass} z-titlebar flex ${heightClass} items-center gap-1`}
      data-revealed={revealed || undefined}
      data-slot="titlebar-nav"
      onPointerEnter={onZoneEnter}
      onPointerLeave={onZoneLeave}
      style={noDragStyle}
    >
      <TitlebarButton
        label={
          isSidebarOpen ? t("shell.collapseSidebar") : t("shell.expandSidebar")
        }
        onClick={toggleSidebar}
        title={
          isSidebarOpen
            ? t("titlebar.collapseSidebarShortcut")
            : t("titlebar.expandSidebarShortcut")
        }
      >
        <PanelLeft className="size-icon-lg" aria-hidden="true" />
      </TitlebarButton>
      {/* With the sidebar open, the brand and the history arrows share one
          slot: the brand shows at rest and swaps to the arrows while the
          pointer is over the sidebar or titlebar row, or on keyboard focus. Collapsed, there is no brand, so the arrows stay put. */}
      <div className="grid items-center">
        {isSidebarOpen ? (
          <button
            aria-label={t("shell.goHome")}
            className="col-start-1 row-start-1 ml-1.5 whitespace-nowrap rounded-md px-0.5 text-left text-lg font-extrabold leading-tight tracking-tight-lg text-foreground group-has-focus-visible/nav:pointer-events-none group-has-focus-visible/nav:opacity-0 group-data-[revealed]/nav:pointer-events-none group-data-[revealed]/nav:opacity-0"
            data-sidebar-brand=""
            onClick={() => void navigate({ to: "/" })}
            style={noDragStyle}
            type="button"
          >
            Orchestrator.inc
          </button>
        ) : null}
        <div
          className={`col-start-1 row-start-1 flex items-center gap-1 ${
            isSidebarOpen
              ? "pointer-events-none opacity-0 group-has-focus-visible/nav:pointer-events-auto group-has-focus-visible/nav:opacity-100 group-data-[revealed]/nav:pointer-events-auto group-data-[revealed]/nav:opacity-100"
              : ""
          }`}
        >
          <TitlebarButton
            disabled={historyLocked || !canGoBack}
            label={t("titlebar.goBack")}
            onClick={() => router.history.back()}
            title={t("titlebar.goBack")}
          >
            <ArrowLeft className="size-icon-lg" aria-hidden="true" />
          </TitlebarButton>
          <TitlebarButton
            disabled={historyLocked || !canGoForward}
            label={t("titlebar.goForward")}
            onClick={() => router.history.forward()}
            title={t("titlebar.goForward")}
          >
            <ArrowRight className="size-icon-lg" aria-hidden="true" />
          </TitlebarButton>
        </div>
      </div>
    </div>
  );
}

function TitlebarButton({
  label,
  title,
  disabled,
  tabIndex,
  onClick,
  children,
}: {
  label: string;
  title: string;
  disabled?: boolean;
  tabIndex?: number;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="inline-flex">
          <button
            aria-label={label}
            aria-disabled={disabled || undefined}
            className="grid size-control-md place-items-center rounded-md text-passive transition-colors hover:bg-interactive-hover hover:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-55 disabled:hover:bg-transparent disabled:hover:text-passive"
            disabled={disabled}
            onClick={onClick}
            style={noDragStyle}
            tabIndex={tabIndex}
            type="button"
          >
            {children}
          </button>
        </span>
      </TooltipTrigger>
      <TooltipContent side="bottom">{title}</TooltipContent>
    </Tooltip>
  );
}

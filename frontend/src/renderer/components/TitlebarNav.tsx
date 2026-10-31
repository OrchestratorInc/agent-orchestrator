import { motion, useReducedMotion } from "motion/react";
import { useCanGoBack, useRouter } from "@tanstack/react-router";
import { ArrowLeft, ArrowRight, PanelLeft } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { SHELL_PANEL_SPRING } from "@/lib/motion-spring";
import { isLinuxPlatform, isMacPlatform } from "../lib/platform";
import { sidebarIsVisible, useUiStore } from "../stores/ui-store";
import { AOMascot } from "./AOMascot";
import { BuildInfoTooltip } from "./BuildInfoTooltip";
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

export function TitlebarNav({
  historyLocked = false,
  isFullScreen = false,
  zoomFactor = 1,
}: {
  historyLocked?: boolean;
  isFullScreen?: boolean;
  zoomFactor?: number;
}) {
  const { t } = useTranslation();
  const toggleSidebar = useUiStore((state) => state.toggleSidebar);
  const isSidebarOpen = useUiStore(sidebarIsVisible);
  const router = useRouter();
  const canGoBack = useCanGoBack();
  const canGoForward = useCanGoForward();
  // The brand and the arrows swap instantly with the sidebar toggle.
  const showBrand = isSidebarOpen;
  // The sidebar's minimum width is measured from the brand label, which only
  // exists once this mounts it. Nudge the sidebar's resize re-clamp so a stored
  // or default width narrower than the label grows to fit it. Fullscreen and
  // zoom move the label with the traffic-light reserve, and the window's own
  // resize fires before that lands, so nudge again once the label has moved.
  useEffect(() => {
    if (showBrand) window.dispatchEvent(new Event("resize"));
  }, [showBrand, isFullScreen, zoomFactor]);

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
  // top-0 plus the traffic-light row height puts this row on the native
  // buttons' centreline; fullscreen has no frame, so it uses the flush row.
  const topClass = isMac ? "top-0" : "top-0.75";
  const heightClass = isMac && !isFullScreen ? "h-traffic-light-clearance" : "h-toolbar";

  // With the sidebar open the brand sits where the history arrows would be;
  // collapsed, the arrows take its place.
  const arrowsVisible = !showBrand;
  const prefersReducedMotion = useReducedMotion();
  const transition = prefersReducedMotion ? { duration: 0 } : SHELL_PANEL_SPRING;

  // The brand sits in this fixed row, not inside the sidebar, so it does not
  // shrink when a small window caps the sidebar narrower than the label. Cap it
  // to the sidebar's right edge so it truncates instead of overlapping the tabs.
  const brandRef = useRef<HTMLSpanElement>(null);
  const [brandMaxWidth, setBrandMaxWidth] = useState<number | undefined>(undefined);
  useEffect(() => {
    if (!showBrand) return;
    const sidebar = document.querySelector<HTMLElement>('[data-slot="sidebar-container"]');
    if (!sidebar) return;
    const fit = () => {
      const brand = brandRef.current;
      if (!brand) return;
      // Layout width, not its painted right edge: the sidebar may still be sliding
      // in from off-canvas when this runs, and the slide leaves offsetWidth alone.
      const available = sidebar.offsetWidth - brand.getBoundingClientRect().left - 12;
      setBrandMaxWidth(Math.max(24, Math.floor(available)));
    };
    fit();
    const observer = new ResizeObserver(fit);
    observer.observe(sidebar);
    window.addEventListener("resize", fit);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", fit);
    };
  }, [showBrand]);

  return (
    <div
      className={`fixed ${topClass} ${leftClass} z-titlebar flex ${heightClass} items-center gap-1`}
      data-slot="titlebar-nav"
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
      {/* The brand and the history arrows trade places by animating width, on the
          same spring as the sidebar, so the brand closes with the sidebar edge.
          The hidden side collapses to width 0 and is inert, so it never leaves an
          invisible no-drag hole in the window-drag region. */}
      <div className="flex items-center">
        <motion.div
          animate={{ width: showBrand ? "auto" : 0, opacity: showBrand ? 1 : 0 }}
          aria-hidden={showBrand ? undefined : true}
          className="overflow-hidden"
          data-slot="titlebar-brand"
          initial={false}
          inert={!showBrand}
          transition={transition}
        >
          {/* Not a button on purpose. Only the label opts out of window drag (for
              the build-info tooltip); the rest of the row stays draggable. */}
          <span
            className="inline-flex select-none items-center gap-1.5 translate-y-0.5 whitespace-nowrap text-base font-semibold leading-tight tracking-tight-lg text-foreground"
            data-sidebar-brand=""
            ref={brandRef}
            style={{ maxWidth: brandMaxWidth }}
          >
            <BuildInfoTooltip>
              <span className="inline-flex min-w-0 items-center gap-1.5">
                <AOMascot className="h-5.5 w-5.5 shrink-0" />
                <span className="min-w-0 truncate" data-brand-label="">Orchestrator</span>
              </span>
            </BuildInfoTooltip>
          </span>
        </motion.div>
        <motion.div
          animate={{ width: arrowsVisible ? "auto" : 0, opacity: arrowsVisible ? 1 : 0 }}
          aria-hidden={arrowsVisible ? undefined : true}
          className="overflow-hidden"
          data-slot="titlebar-history"
          initial={false}
          inert={!arrowsVisible}
          transition={transition}
        >
          <div className="flex items-center gap-1">
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
        </motion.div>
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

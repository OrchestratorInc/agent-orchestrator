import { GitBranch } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../lib/bridge";
import { DEV_BUILD_INFO } from "../lib/dev-build-info";
import { IS_DEV } from "../lib/is-dev";
import { cn } from "../lib/utils";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type BuildChannel = "dev" | "nightly" | "stable";

function readPreviewChannel(): string | null {
  try {
    return window.localStorage.getItem("ao.dev.buildChannel");
  } catch {
    return null;
  }
}

// Channel is identity: derived from the running build, not the update-channel
// setting. Dev wins over the version string because dev runs report Electron's.
function useBuildChannel(): { channel: BuildChannel; version: string | undefined } {
  const [version, setVersion] = useState<string | undefined>(undefined);
  useEffect(() => {
    let cancelled = false;
    void Promise.resolve()
      .then(() => aoBridge.app.getVersion())
      .then((value) => {
        if (!cancelled) setVersion(typeof value === "string" ? value : undefined);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);
  if (IS_DEV) {
    // Dev-only preview of the other channels: localStorage.setItem("ao.dev.buildChannel", "nightly" | "stable"),
    // then reload. Stripped from production builds with the IS_DEV gate.
    const preview = readPreviewChannel();
    if (preview === "nightly") return { channel: "nightly", version: version ?? "0.0.0-nightly.preview" };
    if (preview === "stable") return { channel: "stable", version };
    return { channel: "dev", version };
  }
  return { channel: version?.includes("-nightly.") ? "nightly" : "stable", version };
}

const DOT_CLASS: Record<BuildChannel, string> = {
  dev: "bg-amber-500",
  nightly: "bg-violet-300 shadow-[0_0_6px_oklch(0.8_0.12_295)]",
  stable: "bg-emerald-500",
};

// Wraps the brand label. It opts out of the window-drag region on purpose:
// macOS swallows hover over drag regions, so the tooltip could never open.
export function BuildInfoTooltip({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation();
  const { channel, version } = useBuildChannel();
  const isNightly = channel === "nightly";
  const channelLabel =
    channel === "dev"
      ? t("shell.dev")
      : isNightly
        ? t("shell.nightly")
        : t("settings.updates.channel.stable");

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          className="inline-flex min-w-0"
          data-testid="build-info-trigger"
          style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
        >
          {children}
        </span>
      </TooltipTrigger>
      <TooltipContent
        align="start"
        className={cn(
          "relative min-w-52 max-w-72 overflow-hidden px-3 py-2.5",
          isNightly && "build-info-nightly",
        )}
        hideArrow
        side="bottom"
        sideOffset={6}
      >
        <div className="relative flex flex-col gap-2 text-xs" data-testid="build-info">
          {/* Baseline-align the label and the mono version (different fonts and
              sizes drift apart when only their boxes are centered); the dot opts out. */}
          <div className="flex items-baseline justify-between gap-4">
            <span className="flex items-baseline gap-2 font-medium capitalize">
              <span aria-hidden="true" className={cn("size-1.5 shrink-0 self-center rounded-full", DOT_CLASS[channel])} />
              {channelLabel}
            </span>
            {version ? (
              <span className={cn("truncate font-mono text-[11px]", isNightly ? "text-white/70" : "text-muted-foreground")}>
                v{version}
              </span>
            ) : null}
          </div>
          {channel === "dev" ? (
            <div className="flex items-center gap-1.5 border-t border-border pt-2 text-muted-foreground">
              <GitBranch aria-hidden="true" className="size-3 shrink-0" />
              <span className="truncate font-mono text-[11px]" title={DEV_BUILD_INFO.branch}>
                {DEV_BUILD_INFO.branch}
              </span>
            </div>
          ) : null}
        </div>
      </TooltipContent>
    </Tooltip>
  );
}

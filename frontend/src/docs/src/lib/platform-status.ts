export type PlatformStatus = "full" | "partial" | "none";

export const STATUS_LABEL: Record<PlatformStatus, string> = {
  full: "Supported",
  partial: "Limited",
  none: "Not supported",
};

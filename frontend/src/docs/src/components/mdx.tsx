import Link from "next/link";
import defaultMdxComponents from "fumadocs-ui/mdx";
import { Children, cloneElement, isValidElement, type ComponentPropsWithoutRef, type ReactElement, type ReactNode } from "react";
import type { MDXComponents } from "mdx/types";
import type { CodeBlockProps } from "fumadocs-ui/components/codeblock";
import { ArrowUpRight, ChevronDown, CircleAlert, Download, Info, TriangleAlert } from "lucide-react";
import { LocalizedCodeBlock } from "./code-block";
import { Tab, Tabs } from "./docs-tabs";

function textOf(node: ReactNode): string {
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(textOf).join("");
  if (isValidElement(node)) return textOf((node.props as { children?: ReactNode }).children);
  return "";
}

function slugify(text: string): string {
  return text
    .toLowerCase()
    .trim()
    .replace(/[^\w\s-]/g, "")
    .replace(/\s+/g, "-");
}

function Heading({
  level,
  children,
  ...props
}: {
  level: 2 | 3 | 4;
  children?: ReactNode;
} & ComponentPropsWithoutRef<"h2"> &
  ComponentPropsWithoutRef<"h3"> &
  ComponentPropsWithoutRef<"h4">) {
  const id = slugify(textOf(children));
  if (level === 2) return <h2 id={id} {...props}>{children}</h2>;
  if (level === 3) return <h3 id={id} {...props}>{children}</h3>;
  return <h4 id={id} {...props}>{children}</h4>;
}

const FILE_LOGOS: Record<string, string> = {
  "claude-code": "claude-code.svg",
  claude: "claude-code.svg",
  codex: "codex.svg",
  cursor: "cursor.svg",
  opencode: "opencode.svg",
};

export function Logo({ name, size = 20, className }: { name: string; size?: number; className?: string }) {
  const key = name.toLowerCase();
  const file = FILE_LOGOS[key];
  if (file) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={`/docs/logos/${file}`}
        alt=""
        aria-hidden="true"
        className={className}
        style={{ width: size, height: size, flexShrink: 0, objectFit: "contain" }}
      />
    );
  }

  return (
    <span aria-hidden="true" className={className} style={{ width: size, height: size }}>
      <span className="grid size-full place-items-center rounded-md border border-white/10 bg-white/[0.06] text-[0.6em] font-medium uppercase text-foreground/80">
        {name.charAt(0)}
      </span>
    </span>
  );
}

// One accent per meaning, drawn from the marketing site's sky blue for notes
// and the usual amber and red for warnings and errors.
const CALLOUT_TONE: Record<string, { icon: typeof Info; frame: string; mark: string }> = {
  info: { icon: Info, frame: "border-sky-400/20 bg-sky-400/[0.05]", mark: "text-sky-300" },
  warn: { icon: TriangleAlert, frame: "border-amber-400/25 bg-amber-400/[0.05]", mark: "text-amber-300" },
  warning: { icon: TriangleAlert, frame: "border-amber-400/25 bg-amber-400/[0.05]", mark: "text-amber-300" },
  error: { icon: CircleAlert, frame: "border-red-400/25 bg-red-400/[0.05]", mark: "text-red-300" },
};

export function Callout({ type = "info", title, children }: { type?: string; title?: ReactNode; children: ReactNode }) {
  const tone = CALLOUT_TONE[type] ?? CALLOUT_TONE.info;
  const Icon = tone.icon;
  return (
    <div className={`not-prose my-6 flex gap-3 rounded-xl border px-4 py-3.5 ${tone.frame}`}>
      <Icon aria-hidden="true" className={`mt-0.5 size-4 shrink-0 ${tone.mark}`} />
      <div className="min-w-0 flex-1">
        {title && <div className="mb-1 text-sm font-medium text-foreground">{title}</div>}
        <div className="docs-callout-body text-sm leading-relaxed text-foreground/70 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0">
          {children}
        </div>
      </div>
    </div>
  );
}

export function Accordions({ children, expandAll = false }: { children: ReactNode; expandAll?: boolean }) {
  const items = expandAll
    ? Children.map(children, (child) => {
        if (!isValidElement(child)) return child;
        return cloneElement(child as ReactElement<{ open?: boolean }>, { open: true });
      })
    : children;

  return (
    <div className="my-6 overflow-hidden rounded-xl border border-white/[0.08] bg-[oklch(0.175_0.006_257)]">{items}</div>
  );
}

export function Accordion({ title, children, open = false }: { title: ReactNode; children: ReactNode; open?: boolean }) {
  return (
    <details open={open} className="group border-b border-white/[0.08] last:border-b-0">
      <summary className="flex cursor-pointer list-none items-center gap-3 px-4 py-3.5 text-sm font-medium text-foreground transition-colors hover:bg-white/[0.03] [&::-webkit-details-marker]:hidden">
        <span className="min-w-0 flex-1">{title}</span>
        <ChevronDown
          aria-hidden="true"
          className="size-4 shrink-0 text-foreground/45 transition-transform group-open:rotate-180"
        />
      </summary>
      <div className="px-4 pb-4 text-sm text-foreground/70 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0">{children}</div>
    </details>
  );
}

export function Steps({ children }: { children: ReactNode }) {
  return <div className="docs-steps my-8">{children}</div>;
}

export function Step({ children }: { children: ReactNode }) {
  return <div className="[&>*:first-child]:mt-0">{children}</div>;
}

export function Cards({ children }: { children: ReactNode }) {
  return <div className="not-prose my-6 grid gap-3 sm:grid-cols-2">{children}</div>;
}

export function Card({
  title,
  href,
  description,
  children,
}: {
  title: string;
  href?: string;
  description?: ReactNode;
  children?: ReactNode;
}) {
  const body = (
    <>
      <span className="flex items-start gap-3">
        <span className="min-w-0 flex-1 text-[0.9375rem] font-medium text-foreground">{title}</span>
        {href && (
          <ArrowUpRight
            aria-hidden="true"
            className="mt-0.5 size-4 shrink-0 text-foreground/35 transition-[color,transform] group-hover:-translate-y-0.5 group-hover:translate-x-0.5 group-hover:text-foreground"
          />
        )}
      </span>
      <span className="mt-1.5 block text-sm leading-relaxed text-foreground/60">{description ?? children}</span>
    </>
  );
  const cls =
    "group block rounded-xl border border-white/[0.08] bg-[oklch(0.175_0.006_257)] p-4 no-underline transition-colors hover:border-white/[0.16] hover:bg-white/[0.04]";

  return href ? (
    <Link href={href} className={cls}>
      {body}
    </Link>
  ) : (
    <div className={cls}>{body}</div>
  );
}

export function PluginGrid({ children }: { children: ReactNode }) {
  return <div className="not-prose my-6 grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(260px,1fr))]">{children}</div>;
}

export function PluginCard({
  name,
  logo,
  href,
  description,
  badge,
}: {
  name: string;
  logo: string;
  href: string;
  description: string;
  badge?: ReactNode;
}) {
  return (
    <Link
      href={href}
      className="flex items-start gap-3.5 rounded-xl border border-white/[0.08] bg-[oklch(0.175_0.006_257)] p-4 no-underline transition-colors hover:border-white/[0.16] hover:bg-white/[0.04]"
    >
      <span className="grid size-9 shrink-0 place-items-center rounded-lg border border-white/10 bg-black/40">
        <Logo name={logo} size={22} />
      </span>
      <span className="flex min-w-0 flex-col gap-1">
        <span className="inline-flex items-center gap-2 text-[0.9375rem] font-medium text-foreground">
          {name}
          {badge}
        </span>
        <span className="text-[0.8125rem] leading-normal text-muted-foreground">{description}</span>
      </span>
    </Link>
  );
}

type Status = "full" | "partial" | "none";

const STATUS_LABEL: Record<Status, string> = {
  full: "Supported",
  partial: "Limited",
  none: "Not supported",
};

const STATUS_DOT: Record<Status, string> = {
  full: "bg-sky-400",
  partial: "bg-amber-400",
  none: "bg-muted-foreground",
};

function PlatformCell({ platform, status }: { platform: "macos" | "linux" | "windows"; status: Status }) {
  const title = platform === "macos" ? "macOS" : platform === "linux" ? "Linux" : "Windows";

  return (
    <div className="flex min-w-0 flex-1 items-center rounded-xl border border-white/[0.08] bg-[oklch(0.175_0.006_257)] px-4 py-3">
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="text-[0.8125rem] font-medium text-foreground">{title}</span>
        <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
          <span className={`inline-block size-1.5 rounded-full ${STATUS_DOT[status]}`} />
          {STATUS_LABEL[status]}
        </span>
      </div>
    </div>
  );
}

export function PlatformSupport({
  macos = "full",
  linux = "full",
  windows = "full",
  note,
}: {
  macos?: Status;
  linux?: Status;
  windows?: Status;
  note?: ReactNode;
}) {
  return (
    <div className="not-prose my-6">
      <div className="flex flex-wrap gap-2">
        <PlatformCell platform="macos" status={macos} />
        <PlatformCell platform="linux" status={linux} />
        <PlatformCell platform="windows" status={windows} />
      </div>
      {note && <p className="mt-2 text-[0.8125rem] text-muted-foreground">{note}</p>}
    </div>
  );
}

export function InstallDownloads() {
  const release = "https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download";
  const downloads = [
    { platform: "macOS Apple silicon", assets: [["Disk image", "Orchestrator-darwin-arm64.dmg"], ["Zip", "agent-orchestrator-darwin-arm64.zip"]] },
    { platform: "macOS Intel", assets: [["Disk image", "Orchestrator-darwin-x64.dmg"], ["Zip", "agent-orchestrator-darwin-x64.zip"]] },
    { platform: "Windows x64", assets: [["Installer", "agent-orchestrator-win32-x64.exe"]] },
    { platform: "Linux x64", assets: [["AppImage", "agent-orchestrator-linux-x64.AppImage"], ["Debian / Ubuntu", "agent-orchestrator-linux-x64.deb"], ["Fedora / RHEL", "agent-orchestrator-linux-x64.rpm"]] },
  ];

  return (
    <div role="group" aria-label="Download the latest Orchestrator desktop release" className="not-prose my-6 grid gap-3 sm:grid-cols-2">
      {downloads.map(({ platform, assets }) => (
        <div key={platform} className="rounded-xl border border-white/[0.08] bg-[oklch(0.175_0.006_257)] p-4">
          <div className="text-sm font-medium text-foreground">{platform}</div>
          <div className="mt-3 flex flex-wrap gap-2">
            {assets.map(([label, asset]) => (
              <a
                key={asset}
                href={`${release}/${asset}`}
                className="inline-flex items-center gap-1.5 rounded-lg border border-white/10 bg-white/[0.04] px-2.5 py-1.5 text-xs font-medium text-foreground/85 no-underline transition-colors hover:bg-white/[0.09] hover:text-foreground"
              >
                <Download aria-hidden="true" className="size-3.5 text-foreground/50" />
                {label}
              </a>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

export function getMDXComponents(components?: MDXComponents) {
  return {
    ...defaultMdxComponents,
    pre: (props) => <LocalizedCodeBlock {...(props as CodeBlockProps)} />,
    h2: (props) => <Heading level={2} {...props} />,
    h3: (props) => <Heading level={3} {...props} />,
    h4: (props) => <Heading level={4} {...props} />,
    Logo,
    Callout,
    Accordion,
    Accordions,
    Step,
    Steps,
    Tab,
    Tabs,
    Card,
    Cards,
    PluginCard,
    PluginGrid,
    PlatformSupport,
    InstallDownloads,
    ...components,
  } satisfies MDXComponents;
}

export const useMDXComponents = getMDXComponents;

declare global {
  type MDXProvidedComponents = ReturnType<typeof getMDXComponents>;
}

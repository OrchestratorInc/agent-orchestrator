"use client";

import { Children, isValidElement, useState, type ReactElement, type ReactNode } from "react";

export function Tab({ children }: { value?: string; children: ReactNode }) {
  return <>{children}</>;
}

export function Tabs({ items, children }: { items?: string[]; children: ReactNode }) {
  const tabs = Children.toArray(children).filter(isValidElement) as ReactElement<{ value?: string; children?: ReactNode }>[];
  const [active, setActive] = useState(0);
  const labels = items ?? tabs.map((tab, index) => tab.props.value ?? `Tab ${index + 1}`);

  return (
    <div className="my-6 overflow-hidden rounded-xl border border-border bg-muted/25">
      <div className="flex flex-wrap gap-1 border-b border-border bg-background p-1.5">
        {labels.map((label, index) => (
          <button
            key={label}
            type="button"
            onClick={() => setActive(index)}
            className={`rounded-lg px-3 py-1.5 text-sm transition-colors ${
              index === active ? "bg-muted text-foreground" : "text-muted-foreground hover:bg-muted/60 hover:text-foreground"
            }`}
          >
            {label}
          </button>
        ))}
      </div>
      <div className="prose max-w-none px-4 py-4 prose-p:my-2 prose-pre:my-2">
        {tabs[active]?.props.children}
      </div>
    </div>
  );
}

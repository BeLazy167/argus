"use client";

import { Command, CommandDialog } from "cmdk";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@clerk/nextjs";
import { Search, CornerDownLeft } from "lucide-react";
import { NAV_PRIMARY, NAV_GROUPS } from "./nav-items";

const ITEM_CLASS =
  "flex cursor-pointer items-center gap-2.5 rounded-sm px-2 py-2 font-mono text-sm text-slate-text transition-colors data-[selected=true]:bg-amber/10 data-[selected=true]:text-foreground";

/**
 * Dashboard command palette. Opens on Cmd/Ctrl+K and offers fuzzy navigation to
 * every dashboard route plus quick actions. cmdk supplies the accessible
 * combobox semantics (roving focus, aria-activedescendant, type-ahead filtering);
 * this component supplies the item set and the Argus styling.
 *
 * Mounted from the root layout (so it is reachable in the render graph) but only
 * active for signed-in app users — the Cmd+K listener and dialog stay inert on
 * marketing and auth surfaces.
 */
export function CommandMenu() {
  const { isSignedIn } = useAuth();
  const [open, setOpen] = useState(false);
  const router = useRouter();

  useEffect(() => {
    if (!isSignedIn) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        setOpen((o) => !o);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [isSignedIn]);

  const go = (href: string) => {
    setOpen(false);
    router.push(href);
  };

  const groups = [{ label: "Menu", items: NAV_PRIMARY }, ...NAV_GROUPS];

  if (!isSignedIn) return null;

  return (
    <CommandDialog
      open={open}
      onOpenChange={setOpen}
      label="Command menu"
      overlayClassName="fixed inset-0 z-[100] bg-void/80 backdrop-blur-sm data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=closed]:animate-out data-[state=closed]:fade-out-0"
      contentClassName="fixed left-1/2 top-[18%] z-[101] w-[calc(100%-2rem)] max-w-lg -translate-x-1/2 overflow-hidden border border-iron bg-charcoal shadow-2xl shadow-black/40 duration-150 data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95"
    >
      <div className="flex items-center gap-2 border-b border-iron px-3.5">
        <Search className="h-4 w-4 shrink-0 text-slate-text/60" aria-hidden />
        <Command.Input
          placeholder="Search commands..."
          className="w-full rounded-sm bg-transparent py-3.5 font-mono text-sm text-foreground outline-none placeholder:text-slate-text/50 focus-visible:ring-1 focus-visible:ring-amber/40"
        />
      </div>

      <Command.List className="max-h-[22rem] overflow-y-auto overflow-x-hidden p-2 [&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:pb-1 [&_[cmdk-group-heading]]:pt-2.5 [&_[cmdk-group-heading]]:font-mono [&_[cmdk-group-heading]]:text-[10px] [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group-heading]]:tracking-[0.16em] [&_[cmdk-group-heading]]:text-slate-text/50">
        <Command.Empty className="py-10 text-center font-mono text-xs text-slate-text">
          No matching command.
        </Command.Empty>

        {groups.map((group) => (
          <Command.Group key={group.label} heading={group.label}>
            {group.items.map((item) => (
              <Command.Item
                key={item.href}
                value={`${item.label} ${item.href}`}
                onSelect={() => go(item.href)}
                className={ITEM_CLASS}
              >
                <item.icon className="h-3.5 w-3.5 shrink-0" />
                {item.label}
              </Command.Item>
            ))}
          </Command.Group>
        ))}
      </Command.List>

      <div className="flex items-center justify-between border-t border-iron px-3.5 py-2 font-mono text-[10px] text-slate-text/50">
        <span className="inline-flex items-center gap-1">
          <CornerDownLeft className="h-3 w-3" aria-hidden /> select
        </span>
        <span>esc to close</span>
      </div>
    </CommandDialog>
  );
}

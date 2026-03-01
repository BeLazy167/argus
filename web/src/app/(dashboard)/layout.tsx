"use client";

import { UserButton } from "@clerk/nextjs";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  LayoutDashboard,
  MessageSquare,
  Brain,
  Settings,
} from "lucide-react";
import { QueryProvider } from "@/providers/query-provider";
import { InstallationProvider, useInstallation } from "@/providers/installation-provider";

const navItems = [
  { href: "/dashboard", label: "Overview", icon: LayoutDashboard },
  { href: "/reviews", label: "Reviews", icon: MessageSquare },
  { href: "/patterns", label: "Patterns", icon: Brain },
  { href: "/settings", label: "Settings", icon: Settings },
];

function SidebarLink({
  href,
  label,
  icon: Icon,
  active,
}: {
  href: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  active: boolean;
}) {
  return (
    <Link
      href={href}
      className={`flex items-center gap-3 rounded-md px-3 py-2 text-xs font-mono transition-colors ${
        active
          ? "border-l-2 border-amber bg-sidebar-accent text-amber"
          : "text-slate-text hover:bg-sidebar-accent hover:text-foreground"
      }`}
    >
      <Icon className="h-4 w-4" />
      {label}
    </Link>
  );
}

function InstallationSwitcher() {
  const { installations, active, setActive } = useInstallation();
  if (installations.length <= 1) return null;
  return (
    <div className="px-5 py-2 border-b border-sidebar-border">
      <select
        value={active?.id ?? ""}
        onChange={(e) => setActive(Number(e.target.value))}
        className="w-full bg-sidebar text-xs font-mono text-slate-text border border-sidebar-border rounded px-2 py-1 focus:outline-none focus:border-amber"
      >
        {installations.map((inst) => (
          <option key={inst.id} value={inst.id}>
            {inst.org_login}
          </option>
        ))}
      </select>
    </div>
  );
}

export default function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const pathname = usePathname();

  return (
    <QueryProvider>
      <InstallationProvider>
        <div className="flex h-screen overflow-hidden">
          {/* Sidebar */}
          <aside className="flex w-56 shrink-0 flex-col border-r border-sidebar-border bg-sidebar">
            {/* Wordmark + Status */}
            <div className="flex h-14 items-center justify-between border-b border-sidebar-border px-5">
              <Link
                href="/dashboard"
                className="wordmark text-xs text-amber tracking-[0.2em]"
              >
                ARGUS
              </Link>
              <div className="flex items-center gap-1.5">
                <span className="h-1.5 w-1.5 rounded-full bg-emerald-500 animate-pulse" />
                <span className="text-[9px] font-mono text-slate-text uppercase tracking-wider">Live</span>
              </div>
            </div>

            <InstallationSwitcher />

            {/* Nav */}
            <nav className="flex-1 space-y-1 px-3 py-4">
              {navItems.map((item) => (
                <SidebarLink
                  key={item.href}
                  {...item}
                  active={pathname === item.href || (item.href !== "/dashboard" && pathname.startsWith(item.href))}
                />
              ))}
            </nav>

            {/* User */}
            <div className="border-t border-sidebar-border p-4">
              <UserButton
                appearance={{
                  elements: {
                    avatarBox: "h-7 w-7",
                  },
                }}
              />
            </div>
          </aside>

          {/* Main content */}
          <main className="flex-1 overflow-y-auto scroll-smooth bg-background bg-noise">
            <div className="mx-auto max-w-6xl px-8 py-8">{children}</div>
          </main>
        </div>
      </InstallationProvider>
    </QueryProvider>
  );
}

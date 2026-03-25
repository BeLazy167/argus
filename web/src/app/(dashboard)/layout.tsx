"use client";

import { UserButton, OrganizationSwitcher } from "@clerk/nextjs";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  LayoutDashboard,
  MessageSquare,
  GitFork,
  Brain,
  Shield,
  Activity,
  Settings,
  Users,
  CreditCard,
  Menu,
  X,
} from "lucide-react";
import { useState } from "react";
import { QueryProvider } from "@/providers/query-provider";
import { InstallationProvider } from "@/providers/installation-provider";
import { ActiveRepoProvider, useActiveRepo } from "@/providers/active-repo-provider";
import { RepoSelect } from "@/components/dashboard/repo-select";

function TopBar() {
  const { repos, activeId, setSelectedId } = useActiveRepo();
  if (!repos.length) return null;
  return (
    <div className="sticky top-0 z-10 flex items-center justify-end border-b border-iron/30 bg-background/80 backdrop-blur-sm px-4 py-2 md:px-8">
      <RepoSelect repos={repos} value={activeId} onChange={setSelectedId} showAll={false} />
    </div>
  );
}

const navItems = [
  { href: "/dashboard", label: "Overview", icon: LayoutDashboard },
  { href: "/reviews", label: "Reviews", icon: MessageSquare },
  { href: "/repos", label: "Repos", icon: GitFork },
  { href: "/patterns", label: "Patterns", icon: Brain },
  { href: "/scenarios", label: "Scenarios", icon: Shield },
  { href: "/insights", label: "Insights", icon: Activity },
  { href: "/team", label: "Team", icon: Users },
  { href: "/billing", label: "Billing", icon: CreditCard },
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

export default function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const pathname = usePathname();
  const [mobileOpen, setMobileOpen] = useState(false);

  const isActive = (href: string) =>
    pathname === href || (href !== "/dashboard" && pathname.startsWith(href));

  const sidebarContent = (
    <>
      <div className="flex h-14 items-center justify-between border-b border-sidebar-border px-5">
        <Link href="/dashboard" className="wordmark text-xs text-amber tracking-[0.2em]">
          ARGUS
        </Link>
        <div className="flex items-center gap-1.5">
          <span className="h-1.5 w-1.5 rounded-full bg-emerald-500 animate-pulse" />
          <span className="text-[9px] font-mono text-slate-text uppercase tracking-wider">Live</span>
        </div>
      </div>
      <div className="px-3 py-2 border-b border-sidebar-border">
        <OrganizationSwitcher
          hidePersonal={false}
          afterSelectOrganizationUrl="/dashboard"
          afterCreateOrganizationUrl="/dashboard"
          appearance={{
            elements: {
              rootBox: "w-full",
              organizationSwitcherTrigger: "w-full justify-between px-3 py-2 text-xs font-mono border border-sidebar-border rounded bg-sidebar text-slate-text hover:text-foreground",
            }
          }}
        />
      </div>
      <nav className="flex-1 space-y-1 px-3 py-4">
        {navItems.map((item) => (
          <SidebarLink
            key={item.href}
            {...item}
            active={isActive(item.href)}
          />
        ))}
      </nav>
      <div className="border-t border-sidebar-border p-4">
        <UserButton appearance={{ elements: { avatarBox: "h-7 w-7" } }} />
      </div>
    </>
  );

  return (
    <QueryProvider>
      <InstallationProvider>
        <ActiveRepoProvider>
        <div className="flex h-screen overflow-hidden">
          {/* Mobile hamburger */}
          <button
            onClick={() => setMobileOpen(true)}
            className="fixed left-4 top-4 z-50 rounded-lg border border-zinc-800 bg-zinc-900 p-2 md:hidden"
            aria-label="Open navigation"
          >
            <Menu className="h-5 w-5 text-zinc-400" />
          </button>

          {/* Mobile overlay + drawer */}
          {mobileOpen && (
            <>
              <div className="fixed inset-0 z-40 bg-black/60 md:hidden" onClick={() => setMobileOpen(false)} />
              <aside className="fixed inset-y-0 left-0 z-50 flex w-64 flex-col border-r border-sidebar-border bg-sidebar md:hidden">
                <button
                  onClick={() => setMobileOpen(false)}
                  className="absolute right-3 top-4 p-1"
                  aria-label="Close navigation"
                >
                  <X className="h-5 w-5 text-zinc-400" />
                </button>
                {sidebarContent}
              </aside>
            </>
          )}

          {/* Desktop sidebar */}
          <aside className="hidden w-56 shrink-0 flex-col border-r border-sidebar-border bg-sidebar md:flex">
            {sidebarContent}
          </aside>

          {/* Main content */}
          <main className="flex-1 overflow-y-auto scroll-smooth bg-background bg-noise">
            <TopBar />
            <div className="mx-auto max-w-6xl px-4 py-4 pt-12 md:px-8 md:pt-4">{children}</div>
          </main>
        </div>
      </ActiveRepoProvider>
      </InstallationProvider>
    </QueryProvider>
  );
}

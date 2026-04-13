"use client";

import { UserButton, OrganizationSwitcher } from "@clerk/nextjs";
import Link from "next/link";
import { OnboardingChecklist } from "@/components/dashboard/onboarding-checklist";
import { usePathname } from "next/navigation";
import {
  LayoutDashboard,
  MessageSquare,
  GitFork,
  Brain,
  Shield,
  Activity,
  Network,
  Settings,
  Users,
  Key,
  CreditCard,
  Menu,
  X,
} from "lucide-react";
import { useState } from "react";
import { QueryProvider } from "@/providers/query-provider";
import { InstallationProvider } from "@/providers/installation-provider";
import { ActiveRepoProvider, useActiveRepo } from "@/providers/active-repo-provider";
import { RepoSelect } from "@/components/dashboard/repo-select";
import { ThemeToggle } from "@/components/dashboard/theme-toggle";

function SidebarRepoSelector() {
  const { repos, activeId, setSelectedId } = useActiveRepo();
  if (!repos.length) return null;
  return (
    <div className="px-3 py-2 border-b border-sidebar-border">
      <label className="block text-[9px] font-mono text-slate-text uppercase tracking-wider mb-1 px-1">Repo</label>
      <RepoSelect repos={repos} value={activeId} onChange={setSelectedId} showAll className="w-full" />
    </div>
  );
}

const orgNavItems = [
  { href: "/dashboard", label: "Overview", icon: LayoutDashboard },
  { href: "/repos", label: "Repos", icon: GitFork },
];

const repoNavItems = [
  { href: "/reviews", label: "Reviews", icon: MessageSquare },
  { href: "/patterns", label: "Patterns", icon: Brain },
  { href: "/scenarios", label: "Scenarios", icon: Shield },
  { href: "/insights", label: "Insights", icon: Activity },
  { href: "/architecture", label: "Architecture", icon: Network },
];

const settingsNavItems = [
  { href: "/team", label: "Team", icon: Users },
  { href: "/providers", label: "Integrations", icon: Key },
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
      className={`flex items-center gap-3 px-3 py-3 text-xs font-mono transition-colors ${
        active
          ? "bg-sidebar-accent text-amber"
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
        <span className="h-1.5 w-1.5 rounded-full bg-amber animate-pulse" />
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
      <SidebarRepoSelector />
      <nav className="flex-1 overflow-y-auto px-3 py-4">
        <div className="space-y-1">
          {orgNavItems.map((item) => (
            <SidebarLink key={item.href} {...item} active={isActive(item.href)} />
          ))}
        </div>
        <div className="my-2 border-t border-sidebar-border" />
        <p className="px-3 pt-1 pb-1.5 text-[9px] font-mono text-slate-text/50 uppercase tracking-wider">Analysis</p>
        <div className="space-y-1">
          {repoNavItems.map((item) => (
            <SidebarLink key={item.href} {...item} active={isActive(item.href)} />
          ))}
        </div>
        <div className="my-2 border-t border-sidebar-border" />
        <div className="space-y-1">
          {settingsNavItems.map((item) => (
            <SidebarLink key={item.href} {...item} active={isActive(item.href)} />
          ))}
        </div>
      </nav>
      <div className="border-t border-sidebar-border px-3 py-3 flex items-center gap-2">
        <div className="flex-1 min-w-0">
          <UserButton
            showName
            appearance={{
              elements: {
                rootBox: "w-full",
                userButtonTrigger: "w-full justify-start gap-2 px-2 py-1.5 rounded-md hover:bg-sidebar-accent transition-colors",
                userButtonAvatarBox: "h-6 w-6",
                userButtonOuterIdentifier: "text-xs font-mono text-slate-text truncate",
              },
            }}
          />
        </div>
        <ThemeToggle />
      </div>
    </>
  );

  return (
    <QueryProvider>
      <InstallationProvider>
        <ActiveRepoProvider>
        <div className="flex h-screen overflow-hidden">
          <a href="#main-content" className="sr-only focus:not-sr-only focus:absolute focus:top-4 focus:left-4 focus:z-[60] focus:px-4 focus:py-2 focus:bg-amber focus:text-void focus:font-mono focus:text-xs">
            Skip to content
          </a>
          {/* Mobile hamburger */}
          <button
            onClick={() => setMobileOpen(true)}
            className="fixed left-4 top-4 z-50 border border-zinc-800 bg-zinc-900 p-3 md:hidden"
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
          <main id="main-content" className="flex-1 overflow-y-auto scroll-smooth bg-background bg-noise">
            <OnboardingChecklist />
            <div className="mx-auto max-w-6xl px-4 py-8 pt-16 md:px-8 md:pt-8">{children}</div>
          </main>
        </div>
      </ActiveRepoProvider>
      </InstallationProvider>
    </QueryProvider>
  );
}

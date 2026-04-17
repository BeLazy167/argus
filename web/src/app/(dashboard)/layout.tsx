"use client";

import { UserButton, OrganizationSwitcher, useUser } from "@clerk/nextjs";
import Image from "next/image";
import Link from "next/link";
import { OnboardingChecklist } from "@/components/dashboard/onboarding-checklist";
import { usePathname } from "next/navigation";
import {
  LayoutGrid,
  GitBranch,
  BarChart3,
  MessageSquare,
  Layers,
  CirclePlay,
  Zap,
  Box,
  Users,
  Puzzle,
  CreditCard,
  Settings,
  Menu,
  X,
  PanelLeftClose,
  PanelLeftOpen,
} from "lucide-react";
import { Suspense, useState } from "react";
import { QueryProvider } from "@/providers/query-provider";
import { InstallationProvider, useInstallation } from "@/providers/installation-provider";
import { ActiveRepoProvider, useActiveRepo } from "@/providers/active-repo-provider";
import { RepoSelect } from "@/components/dashboard/repo-select";
import { ThemeToggle } from "@/components/dashboard/theme-toggle";
import { useSidebarCollapsed } from "@/components/dashboard/sidebar-collapse";

function SidebarRepoSelector({ collapsed }: { collapsed: boolean }) {
  const { repos, activeId, setSelectedId } = useActiveRepo();
  if (!repos.length || collapsed) return null;
  return (
    <div className="px-4 pt-1">
      <SectionLabel>Repo</SectionLabel>
      <RepoSelect repos={repos} value={activeId} onChange={setSelectedId} showAll className="w-full" />
    </div>
  );
}

type NavItem = { href: string; label: string; icon: React.ComponentType<{ className?: string }> };

// Design dashboardv3.pen#C1rzA — icon choices match the design spec.
const NAV_PRIMARY: NavItem[] = [
  { href: "/dashboard", label: "Overview", icon: LayoutGrid },
  { href: "/repos", label: "Repos", icon: GitBranch },
  { href: "/stats", label: "Stats", icon: BarChart3 },
];

const NAV_GROUPS: { label: string; items: NavItem[] }[] = [
  {
    label: "Analysis",
    items: [
      { href: "/architecture", label: "Architecture", icon: Box },
      { href: "/reviews", label: "Reviews", icon: MessageSquare },
      { href: "/patterns", label: "Patterns", icon: Layers },
      { href: "/scenarios", label: "Scenarios", icon: CirclePlay },
      { href: "/insights", label: "Insights", icon: Zap },
    ],
  },
  {
    label: "Workspace",
    items: [
      { href: "/team", label: "Team", icon: Users },
      { href: "/providers", label: "Integrations", icon: Puzzle },
      { href: "/billing", label: "Billing", icon: CreditCard },
      { href: "/settings", label: "Settings", icon: Settings },
    ],
  },
];

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <p className="px-1 pb-2 pt-3 text-[11px] font-mono uppercase tracking-[0.14em] text-slate-text/70">
      {children}
    </p>
  );
}

function SidebarLink({
  href,
  label,
  icon: Icon,
  active,
  collapsed,
}: {
  href: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  active: boolean;
  collapsed: boolean;
}) {
  return (
    <Link
      href={href}
      aria-current={active ? "page" : undefined}
      title={collapsed ? label : undefined}
      className={`relative flex items-center gap-3 rounded-md text-[14px] transition-colors active:scale-[0.98] ${
        collapsed ? "justify-center px-0 py-2.5" : "px-4 py-2.5"
      } ${
        active
          ? "bg-amber/10 text-foreground"
          : "text-slate-text hover:bg-sidebar-accent/60 hover:text-foreground"
      }`}
      style={{ transition: "transform 160ms cubic-bezier(0.23,1,0.32,1), background-color 150ms, color 150ms" }}
    >
      <span
        aria-hidden
        className={`absolute left-0 top-1/2 h-5 w-[3px] -translate-y-1/2 rounded-r-sm transition-colors ${
          active
            ? "bg-amber shadow-[0_0_6px_color-mix(in_oklch,var(--color-amber-glow)_55%,transparent)]"
            : "bg-transparent"
        }`}
      />
      <Icon className="h-[18px] w-[18px] shrink-0" />
      {!collapsed && <span className="truncate">{label}</span>}
    </Link>
  );
}

function UserFooter({ collapsed }: { collapsed: boolean }) {
  const { user } = useUser();
  const { active: installation } = useInstallation();
  const displayName = user?.fullName ?? user?.primaryEmailAddress?.emailAddress ?? "Account";
  const planTier = installation?.plan_tier ?? "Free";
  const planLabel = planTier.charAt(0).toUpperCase() + planTier.slice(1) + " Plan";

  return (
    <div className={`border-t border-sidebar-border ${collapsed ? "px-2 py-3" : "px-4 py-3"}`}>
      <div
        className={`flex items-center rounded-[10px] bg-sidebar-accent/50 ${
          collapsed ? "justify-center py-2" : "gap-2.5 px-2.5 py-2"
        }`}
      >
        <div className="shrink-0">
          <UserButton
            appearance={{
              elements: {
                rootBox: "flex",
                userButtonTrigger:
                  "h-8 w-8 rounded-full ring-1 ring-iron/60 hover:ring-amber/60 transition-[box-shadow] duration-150",
                userButtonAvatarBox: "h-8 w-8",
              },
            }}
          />
        </div>
        {!collapsed && (
          <>
            <div className="min-w-0 flex-1">
              <p className="truncate text-[13px] font-medium text-foreground">{displayName}</p>
              <p className="truncate text-[11px] text-slate-text">{planLabel}</p>
            </div>
            <ThemeToggle />
          </>
        )}
      </div>
    </div>
  );
}

function CollapseToggle({
  collapsed,
  onToggle,
}: {
  collapsed: boolean;
  onToggle: () => void;
}) {
  const Icon = collapsed ? PanelLeftOpen : PanelLeftClose;
  return (
    <button
      type="button"
      onClick={onToggle}
      aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
      title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
      className="flex h-7 w-7 items-center justify-center rounded-md text-slate-text transition-colors hover:bg-sidebar-accent/60 hover:text-foreground active:scale-[0.96]"
      style={{ transition: "transform 160ms cubic-bezier(0.23,1,0.32,1), background-color 150ms, color 150ms" }}
    >
      <Icon className="h-4 w-4" />
    </button>
  );
}

export default function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const pathname = usePathname();
  const [mobileOpen, setMobileOpen] = useState(false);
  const [collapsed, setCollapsed] = useSidebarCollapsed();

  const isActive = (href: string) =>
    pathname === href || (href !== "/dashboard" && pathname.startsWith(href));

  const sidebarContent = (collapsedMode: boolean) => (
    <>
      {/* Header row: logo + collapse toggle. Full-width when expanded; icon-only when collapsed. */}
      <div
        className={`flex items-center ${
          collapsedMode ? "flex-col gap-2 px-2 py-3" : "justify-between px-4 py-2"
        }`}
      >
        <Link
          href="/dashboard"
          aria-label="Argus dashboard"
          className="group flex items-center transition-[filter] duration-200 hover:drop-shadow-[0_0_14px_color-mix(in_oklch,var(--color-amber-glow)_55%,transparent)]"
        >
          <Image
            src="/logo-text.png"
            alt="Argus"
            width={220}
            height={160}
            priority
            sizes={collapsedMode ? "48px" : "200px"}
            className={collapsedMode ? "h-10 w-auto" : "h-20 w-auto"}
          />
        </Link>
        <CollapseToggle collapsed={collapsedMode} onToggle={() => setCollapsed(!collapsedMode)} />
      </div>

      {!collapsedMode && (
        <div className="px-4 pb-4">
          <OrganizationSwitcher
            hidePersonal={false}
            afterSelectOrganizationUrl="/dashboard"
            afterCreateOrganizationUrl="/dashboard"
            appearance={{
              elements: {
                rootBox: "w-full",
                organizationSwitcherTrigger:
                  "w-full justify-between gap-2.5 rounded-lg bg-sidebar-accent/50 px-2.5 py-2 text-[13px] font-medium text-foreground hover:bg-sidebar-accent transition-colors border-0",
                organizationPreviewAvatarBox: "h-5 w-5",
                organizationSwitcherTriggerIcon: "text-slate-text",
              },
            }}
          />
        </div>
      )}

      <div className="h-px bg-sidebar-border" />
      <SidebarRepoSelector collapsed={collapsedMode} />

      {/* Nav groups */}
      <nav className={`flex-1 overflow-y-auto pb-2 pt-2 ${collapsedMode ? "px-2" : "px-2"}`}>
        <div className="space-y-0.5 py-2">
          {NAV_PRIMARY.map((item) => (
            <SidebarLink
              key={item.href}
              {...item}
              active={isActive(item.href)}
              collapsed={collapsedMode}
            />
          ))}
        </div>
        <div className="h-px bg-sidebar-border" />
        {NAV_GROUPS.map((group) => (
          <div key={group.label} className={collapsedMode ? "pt-2" : undefined}>
            {!collapsedMode && <SectionLabel>{group.label}</SectionLabel>}
            <div className="space-y-0.5">
              {group.items.map((item) => (
                <SidebarLink
                  key={item.href}
                  {...item}
                  active={isActive(item.href)}
                  collapsed={collapsedMode}
                />
              ))}
            </div>
            {collapsedMode && <div className="mt-2 h-px bg-sidebar-border" />}
          </div>
        ))}
      </nav>

      <UserFooter collapsed={collapsedMode} />
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
            className="fixed left-4 top-4 z-50 border border-iron bg-charcoal p-3 md:hidden"
            aria-label="Open navigation"
          >
            <Menu className="h-5 w-5 text-slate-text" />
          </button>

          {/* Mobile overlay + drawer — always expanded on mobile */}
          {mobileOpen && (
            <>
              <div className="fixed inset-0 z-40 bg-black/60 md:hidden" onClick={() => setMobileOpen(false)} />
              <aside className="fixed inset-y-0 left-0 z-50 flex w-64 flex-col border-r border-sidebar-border bg-sidebar md:hidden">
                <button
                  onClick={() => setMobileOpen(false)}
                  className="absolute right-3 top-4 p-1"
                  aria-label="Close navigation"
                >
                  <X className="h-5 w-5 text-slate-text" />
                </button>
                {sidebarContent(false)}
              </aside>
            </>
          )}

          {/* Desktop sidebar — width animates between collapsed (64px) and expanded (260px) */}
          <aside
            className={`hidden shrink-0 flex-col border-r border-sidebar-border bg-sidebar md:flex ${
              collapsed ? "w-[64px]" : "w-[260px]"
            }`}
            style={{ transition: "width 220ms cubic-bezier(0.23,1,0.32,1)" }}
          >
            {sidebarContent(collapsed)}
          </aside>

          {/* Main content */}
          <main id="main-content" className="flex-1 overflow-y-auto scroll-smooth bg-background bg-noise">
            <OnboardingChecklist />
            <Suspense>
              <div className="px-4 py-8 pt-16 md:px-8 md:pt-8">{children}</div>
            </Suspense>
          </main>
        </div>
      </ActiveRepoProvider>
      </InstallationProvider>
    </QueryProvider>
  );
}

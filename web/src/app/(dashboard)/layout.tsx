"use client";

import { UserButton } from "@clerk/nextjs";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  LayoutDashboard,
  GitFork,
  MessageSquare,
  ScrollText,
  Settings,
} from "lucide-react";
import { QueryProvider } from "@/providers/query-provider";

const navItems = [
  { href: "/dashboard", label: "Overview", icon: LayoutDashboard },
  { href: "/repos", label: "Repos", icon: GitFork },
  { href: "/reviews", label: "Reviews", icon: MessageSquare },
  { href: "/rules", label: "Rules", icon: ScrollText },
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

  return (
    <QueryProvider>
      <div className="flex h-screen overflow-hidden">
        {/* Sidebar */}
        <aside className="flex w-56 shrink-0 flex-col border-r border-sidebar-border bg-sidebar">
          {/* Wordmark */}
          <div className="flex h-14 items-center border-b border-sidebar-border px-5">
            <Link
              href="/dashboard"
              className="wordmark text-xs text-amber tracking-[0.2em]"
            >
              ARGUS
            </Link>
          </div>

          {/* Nav */}
          <nav className="flex-1 space-y-1 px-3 py-4">
            {navItems.map((item) => (
              <SidebarLink
                key={item.href}
                {...item}
                active={pathname === item.href}
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
        <main className="flex-1 overflow-y-auto bg-background bg-noise">
          <div className="mx-auto max-w-6xl px-8 py-8">{children}</div>
        </main>
      </div>
    </QueryProvider>
  );
}

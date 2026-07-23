import type { ComponentType } from "react";
import {
  LayoutGrid,
  GitBranch,
  BarChart3,
  MessageSquare,
  Brain,
  Users,
  Puzzle,
  CreditCard,
  Settings,
} from "lucide-react";

export type NavItem = {
  href: string;
  label: string;
  icon: ComponentType<{ className?: string }>;
};

export type NavGroup = { label: string; items: NavItem[] };

// Design dashboardv3.pen#C1rzA — icon choices match the design spec.
// Shared by the sidebar (dashboard/layout) and the command menu so navigation
// stays in one place.
export const NAV_PRIMARY: NavItem[] = [
  { href: "/dashboard", label: "Overview", icon: LayoutGrid },
  { href: "/repos", label: "Repos", icon: GitBranch },
  { href: "/stats", label: "Stats", icon: BarChart3 },
];

export const NAV_GROUPS: NavGroup[] = [
  {
    label: "Analysis",
    items: [
      { href: "/memory", label: "Memory", icon: Brain },
      { href: "/reviews", label: "Reviews", icon: MessageSquare },
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

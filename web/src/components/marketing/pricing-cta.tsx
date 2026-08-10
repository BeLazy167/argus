"use client";

import Link from "next/link";
import { track } from "@/lib/analytics";

export function PricingCTA({
  href,
  plan,
  className,
  children,
}: {
  href: string;
  plan: "free" | "pro";
  className: string;
  children: React.ReactNode;
}) {
  return (
    <Link
      href={href}
      className={className}
      onClick={() => track("pricing.signup_clicked", { plan })}
    >
      {children}
    </Link>
  );
}

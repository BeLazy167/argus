"use client";

import { useInstallation } from "@/providers/installation-provider";

export default function BillingPage() {
  const { active } = useInstallation();
  const tier = active?.plan_tier || "free";

  return (
    <>
      <div className="mb-8">
        <h1 className="font-mono text-2xl font-bold text-foreground">Billing</h1>
        <p className="mt-1 text-sm text-slate-text">Manage your subscription and plan.</p>
      </div>

      <div className="border border-iron bg-charcoal p-6">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-lg font-semibold text-white font-mono">
              Current Plan: <span className="text-amber">{tier === "pro" ? "Pro" : "Free"}</span>
            </h2>
            <p className="mt-1 text-sm text-zinc-400 font-mono">
              {tier === "pro"
                ? "Unlimited repos, deep review, all specialists, 500 reviews/month."
                : "3 repos, basic review, 50 reviews/month."}
            </p>
          </div>
          <span
            className={`rounded-full px-3 py-1 text-xs font-mono font-semibold ${
              tier === "pro"
                ? "bg-amber/20 text-amber border border-amber/30"
                : "bg-zinc-800 text-zinc-400 border border-zinc-700"
            }`}
          >
            {tier === "pro" ? "PRO" : "FREE"}
          </span>
        </div>

        {tier === "free" && (
          <div className="mt-6 border border-amber/20 bg-amber/5 p-4">
            <p className="text-sm text-amber font-mono">
              Upgrade to Pro for unlimited repos, deep review with 4 specialists, and 500 reviews/month.
            </p>
            <p className="mt-2 text-xs text-zinc-500 font-mono">
              Contact us to upgrade: support@argus.reviews
            </p>
          </div>
        )}

        <div className="mt-6 grid grid-cols-2 gap-4">
          <div className="bg-charcoal border border-iron p-3">
            <p className="text-xs text-zinc-500 font-mono">Repos</p>
            <p className="text-lg font-semibold text-white font-mono">
              {tier === "pro" ? "Unlimited" : "3 max"}
            </p>
          </div>
          <div className="bg-charcoal border border-iron p-3">
            <p className="text-xs text-zinc-500 font-mono">Reviews/month</p>
            <p className="text-lg font-semibold text-white font-mono">
              {tier === "pro" ? "500" : "50"}
            </p>
          </div>
          <div className="bg-charcoal border border-iron p-3">
            <p className="text-xs text-zinc-500 font-mono">Deep Review</p>
            <p className="text-lg font-semibold text-white font-mono">
              {tier === "pro" ? "Enabled" : "Disabled"}
            </p>
          </div>
          <div className="bg-charcoal border border-iron p-3">
            <p className="text-xs text-zinc-500 font-mono">Specialists</p>
            <p className="text-lg font-semibold text-white font-mono">
              {tier === "pro" ? "4 (all)" : "1 (primary)"}
            </p>
          </div>
        </div>
      </div>
    </>
  );
}

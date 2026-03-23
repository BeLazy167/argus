"use client";
import { Check } from "lucide-react";

const PLANS = [
  {
    name: "Free",
    price: "$0",
    period: "forever",
    features: ["1 repo", "Basic review", "Bring your own API keys"],
    current: true,
    planId: null,
  },
  {
    name: "Pro",
    price: "$29",
    period: "/mo per org",
    features: [
      "Unlimited repos",
      "Deep review (4 specialists)",
      "Custom prompts",
      "Pattern learning",
      "Priority support",
    ],
    current: false,
    planId: "plan_pro",
  },
];

export default function BillingPage() {
  return (
    <>
      <div className="mb-8">
        <h1 className="font-display text-2xl font-bold text-foreground">Billing</h1>
        <p className="text-xs font-mono text-slate-text mt-1">
          Manage your plan and subscription.
        </p>
      </div>

      <div className="grid gap-6 md:grid-cols-2 max-w-2xl">
        {PLANS.map((plan) => (
          <div
            key={plan.name}
            className={`relative flex flex-col rounded-lg border p-6 ${
              plan.current
                ? "border-amber/40 bg-amber/5"
                : "border-iron bg-charcoal"
            }`}
          >
            {plan.current && (
              <span className="absolute -top-3 left-6 rounded-sm bg-amber px-3 py-0.5 text-[10px] font-mono font-medium uppercase tracking-wider text-void">
                Current
              </span>
            )}

            <h3 className="font-display text-lg font-bold text-foreground">
              {plan.name}
            </h3>

            <div className="my-4">
              <span className="font-mono text-3xl font-medium text-foreground">
                {plan.price}
              </span>
              <span className="ml-1 text-xs text-slate-text">{plan.period}</span>
            </div>

            <ul className="flex-1 space-y-3 mb-6">
              {plan.features.map((f) => (
                <li key={f} className="flex items-start gap-2 text-xs text-ash">
                  <Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-amber" />
                  {f}
                </li>
              ))}
            </ul>

            {!plan.current && plan.planId && (
              <button
                type="button"
                onClick={() => {
                  // @ts-expect-error -- Clerk billing API on window
                  window.Clerk?.billing?.startCheckout({
                    planId: plan.planId,
                    planPeriod: "month",
                  });
                }}
                className="inline-flex h-10 items-center justify-center rounded-md bg-amber text-sm font-mono font-medium text-void hover:brightness-110 transition-all"
              >
                Upgrade to {plan.name}
              </button>
            )}
          </div>
        ))}
      </div>
    </>
  );
}

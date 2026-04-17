"use client";
import { useSyncExternalStore, useState } from "react";
import { CheckCircle2, Circle, X, ExternalLink } from "lucide-react";
import Link from "next/link";
import { useInstallation } from "@/providers/installation-provider";
import { useRepos } from "@/lib/queries/repos";
import { useReviews } from "@/lib/queries/reviews";

interface ChecklistStep {
  label: string;
  done: boolean;
  cta?: { label: string; href: string; external?: boolean };
}

const DISMISS_KEY = "onboarding_dismissed";

function subscribeStorage(cb: () => void) {
  window.addEventListener("storage", cb);
  return () => window.removeEventListener("storage", cb);
}
const readDismissed = () => localStorage.getItem(DISMISS_KEY) === "true";
const readDismissedSSR = () => true;

export function OnboardingChecklist() {
  const { active, isLoading } = useInstallation();
  // useSyncExternalStore replaces a mount-time useEffect + state — SSR-safe, storage-synced.
  const [dismissed, setDismissed] = useState(() => {
    try { return readDismissed(); } catch { return true; }
  });
  const storageDismissed = useSyncExternalStore(subscribeStorage, readDismissed, readDismissedSSR);
  const isDismissed = dismissed || storageDismissed;

  const reposQuery = useRepos();
  const repos = reposQuery.data ?? [];
  const enabledRepo = repos.find((r) => r.enabled);
  const reviewsQuery = useReviews({ variables: { repoId: enabledRepo?.id ?? 0, limit: 1, offset: 0 } });
  const hasReviews = (reviewsQuery.data ?? []).length > 0;

  if (isLoading || isDismissed) return null;

  const hasInstallation = active !== null;
  const enabledCount = repos.filter((r) => r.enabled).length;
  const hasEnabledRepo = enabledCount > 0;

  const steps: ChecklistStep[] = [
    { label: "Create account", done: true },
    {
      label: "Install GitHub App",
      done: hasInstallation,
      cta: hasInstallation ? undefined : { label: "Add repos", href: "/repos" },
    },
    {
      label: hasEnabledRepo ? `${enabledCount} repo${enabledCount > 1 ? "s" : ""} enabled` : "Enable a repo",
      done: hasEnabledRepo,
      cta: hasEnabledRepo ? undefined : { label: "Go to repos", href: "/repos" },
    },
    {
      label: "Get your first review",
      done: hasReviews,
      cta: hasReviews ? undefined : { label: "Trigger review", href: "/repos" },
    },
  ];

  const completedCount = steps.filter((s) => s.done).length;
  const allDone = completedCount === steps.length;
  if (allDone) return null;

  const handleDismiss = () => {
    localStorage.setItem(DISMISS_KEY, "true");
    setDismissed(true);
  };

  return (
    <div className="mx-4 mt-4 border border-iron bg-charcoal p-4">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <h3 className="text-sm font-semibold text-white font-mono">Get started with Argus</h3>
          <span className="text-xs text-zinc-400 font-mono">
            {completedCount}/{steps.length}
          </span>
        </div>
        <button
          onClick={handleDismiss}
          aria-label="Dismiss onboarding checklist"
          className="text-zinc-500 hover:text-zinc-300 transition-colors"
        >
          <X className="h-4 w-4" />
        </button>
      </div>
      <div className="space-y-2">
        {steps.map((step) => (
          <div key={step.label} className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              {step.done ? (
                <CheckCircle2 className="h-4 w-4 text-green-500" />
              ) : (
                <Circle className="h-4 w-4 text-zinc-600" />
              )}
              <span
                className={`text-sm font-mono ${step.done ? "text-zinc-400 line-through" : "text-white"}`}
              >
                {step.label}
              </span>
            </div>
            {step.cta && !step.done && (
              step.cta.external ? (
                <a
                  href={step.cta.href}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="flex items-center gap-1 text-xs text-amber-500 hover:text-amber-400 font-mono"
                >
                  {step.cta.label}
                  <ExternalLink className="h-3 w-3" />
                </a>
              ) : (
                <Link
                  href={step.cta.href}
                  className="text-xs text-amber-500 hover:text-amber-400 font-mono"
                >
                  {step.cta.label} &rarr;
                </Link>
              )
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

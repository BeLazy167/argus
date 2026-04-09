"use client";
import { useState, useEffect } from "react";
import { CheckCircle2, Circle, X, ExternalLink } from "lucide-react";
import Link from "next/link";
import { useInstallation } from "@/providers/installation-provider";
import { useApi } from "@/lib/hooks/use-api";

interface ChecklistStep {
  label: string;
  done: boolean;
  cta?: { label: string; href: string; external?: boolean };
}

export function OnboardingChecklist() {
  const { installations, active, isLoading } = useInstallation();
  const [dismissed, setDismissed] = useState(true); // default hidden until loaded
  const [repos, setRepos] = useState<{ enabled: boolean }[]>([]);
  const [hasReviews, setHasReviews] = useState(false);
  const api = useApi();

  useEffect(() => {
    const stored = localStorage.getItem("onboarding_dismissed");
    setDismissed(stored === "true");
  }, []);

  useEffect(() => {
    if (!active) return;
    api.get<any[]>("/api/v1/repos").then((data) => {
      if (Array.isArray(data)) {
        setRepos(data);
        const enabledRepo = data.find((r: any) => r.enabled);
        if (enabledRepo) {
          api
            .get<any[]>(`/api/v1/repos/${enabledRepo.id}/reviews?limit=1&offset=0`)
            .then((reviews) => {
              setHasReviews(Array.isArray(reviews) && reviews.length > 0);
            })
            .catch(() => {});
        }
      }
    }).catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active?.id]);

  if (isLoading || dismissed) return null;

  const hasInstallation = active !== null;
  const enabledCount = repos.filter((r) => r.enabled).length;
  const hasEnabledRepo = enabledCount > 0;

  const steps: ChecklistStep[] = [
    { label: "Create account", done: true },
    {
      label: "Install GitHub App",
      done: hasInstallation,
      cta: hasInstallation
        ? undefined
        : { label: "Add repos", href: "/repos" },
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
    localStorage.setItem("onboarding_dismissed", "true");
    setDismissed(true);
  };

  return (
    <div className="mx-4 mt-4 rounded-lg border border-iron bg-charcoal p-4">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <h3 className="text-sm font-semibold text-white font-mono">Get started with Argus</h3>
          <span className="text-xs text-zinc-400 font-mono">
            {completedCount}/{steps.length}
          </span>
        </div>
        <button
          onClick={handleDismiss}
          className="text-zinc-500 hover:text-zinc-300 transition-colors"
        >
          <X className="h-4 w-4" />
        </button>
      </div>
      <div className="space-y-2">
        {steps.map((step, i) => (
          <div key={i} className="flex items-center justify-between">
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

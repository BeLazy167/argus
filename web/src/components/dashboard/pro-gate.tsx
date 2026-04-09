"use client";

import { useInstallation } from "@/providers/installation-provider";
import { UpgradePrompt } from "./upgrade-prompt";

export function ProGate({
  children,
  feature,
}: {
  children: React.ReactNode;
  feature: string;
}) {
  const { active } = useInstallation();
  const isPro = active?.plan_tier === "pro";

  if (!isPro) {
    return <UpgradePrompt feature={feature} />;
  }

  return <>{children}</>;
}

import Link from "next/link";

export function UpgradePrompt({ feature }: { feature: string }) {
  return (
    <div className="flex flex-col items-center justify-center border border-amber/20 bg-amber/5 p-8 text-center">
      <h3 className="font-mono text-lg font-bold text-foreground mb-2">
        Upgrade to Pro
      </h3>
      <p className="text-sm text-slate-text mb-4">
        {feature} is available on the Pro plan.
      </p>
      <Link
        href="/billing"
        className="border bg-amber px-4 py-2 text-xs font-mono font-medium text-void hover:brightness-110 transition-[filter,box-shadow] duration-150 active:scale-[0.97]"
      >
        View plans
      </Link>
    </div>
  );
}

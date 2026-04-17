"use client";

import { AuthenticateWithRedirectCallback } from "@clerk/nextjs";
import { Loader2 } from "lucide-react";

/**
 * SSO callback landing for the custom sign-up flow. Clerk handles the token exchange
 * and session activation internally; we only render a spinner while that happens.
 *
 * Keep this route static (not part of [[...sign-up]] catch-all) so Clerk's redirect
 * lands predictably regardless of upstream OAuth provider quirks.
 */
export default function SSOCallbackPage() {
  return (
    <div className="flex min-h-svh items-center justify-center bg-void">
      <div className="flex flex-col items-center gap-3">
        <Loader2 className="h-6 w-6 animate-spin text-amber" />
        <p className="font-mono text-[12px] text-slate-text">Finalizing sign-up…</p>
      </div>
      <AuthenticateWithRedirectCallback
        signUpFallbackRedirectUrl="/dashboard"
        signInFallbackRedirectUrl="/dashboard"
      />
    </div>
  );
}

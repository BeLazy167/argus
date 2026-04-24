"use client";

import { useEffect, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useAuth, useSignIn } from "@clerk/nextjs";
import { Loader2, AlertCircle } from "lucide-react";

/**
 * Two-step password reset via Clerk's reset_password_email_code strategy.
 *
 *   Step 1 (enter email):
 *     signIn.create({ strategy: 'reset_password_email_code', identifier })
 *     → Clerk sends a 6-digit code to the user's verified email.
 *
 *   Step 2 (enter code + new password):
 *     signIn.attemptFirstFactor({ strategy: 'reset_password_email_code', code })
 *     → status === 'needs_new_password'
 *     signIn.resetPassword({ password: newPassword })
 *     → status === 'complete' → setActive → /dashboard
 *
 * If the user is already signed in, we skip the form and bounce to
 * /dashboard — same guard as the main sign-in page.
 */
export default function ForgotPasswordPage() {
  const router = useRouter();
  const { isLoaded, signIn, setActive } = useSignIn();
  const { isLoaded: authLoaded, isSignedIn } = useAuth();

  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [stage, setStage] = useState<"request" | "reset">("request");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (authLoaded && isSignedIn) {
      router.replace("/dashboard");
    }
  }, [authLoaded, isSignedIn, router]);

  const surfaceClerkError = (err: unknown): string => {
    if (err && typeof err === "object" && "errors" in err && Array.isArray((err as { errors: unknown[] }).errors)) {
      const first = (err as { errors: { longMessage?: string; message?: string }[] }).errors[0];
      if (first) return first.longMessage ?? first.message ?? "Something went wrong.";
    }
    if (err instanceof Error) return err.message;
    return "Something went wrong.";
  };

  const handleRequestCode = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!isLoaded) return;
    setError(null);
    setSubmitting(true);
    try {
      await signIn.create({ strategy: "reset_password_email_code", identifier: email });
      setStage("reset");
    } catch (err) {
      setError(surfaceClerkError(err));
    } finally {
      setSubmitting(false);
    }
  };

  const handleResetPassword = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!isLoaded) return;
    setError(null);
    setSubmitting(true);
    try {
      const verify = await signIn.attemptFirstFactor({ strategy: "reset_password_email_code", code });
      // Clerk's reset flow goes code → needs_new_password → resetPassword → complete.
      // Handling just the happy-path transition here; other statuses are rare
      // for this strategy and get surfaced to the user via the default message.
      if (verify.status !== "needs_new_password") {
        setError(`Couldn't verify the code (${verify.status ?? "unknown"}). Try requesting a new one.`);
        return;
      }
      const done = await signIn.resetPassword({ password: newPassword });
      if (done.status === "complete") {
        await setActive({ session: done.createdSessionId });
        router.push("/dashboard");
        return;
      }
      setError(`Password reset didn't complete (${done.status ?? "unknown"}). Contact support.`);
    } catch (err) {
      setError(surfaceClerkError(err));
    } finally {
      setSubmitting(false);
    }
  };

  const handleResend = async () => {
    if (!isLoaded) return;
    setError(null);
    try {
      await signIn.create({ strategy: "reset_password_email_code", identifier: email });
    } catch (err) {
      setError(surfaceClerkError(err));
    }
  };

  return (
    <div className="flex min-h-svh flex-col bg-void p-6 md:p-10">
      <div className="flex justify-center">
        <Link href="/" aria-label="Argus home" className="inline-block">
          <Image src="/logo-text.png" alt="Argus" width={220} height={160} priority sizes="170px" className="h-12 w-auto" />
        </Link>
      </div>

      <div className="flex flex-1 flex-col items-center justify-center">
        <div className="w-full max-w-sm">
          {stage === "request" ? (
            <form onSubmit={handleRequestCode} className="flex flex-col gap-5">
              <header className="space-y-1 text-center">
                <h2 className="font-mono text-xl font-bold text-foreground">Reset your password</h2>
                <p className="text-[12px] font-mono text-slate-text">
                  Enter your email and we&apos;ll send you a 6-digit code.
                </p>
              </header>

              <label className="flex flex-col gap-1.5" htmlFor="email">
                <span className="font-mono text-[11px] uppercase tracking-[0.14em] text-slate-text">Email</span>
                <input
                  id="email"
                  type="email"
                  required
                  autoComplete="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="you@example.com"
                  className="h-10 w-full rounded-md border border-iron bg-charcoal px-3 font-mono text-[13px] text-foreground placeholder:text-iron focus:border-amber focus:outline-none focus:ring-1 focus:ring-amber/40"
                />
              </label>

              {error && (
                <div
                  role="alert"
                  className="flex items-start gap-2 rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2 font-mono text-[12px] text-red-400"
                >
                  <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                  <span className="flex-1">{error}</span>
                </div>
              )}

              <button
                type="submit"
                disabled={submitting || !email}
                className="inline-flex h-10 items-center justify-center rounded-md bg-amber px-4 font-mono text-[13px] font-semibold text-primary-foreground transition-colors hover:bg-amber-glow disabled:opacity-50 active:scale-[0.98]"
                style={{ transition: "transform 160ms cubic-bezier(0.23,1,0.32,1), background-color 150ms" }}
              >
                {submitting ? (
                  <>
                    <Loader2 className="mr-2 h-3.5 w-3.5 animate-spin" />
                    Sending code…
                  </>
                ) : (
                  "Send code"
                )}
              </button>

              <p className="text-center text-[12px] font-mono text-slate-text">
                <Link href="/sign-in" className="text-amber transition-colors hover:text-amber-glow">
                  &larr; Back to sign in
                </Link>
              </p>
            </form>
          ) : (
            <form onSubmit={handleResetPassword} className="flex flex-col gap-5">
              <header className="space-y-1 text-center">
                <h2 className="font-mono text-xl font-bold text-foreground">Enter code + new password</h2>
                <p className="text-[12px] font-mono text-slate-text">
                  We sent a 6-digit code to <span className="text-foreground">{email}</span>.
                </p>
              </header>

              <label className="flex flex-col gap-1.5" htmlFor="code">
                <span className="font-mono text-[11px] uppercase tracking-[0.14em] text-slate-text">Verification code</span>
                <input
                  id="code"
                  type="text"
                  required
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  maxLength={6}
                  value={code}
                  onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
                  placeholder="123456"
                  className="h-10 w-full rounded-md border border-iron bg-charcoal px-3 font-mono text-[15px] tracking-[0.3em] text-foreground placeholder:text-iron focus:border-amber focus:outline-none focus:ring-1 focus:ring-amber/40"
                />
              </label>

              <label className="flex flex-col gap-1.5" htmlFor="newPassword">
                <span className="font-mono text-[11px] uppercase tracking-[0.14em] text-slate-text">New password</span>
                <input
                  id="newPassword"
                  type="password"
                  required
                  autoComplete="new-password"
                  minLength={8}
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                  className="h-10 w-full rounded-md border border-iron bg-charcoal px-3 font-mono text-[13px] text-foreground placeholder:text-iron focus:border-amber focus:outline-none focus:ring-1 focus:ring-amber/40"
                />
              </label>

              {error && (
                <div
                  role="alert"
                  className="flex items-start gap-2 rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2 font-mono text-[12px] text-red-400"
                >
                  <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                  <span className="flex-1">{error}</span>
                </div>
              )}

              <button
                type="submit"
                disabled={submitting || code.length < 6 || newPassword.length < 8}
                className="inline-flex h-10 items-center justify-center rounded-md bg-amber px-4 font-mono text-[13px] font-semibold text-primary-foreground transition-colors hover:bg-amber-glow disabled:opacity-50 active:scale-[0.98]"
                style={{ transition: "transform 160ms cubic-bezier(0.23,1,0.32,1), background-color 150ms" }}
              >
                {submitting ? (
                  <>
                    <Loader2 className="mr-2 h-3.5 w-3.5 animate-spin" />
                    Resetting…
                  </>
                ) : (
                  "Reset password and sign in"
                )}
              </button>

              <div className="flex items-center justify-between">
                <button
                  type="button"
                  onClick={() => {
                    setStage("request");
                    setCode("");
                    setNewPassword("");
                    setError(null);
                  }}
                  className="font-mono text-[11px] text-slate-text/80 transition-colors hover:text-amber"
                >
                  &larr; Change email
                </button>
                <button
                  type="button"
                  onClick={handleResend}
                  className="font-mono text-[11px] text-slate-text/80 transition-colors hover:text-amber"
                >
                  Resend code
                </button>
              </div>
            </form>
          )}
        </div>
      </div>
    </div>
  );
}

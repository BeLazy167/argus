"use client";

import { useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useAuth, useSignIn, useUser, useClerk } from "@clerk/nextjs";
import {
  Brain,
  GitPullRequest,
  Key,
  Sparkles,
  Loader2,
  AlertCircle,
  Github,
  ArrowRight,
  LogOut,
} from "lucide-react";
import { ShaderAnimation } from "@/components/ui/shader-lines";
import { useMediaQuery } from "@/lib/hooks/use-media-query";
import { describeClerkError, type ClerkErrorInfo } from "@/lib/clerk-errors";

const VALUE_PROPS = [
  {
    icon: GitPullRequest,
    title: "AI review on every PR",
    body: "Installs as a GitHub App in 60 seconds. Triage → 4-specialist deep review → synthesis, posted as a structured GitHub review.",
  },
  {
    icon: Brain,
    title: "Memory compounds per repo",
    body: "Every review teaches Argus patterns and risks specific to your codebase. The next review is smarter than the last.",
  },
  {
    icon: Sparkles,
    title: "50 free reviews / month",
    body: "Free forever for small teams. Pro is $19/mo per workspace — unlimited reviews, custom personas, priority support.",
  },
  {
    icon: Key,
    title: "BYOK — your key, your model",
    body: "Bring your own OpenAI, Anthropic, or OpenRouter key. Argus never trains on your code and never stores your source.",
  },
];

/**
 * Custom sign-in form backed by Clerk's useSignIn hook. Two-panel layout mirroring /sign-up.
 * Sign-in is single-step for password auth — email + password → setActive → redirect.
 * GitHub OAuth redirects through /sign-in/sso-callback.
 */
// EmailCodeFactor narrows the Clerk SignInFirstFactor union to the
// email_code strategy. emailAddressId + safeIdentifier are present only
// on this variant; narrowing here keeps the lookup below type-safe.
type EmailCodeFactor = {
  strategy: "email_code";
  emailAddressId: string;
  safeIdentifier: string;
};

// needs_client_trust is a newer Clerk sign-in status that the currently
// installed @clerk/nextjs types don't list in the SignInStatus union.
// Comparing status (narrowly typed) directly to the literal trips TS2367.
// isStatus() widens to string so the comparison type-checks without
// weakening the rest of the type-checked status handling.
const isStatus = (status: unknown, want: string): boolean => status === want;

export default function SignInPage() {
  const router = useRouter();
  const { isLoaded, signIn, setActive } = useSignIn();
  const { isLoaded: authLoaded, isSignedIn } = useAuth();
  const { user } = useUser();
  const { signOut } = useClerk();
  const isLg = useMediaQuery("(min-width: 1024px)");

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<ClerkErrorInfo | null>(null);
  const [submitting, setSubmitting] = useState(false);
  // When Clerk returns needs_client_trust after password auth, we prepare
  // an email_code first factor and switch the form to code entry. Null
  // here means the password form is visible; non-null means the code
  // form is. codeSent tracks the transient "we just resent" confirmation.
  const [codeFlow, setCodeFlow] = useState<{ emailAddressId: string } | null>(null);
  const [code, setCode] = useState("");
  const [codeSent, setCodeSent] = useState(false);
  const [switchingAccount, setSwitchingAccount] = useState(false);

  // A session already in the browser (own tab left open, shared machine,
  // stale dev/prod switch) used to silently router.replace() to /dashboard.
  // That dropped a colleague onto someone else's account with no signal
  // of what happened. Clerk is single-session by default, so calling
  // signIn.create() or authenticateWithRedirect() with a live session
  // also throws code "session_exists" with longMessage "You're already
  // signed in." — stranding the user. The SignedInCard branch below
  // replaces both failure modes with an explicit choice.
  const handleContinue = () => router.replace("/dashboard");
  const handleSwitchAccount = async () => {
    setSwitchingAccount(true);
    // signOut with no args clears the current (only) session in a
    // single-session app. Once it resolves, isSignedIn flips false and
    // this branch unmounts — the password/OAuth form re-renders, giving
    // the natural state separation Clerk's docs require between signOut
    // and the next signIn.
    await signOut();
  };

  // describeIncompleteStatus maps a non-"complete" Clerk sign-in status to
  // a ClerkErrorInfo. needs_client_trust is handled separately (kicks off
  // the email_code flow). Remaining statuses either have a dedicated page
  // (needs_new_password → reset flow) or no in-app UI yet.
  const describeIncompleteStatus = (status: string | null): ClerkErrorInfo => {
    switch (status) {
      case "needs_first_factor":
        return { message: "Password didn't match. Check it and try again." };
      case "needs_second_factor":
        return {
          message: "Two-factor authentication is required. We don't have a 2FA entry form yet — sign in with GitHub above.",
        };
      case "needs_new_password":
        return {
          message: "Your password has expired.",
          action: { label: "Set a new one", href: "/sign-in/forgot-password" },
        };
      case "needs_identifier":
        return { message: "Email is missing. Fill in your email and try again." };
      default:
        return {
          message: `Sign-in didn't complete (${status ?? "unknown"}). Sign in with GitHub above, or contact support.`,
        };
    }
  };

  // Looks up the email_code first factor on the current SignIn resource
  // and asks Clerk to send the one-time code. Returns the emailAddressId
  // so the caller can transition the UI into code-entry state.
  const sendEmailCode = async (): Promise<string | null> => {
    if (!signIn) return null;
    const factor = signIn.supportedFirstFactors?.find(
      (f): f is EmailCodeFactor => f.strategy === "email_code",
    );
    if (!factor) {
      setError({
        message: "Email verification isn't available for this account. Sign in with GitHub above.",
      });
      return null;
    }
    await signIn.prepareFirstFactor({ strategy: "email_code", emailAddressId: factor.emailAddressId });
    return factor.emailAddressId;
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!isLoaded) return;
    setError(null);
    setSubmitting(true);
    try {
      const attempt = await signIn.create({ identifier: email, password });
      if (attempt.status === "complete") {
        await setActive({ session: attempt.createdSessionId });
        router.push("/dashboard");
        return;
      }
      if (isStatus(attempt.status, "needs_client_trust")) {
        // Clerk's device-trust gate. Resolve in-app by sending a code to
        // the account email; the user types it in the next step.
        const emailAddressId = await sendEmailCode();
        if (emailAddressId) {
          setCodeFlow({ emailAddressId });
        }
        return;
      }
      setError(describeIncompleteStatus(attempt.status));
    } catch (err) {
      setError(describeClerkError(err));
    } finally {
      setSubmitting(false);
    }
  };

  const handleVerifyCode = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!isLoaded || !codeFlow) return;
    setError(null);
    setSubmitting(true);
    try {
      const result = await signIn.attemptFirstFactor({ strategy: "email_code", code });
      if (result.status === "complete") {
        await setActive({ session: result.createdSessionId });
        router.push("/dashboard");
        return;
      }
      setError(describeIncompleteStatus(result.status));
    } catch (err) {
      setError(describeClerkError(err));
    } finally {
      setSubmitting(false);
    }
  };

  const handleResendCode = async () => {
    if (!isLoaded || !codeFlow) return;
    setError(null);
    setCodeSent(false);
    try {
      await signIn.prepareFirstFactor({ strategy: "email_code", emailAddressId: codeFlow.emailAddressId });
      setCodeSent(true);
    } catch (err) {
      setError(describeClerkError(err));
    }
  };

  const handleCancelCode = () => {
    setCodeFlow(null);
    setCode("");
    setCodeSent(false);
    setError(null);
  };

  const handleGitHub = async () => {
    if (!isLoaded) return;
    setError(null);
    try {
      await signIn.authenticateWithRedirect({
        strategy: "oauth_github",
        redirectUrl: "/sign-in/sso-callback",
        redirectUrlComplete: "/dashboard",
      });
    } catch (err) {
      setError(describeClerkError(err));
    }
  };

  return (
    <div className="grid min-h-svh lg:grid-cols-[2fr_3fr] bg-void">
      {/* Left: shader + brand panel — shader only mounts on lg+ to skip the rAF loop on mobile */}
      <aside className="relative hidden overflow-hidden bg-void lg:flex lg:flex-col lg:justify-between">
        {isLg && <ShaderAnimation />}
        <div className="absolute inset-0 bg-gradient-to-tr from-void/85 via-void/55 to-void/25" />

        <div className="relative z-10 flex flex-col gap-10 p-10 xl:p-14">
          <Link href="/" aria-label="Argus home" className="inline-block">
            <Image
              src="/logo-text.png"
              alt="Argus"
              width={220}
              height={160}
              priority
              sizes="170px"
              className="h-14 w-auto drop-shadow-[0_0_18px_rgba(10,6,18,0.9)]"
            />
          </Link>
          <div className="max-w-md">
            <p className="mb-3 text-[11px] font-mono uppercase tracking-[0.18em] text-amber">
              Welcome back
            </p>
            <h1 className="font-display text-4xl font-bold leading-tight text-foreground">
              Review smarter,
              <br />
              not harder.
            </h1>
            <p className="mt-4 text-sm font-mono text-ash/90 leading-relaxed">
              AI code review that traces dependencies, remembers incidents, and simulates failures before they ship.
            </p>
          </div>
        </div>

        <ul className="relative z-10 space-y-4 p-10 xl:p-14">
          {VALUE_PROPS.map((p) => (
            <li key={p.title} className="flex items-start gap-3 max-w-md">
              <span className="mt-0.5 inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-amber/40 bg-void/60 text-amber backdrop-blur-sm">
                <p.icon className="h-4 w-4" />
              </span>
              <div>
                <h2 className="text-sm font-mono text-foreground">{p.title}</h2>
                <p className="text-[12px] font-mono text-ash/80 leading-relaxed">
                  {p.body}
                </p>
              </div>
            </li>
          ))}
        </ul>
      </aside>

      {/* Right: custom sign-in form */}
      <div className="flex flex-col gap-4 p-6 md:p-10">
        <div className="flex justify-center lg:hidden">
          <Link href="/" aria-label="Argus home" className="inline-block">
            <Image src="/logo-text.png" alt="Argus" width={220} height={160} priority sizes="170px" className="h-12 w-auto" />
          </Link>
        </div>

        <div className="flex flex-1 flex-col items-center justify-center">
          <div className="w-full max-w-sm">
            {!authLoaded ? (
              <div className="flex min-h-[360px] flex-col items-center justify-center gap-3">
                <Loader2 className="h-5 w-5 animate-spin text-amber" />
                <p className="font-mono text-[11px] text-slate-text">Loading…</p>
              </div>
            ) : isSignedIn && user ? (
              <div className="flex flex-col gap-5">
                <header className="space-y-1 text-center">
                  <h2 className="font-mono text-xl font-bold text-foreground">You&apos;re already signed in</h2>
                  <p className="text-[12px] font-mono text-slate-text">
                    Continue to your dashboard, or sign out to use a different account.
                  </p>
                </header>

                <div className="flex items-center gap-3 rounded-md border border-iron bg-charcoal px-3 py-3">
                  {user.imageUrl ? (
                    <Image
                      src={user.imageUrl}
                      alt=""
                      width={40}
                      height={40}
                      className="h-10 w-10 shrink-0 rounded-full border border-iron/60"
                    />
                  ) : (
                    <span
                      aria-hidden
                      className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full border border-iron/60 bg-void font-mono text-[13px] text-amber"
                    >
                      {(user.fullName ?? user.primaryEmailAddress?.emailAddress ?? "?").charAt(0).toUpperCase()}
                    </span>
                  )}
                  <div className="min-w-0 flex-1">
                    {user.fullName && (
                      <p className="truncate font-mono text-[13px] text-foreground">{user.fullName}</p>
                    )}
                    <p className="truncate font-mono text-[11px] text-slate-text">
                      {user.primaryEmailAddress?.emailAddress ?? "No primary email"}
                    </p>
                  </div>
                </div>

                <button
                  type="button"
                  onClick={handleContinue}
                  className="inline-flex h-10 items-center justify-center gap-2 rounded-md bg-amber px-4 font-mono text-[13px] font-semibold text-primary-foreground transition-colors hover:bg-amber-glow active:scale-[0.98]"
                  style={{ transition: "transform 160ms cubic-bezier(0.23,1,0.32,1), background-color 150ms" }}
                >
                  Continue to dashboard
                  <ArrowRight className="h-3.5 w-3.5" />
                </button>

                <button
                  type="button"
                  onClick={handleSwitchAccount}
                  disabled={switchingAccount}
                  className="inline-flex h-10 items-center justify-center gap-2 rounded-md border border-iron bg-charcoal font-mono text-[12px] text-slate-text transition-colors hover:border-amber/40 hover:text-foreground disabled:opacity-50 active:scale-[0.98]"
                  style={{ transition: "transform 160ms cubic-bezier(0.23,1,0.32,1), background-color 150ms, border-color 150ms, color 150ms" }}
                >
                  {switchingAccount ? (
                    <>
                      <Loader2 className="h-3.5 w-3.5 animate-spin" />
                      Signing out…
                    </>
                  ) : (
                    <>
                      <LogOut className="h-3.5 w-3.5" />
                      Sign out and use a different account
                    </>
                  )}
                </button>
              </div>
            ) : codeFlow ? (
              <form onSubmit={handleVerifyCode} className="flex flex-col gap-5">
                <header className="space-y-1 text-center">
                  <h2 className="font-mono text-xl font-bold text-foreground">Check your email</h2>
                  <p className="text-[12px] font-mono text-slate-text">
                    We sent a 6-digit code to <span className="text-foreground">{email}</span>. Enter it below to finish signing in.
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

                {codeSent && !error && (
                  <p className="text-[11px] font-mono text-emerald-400">New code sent. Check your inbox.</p>
                )}

                {error && (
                  <div
                    role="alert"
                    className="flex items-start gap-2 rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2 font-mono text-[12px] text-red-400"
                  >
                    <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                    <span className="flex-1">
                      {error.message}
                      {error.action && (
                        <>
                          {" "}
                          <Link href={error.action.href} className="underline underline-offset-2 hover:text-red-300">
                            {error.action.label}
                          </Link>
                          .
                        </>
                      )}
                    </span>
                  </div>
                )}

                <button
                  type="submit"
                  disabled={submitting || code.length < 6}
                  className="inline-flex h-10 items-center justify-center rounded-md bg-amber px-4 font-mono text-[13px] font-semibold text-primary-foreground transition-colors hover:bg-amber-glow disabled:opacity-50 active:scale-[0.98]"
                  style={{ transition: "transform 160ms cubic-bezier(0.23,1,0.32,1), background-color 150ms" }}
                >
                  {submitting ? (
                    <>
                      <Loader2 className="mr-2 h-3.5 w-3.5 animate-spin" />
                      Verifying…
                    </>
                  ) : (
                    "Verify and sign in"
                  )}
                </button>

                <div className="flex items-center justify-between">
                  <button
                    type="button"
                    onClick={handleCancelCode}
                    className="font-mono text-[11px] text-slate-text/80 transition-colors hover:text-amber"
                  >
                    &larr; Back
                  </button>
                  <button
                    type="button"
                    onClick={handleResendCode}
                    className="font-mono text-[11px] text-slate-text/80 transition-colors hover:text-amber"
                  >
                    Resend code
                  </button>
                </div>
              </form>
            ) : (
              <form onSubmit={handleSubmit} className="flex flex-col gap-5">
                <header className="space-y-1 text-center">
                  <h2 className="font-mono text-xl font-bold text-foreground">Sign in to Argus</h2>
                  <p className="text-[12px] font-mono text-slate-text">
                    Welcome back. Pick up where you left off.
                  </p>
                </header>

                <button
                  type="button"
                  onClick={handleGitHub}
                  disabled={submitting}
                  className="group relative inline-flex h-10 items-center justify-center gap-2 rounded-md border border-iron bg-charcoal font-mono text-[13px] text-foreground transition-colors hover:border-amber/50 hover:bg-iron/30 disabled:opacity-50 active:scale-[0.98]"
                  style={{ transition: "transform 160ms cubic-bezier(0.23,1,0.32,1), background-color 150ms, border-color 150ms" }}
                >
                  <Github className="h-4 w-4" />
                  Continue with GitHub
                </button>

                <div className="relative my-1 flex items-center gap-3">
                  <span className="h-px flex-1 bg-iron/60" />
                  <span className="font-mono text-[10px] uppercase tracking-[0.18em] text-slate-text">or</span>
                  <span className="h-px flex-1 bg-iron/60" />
                </div>

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

                <label className="flex flex-col gap-1.5" htmlFor="password">
                  <span className="flex items-baseline justify-between">
                    <span className="font-mono text-[11px] uppercase tracking-[0.14em] text-slate-text">Password</span>
                    <Link href="/sign-in/forgot-password" className="font-mono text-[10px] text-slate-text/80 hover:text-amber">
                      Forgot?
                    </Link>
                  </span>
                  <input
                    id="password"
                    type="password"
                    required
                    autoComplete="current-password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    className="h-10 w-full rounded-md border border-iron bg-charcoal px-3 font-mono text-[13px] text-foreground placeholder:text-iron focus:border-amber focus:outline-none focus:ring-1 focus:ring-amber/40"
                  />
                </label>

                <div id="clerk-captcha" className="min-h-0" />

                {error && (
                  <div
                    role="alert"
                    className="flex items-start gap-2 rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2 font-mono text-[12px] text-red-400"
                  >
                    <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                    <span className="flex-1">
                      {error.message}
                      {error.action && (
                        <>
                          {" "}
                          <Link href={error.action.href} className="underline underline-offset-2 hover:text-red-300">
                            {error.action.label}
                          </Link>
                          .
                        </>
                      )}
                    </span>
                  </div>
                )}

                <button
                  type="submit"
                  disabled={submitting || !email || !password}
                  className="inline-flex h-10 items-center justify-center rounded-md bg-amber px-4 font-mono text-[13px] font-semibold text-primary-foreground transition-colors hover:bg-amber-glow disabled:opacity-50 active:scale-[0.98]"
                  style={{ transition: "transform 160ms cubic-bezier(0.23,1,0.32,1), background-color 150ms" }}
                >
                  {submitting ? (
                    <>
                      <Loader2 className="mr-2 h-3.5 w-3.5 animate-spin" />
                      Signing in…
                    </>
                  ) : (
                    "Sign in"
                  )}
                </button>
              </form>
            )}

            {authLoaded && !isSignedIn && (
              <p className="mt-6 text-center text-[12px] font-mono text-slate-text">
                Don&apos;t have an account?{" "}
                <Link href="/sign-up" className="text-amber transition-colors hover:text-amber-glow">
                  Sign up
                </Link>
              </p>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

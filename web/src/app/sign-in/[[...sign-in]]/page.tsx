"use client";

import { useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useSignIn } from "@clerk/nextjs";
import { Brain, GitPullRequest, Key, Sparkles, Loader2, AlertCircle, Github } from "lucide-react";
import { ShaderAnimation } from "@/components/ui/shader-lines";
import { useMediaQuery } from "@/lib/hooks/use-media-query";

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
export default function SignInPage() {
  const router = useRouter();
  const { isLoaded, signIn, setActive } = useSignIn();
  const isLg = useMediaQuery("(min-width: 1024px)");

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const surfaceClerkError = (err: unknown): string => {
    if (err && typeof err === "object" && "errors" in err && Array.isArray((err as { errors: unknown[] }).errors)) {
      const first = (err as { errors: { longMessage?: string; message?: string }[] }).errors[0];
      if (first) return first.longMessage ?? first.message ?? "Something went wrong.";
    }
    if (err instanceof Error) return err.message;
    return "Something went wrong.";
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
      setError(`Sign-in incomplete (${attempt.status}). Try again or reset your password.`);
    } catch (err) {
      setError(surfaceClerkError(err));
    } finally {
      setSubmitting(false);
    }
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
      setError(surfaceClerkError(err));
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
                  <span className="flex-1">{error}</span>
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

            <p className="mt-6 text-center text-[12px] font-mono text-slate-text">
              Don&apos;t have an account?{" "}
              <Link href="/sign-up" className="text-amber transition-colors hover:text-amber-glow">
                Sign up
              </Link>
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}

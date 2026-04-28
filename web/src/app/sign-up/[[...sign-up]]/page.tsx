"use client";

import { useEffect, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useAuth, useSignUp } from "@clerk/nextjs";
import { Brain, GitPullRequest, Key, Sparkles, Loader2, AlertCircle, Github } from "lucide-react";
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
 * Custom sign-up form backed by Clerk's useSignUp hook. Two steps:
 *   1. Email + password (or GitHub OAuth) → Clerk creates the signup + sends verification code
 *   2. 6-digit verification code → Clerk finalizes, setActive(), redirect to /dashboard
 *
 * Clerk still handles session, token, and OAuth — we only own the markup.
 */
export default function SignUpPage() {
  const router = useRouter();
  const { isLoaded, signUp, setActive } = useSignUp();
  const { isLoaded: authLoaded, isSignedIn } = useAuth();
  // Only mount the WebGL shader on lg+ — on narrow viewports the aside is display:none
  // anyway, but the component would still initialize and tick an rAF loop off-screen,
  // draining battery. Gate the mount to avoid that.
  const isLg = useMediaQuery("(min-width: 1024px)");

  // Bounce already-signed-in users to the dashboard. Same reason as the
  // sign-in page: Clerk enforces one session per browser, so landing here
  // with a live session means clicking "Continue with GitHub" would throw
  // "You're already signed in" with no recovery path.
  useEffect(() => {
    if (authLoaded && isSignedIn) {
      router.replace("/dashboard");
    }
  }, [authLoaded, isSignedIn, router]);

  const [step, setStep] = useState<"form" | "verify">("form");
  const [firstName, setFirstName] = useState("");
  const [lastName, setLastName] = useState("");
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [error, setError] = useState<ClerkErrorInfo | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!isLoaded) return;
    setError(null);
    setSubmitting(true);
    try {
      await signUp.create({
        firstName,
        lastName,
        username,
        emailAddress: email,
        password,
      });
      await signUp.prepareEmailAddressVerification({ strategy: "email_code" });
      setStep("verify");
    } catch (err) {
      setError(describeClerkError(err));
    } finally {
      setSubmitting(false);
    }
  };

  const handleVerify = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!isLoaded) return;
    setError(null);
    setSubmitting(true);
    try {
      const attempt = await signUp.attemptEmailAddressVerification({ code });
      if (attempt.status === "complete") {
        await setActive({ session: attempt.createdSessionId });
        router.push("/dashboard");
        return;
      }
      setError({ message: `Verification incomplete (${attempt.status}). Please check the code.` });
    } catch (err) {
      setError(describeClerkError(err));
    } finally {
      setSubmitting(false);
    }
  };

  const handleGitHub = async () => {
    if (!isLoaded) return;
    setError(null);
    try {
      await signUp.authenticateWithRedirect({
        strategy: "oauth_github",
        redirectUrl: "/sign-up/sso-callback",
        redirectUrlComplete: "/dashboard",
      });
    } catch (err) {
      setError(describeClerkError(err));
    }
  };

  return (
    <div className="grid min-h-svh lg:grid-cols-[2fr_3fr] bg-void">
      {/* Left: shader + value props — unchanged layout, now 40/60 split */}
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
              Get started
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

      {/* Right: custom form */}
      <div className="flex flex-col gap-4 p-6 md:p-10">
        <div className="flex justify-center lg:hidden">
          <Link href="/" aria-label="Argus home" className="inline-block">
            <Image src="/logo-text.png" alt="Argus" width={220} height={160} priority sizes="170px" className="h-12 w-auto" />
          </Link>
        </div>

        <div className="flex flex-1 flex-col items-center justify-center">
          <div className="w-full max-w-sm">
            {step === "form" ? (
              <CreateForm
                firstName={firstName}
                lastName={lastName}
                username={username}
                email={email}
                password={password}
                error={error}
                submitting={submitting}
                onFirstName={setFirstName}
                onLastName={setLastName}
                onUsername={setUsername}
                onEmail={setEmail}
                onPassword={setPassword}
                onSubmit={handleCreate}
                onGitHub={handleGitHub}
              />
            ) : (
              <VerifyForm
                email={email}
                code={code}
                error={error}
                submitting={submitting}
                onCode={setCode}
                onSubmit={handleVerify}
                onBack={() => setStep("form")}
              />
            )}

            <p className="mt-6 text-center text-[12px] font-mono text-slate-text">
              Already have an account?{" "}
              <Link href="/sign-in" className="text-amber transition-colors hover:text-amber-glow">
                Sign in
              </Link>
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}

/* ── Step 1: create signup ─────────────────────────────────── */

function CreateForm({
  firstName,
  lastName,
  username,
  email,
  password,
  error,
  submitting,
  onFirstName,
  onLastName,
  onUsername,
  onEmail,
  onPassword,
  onSubmit,
  onGitHub,
}: {
  firstName: string;
  lastName: string;
  username: string;
  email: string;
  password: string;
  error: ClerkErrorInfo | null;
  submitting: boolean;
  onFirstName: (v: string) => void;
  onLastName: (v: string) => void;
  onUsername: (v: string) => void;
  onEmail: (v: string) => void;
  onPassword: (v: string) => void;
  onSubmit: (e: React.FormEvent) => void;
  onGitHub: () => void;
}) {
  const canSubmit = firstName.trim() && lastName.trim() && username.trim() && email && password.length >= 8;
  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-5">
      <header className="space-y-1 text-center">
        <h2 className="font-mono text-xl font-bold text-foreground">Create your Argus account</h2>
        <p className="text-[12px] font-mono text-slate-text">
          Start with 50 free reviews per month.
        </p>
      </header>

      <button
        type="button"
        onClick={onGitHub}
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

      <div className="grid grid-cols-2 gap-3">
        <Field label="First name" htmlFor="firstName">
          <input
            id="firstName"
            type="text"
            required
            autoComplete="given-name"
            value={firstName}
            onChange={(e) => onFirstName(e.target.value)}
            placeholder="Ada"
            className={inputClass}
          />
        </Field>
        <Field label="Last name" htmlFor="lastName">
          <input
            id="lastName"
            type="text"
            required
            autoComplete="family-name"
            value={lastName}
            onChange={(e) => onLastName(e.target.value)}
            placeholder="Lovelace"
            className={inputClass}
          />
        </Field>
      </div>

      <Field label="Username" htmlFor="username">
        <input
          id="username"
          type="text"
          required
          minLength={3}
          autoComplete="username"
          value={username}
          onChange={(e) => onUsername(e.target.value)}
          placeholder="alovelace"
          className={inputClass}
        />
      </Field>

      <Field label="Email" htmlFor="email">
        <input
          id="email"
          type="email"
          required
          autoComplete="email"
          value={email}
          onChange={(e) => onEmail(e.target.value)}
          placeholder="you@example.com"
          className={inputClass}
        />
      </Field>

      <Field label="Password" htmlFor="password" hint="8+ characters">
        <input
          id="password"
          type="password"
          required
          minLength={8}
          autoComplete="new-password"
          value={password}
          onChange={(e) => onPassword(e.target.value)}
          className={inputClass}
        />
      </Field>

      {/* Clerk's bot-detection element — required by the SDK. Keep it mounted. */}
      <div id="clerk-captcha" className="min-h-0" />

      {error && <ErrorBanner info={error} />}

      <button
        type="submit"
        disabled={submitting || !canSubmit}
        className="inline-flex h-10 items-center justify-center rounded-md bg-amber px-4 font-mono text-[13px] font-semibold text-primary-foreground transition-colors hover:bg-amber-glow disabled:opacity-50 active:scale-[0.98]"
        style={{ transition: "transform 160ms cubic-bezier(0.23,1,0.32,1), background-color 150ms" }}
      >
        {submitting ? (
          <>
            <Loader2 className="mr-2 h-3.5 w-3.5 animate-spin" />
            Creating account…
          </>
        ) : (
          "Create account"
        )}
      </button>

      <p className="text-center text-[10px] font-mono text-slate-text/80 leading-relaxed">
        By signing up you agree to our{" "}
        <Link href="/terms" className="text-amber hover:text-amber-glow">Terms</Link>{" "}
        and{" "}
        <Link href="/privacy" className="text-amber hover:text-amber-glow">Privacy Policy</Link>.
      </p>
    </form>
  );
}

const inputClass =
  "h-10 w-full rounded-md border border-iron bg-charcoal px-3 font-mono text-[13px] text-foreground placeholder:text-iron focus:border-amber focus:outline-none focus:ring-1 focus:ring-amber/40";

/* ── Step 2: email verification code ──────────────────────── */

function VerifyForm({
  email,
  code,
  error,
  submitting,
  onCode,
  onSubmit,
  onBack,
}: {
  email: string;
  code: string;
  error: ClerkErrorInfo | null;
  submitting: boolean;
  onCode: (v: string) => void;
  onSubmit: (e: React.FormEvent) => void;
  onBack: () => void;
}) {
  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-5">
      <header className="space-y-1 text-center">
        <h2 className="font-mono text-xl font-bold text-foreground">Check your email</h2>
        <p className="text-[12px] font-mono text-slate-text">
          We sent a 6-digit code to <span className="text-foreground">{email}</span>.
        </p>
      </header>

      <Field label="Verification code" htmlFor="code">
        <input
          id="code"
          type="text"
          inputMode="numeric"
          pattern="[0-9]*"
          maxLength={6}
          required
          autoFocus
          autoComplete="one-time-code"
          value={code}
          onChange={(e) => onCode(e.target.value.replace(/\D/g, ""))}
          placeholder="000000"
          className="h-12 w-full rounded-md border border-iron bg-charcoal px-3 text-center font-mono text-xl tracking-[0.4em] text-foreground placeholder:text-iron/50 focus:border-amber focus:outline-none focus:ring-1 focus:ring-amber/40"
        />
      </Field>

      {error && <ErrorBanner info={error} />}

      <button
        type="submit"
        disabled={submitting || code.length !== 6}
        className="inline-flex h-10 items-center justify-center rounded-md bg-amber px-4 font-mono text-[13px] font-semibold text-primary-foreground transition-colors hover:bg-amber-glow disabled:opacity-50 active:scale-[0.98]"
        style={{ transition: "transform 160ms cubic-bezier(0.23,1,0.32,1), background-color 150ms" }}
      >
        {submitting ? (
          <>
            <Loader2 className="mr-2 h-3.5 w-3.5 animate-spin" />
            Verifying…
          </>
        ) : (
          "Verify & continue"
        )}
      </button>

      <button
        type="button"
        onClick={onBack}
        className="font-mono text-[12px] text-slate-text hover:text-foreground transition-colors"
      >
        ← Use a different email
      </button>
    </form>
  );
}

/* ── Shared bits ──────────────────────────────────────────── */

function Field({
  label,
  htmlFor,
  hint,
  children,
}: {
  label: string;
  htmlFor: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <label className="flex flex-col gap-1.5" htmlFor={htmlFor}>
      <span className="flex items-baseline justify-between">
        <span className="font-mono text-[11px] uppercase tracking-[0.14em] text-slate-text">
          {label}
        </span>
        {hint && (
          <span className="font-mono text-[10px] text-slate-text/70">{hint}</span>
        )}
      </span>
      {children}
    </label>
  );
}

function ErrorBanner({ info }: { info: ClerkErrorInfo }) {
  return (
    <div
      role="alert"
      className="flex items-start gap-2 rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2 font-mono text-[12px] text-red-400"
    >
      <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      <span className="flex-1">
        {info.message}
        {info.action && (
          <>
            {" "}
            <Link href={info.action.href} className="underline underline-offset-2 hover:text-red-300">
              {info.action.label}
            </Link>
            .
          </>
        )}
      </span>
    </div>
  );
}

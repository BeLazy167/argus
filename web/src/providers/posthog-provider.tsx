"use client";

import posthog from "posthog-js";
import { PostHogProvider as PHProvider } from "posthog-js/react";
import { Suspense, useEffect } from "react";
import { useAuth, useUser } from "@clerk/nextjs";
import { usePathname, useSearchParams } from "next/navigation";
import { getLastTraceId } from "@/lib/fetch-with-trace";
import { useInstallation } from "@/providers/installation-provider";

const POSTHOG_KEY = process.env.NEXT_PUBLIC_POSTHOG_PROJECT_TOKEN;
const POSTHOG_HOST = process.env.NEXT_PUBLIC_POSTHOG_HOST || "https://us.i.posthog.com";

/**
 * Routes where no session recording should ever run (marketing + auth).
 * Dashboard routes opt-in via `startSessionRecording` in the effect below.
 */
const isRecordableRoute = (pathname: string | null): boolean => {
  if (!pathname) return false;
  if (pathname === "/") return false;
  if (pathname.startsWith("/sign-in") || pathname.startsWith("/sign-up")) return false;
  return true;
};

if (typeof window !== "undefined" && POSTHOG_KEY) {
  posthog.init(POSTHOG_KEY, {
    api_host: POSTHOG_HOST,
    capture_pageview: false, // we handle manually
    capture_pageleave: true,
    persistence: "localStorage",
    session_recording: {
      maskAllInputs: false,
      maskTextSelector: ".ph-mask, pre, code, [data-phx-mask]",
      blockSelector: ".ph-block",
      // 10% sample rate for the first 2 weeks while the mask coverage is
      // verified — flip to 1.0 after the mask audit window.
      sampleRate: 0.1,
    },
    // Start with recording on; the per-route effect below stops it on
    // marketing/auth routes.
    disable_session_recording: false,
    capture_exceptions: true,
  });
}

function PostHogPageView() {
  const pathname = usePathname();
  const searchParams = useSearchParams();

  useEffect(() => {
    if (!pathname || !POSTHOG_KEY) return;
    const url = window.origin + pathname + (searchParams?.toString() ? `?${searchParams}` : "");
    posthog.capture("$pageview", { $current_url: url });
  }, [pathname, searchParams]);

  return null;
}

/**
 * Per-route session-recording gating. Recording is a liability on marketing
 * pages (GDPR surface, low product value) — stop it there, start it once
 * the user is inside the app shell.
 */
function SessionRecordingGate() {
  const pathname = usePathname();

  useEffect(() => {
    if (!POSTHOG_KEY) return;
    if (isRecordableRoute(pathname)) {
      posthog.startSessionRecording();
    } else {
      posthog.stopSessionRecording();
    }
  }, [pathname]);

  return null;
}

function PostHogIdentify() {
  const { isSignedIn } = useAuth();
  const { user } = useUser();

  useEffect(() => {
    if (!POSTHOG_KEY) return;
    if (isSignedIn && user) {
      // Alias BEFORE identify so webhook-side events keyed by
      // `github:<login>` merge into this Clerk user's person.
      const gh = user.externalAccounts.find(
        (a) => a.verification?.strategy === "oauth_github",
      );
      if (gh?.username) {
        posthog.alias(`github:${gh.username}`, user.id);
      }
      posthog.identify(user.id, {
        email: user.primaryEmailAddress?.emailAddress,
        name: user.fullName,
      });
    } else {
      posthog.reset();
    }
  }, [isSignedIn, user]);

  return null;
}

/**
 * Attaches the active GitHub installation as a PostHog group so event
 * aggregations (cost, error rate, funnel) can be filtered per-workspace.
 * Runs as a child component so `useInstallation` (which requires the
 * InstallationProvider in the tree above it) is available only where we
 * expect it (inside the dashboard route group).
 */
export function PostHogGroupAssociation() {
  const { active } = useInstallation();

  useEffect(() => {
    if (!POSTHOG_KEY || !active) return;
    posthog.group("installation", String(active.id), {
      org_login: active.org_login,
      plan_tier: active.plan_tier,
    });
  }, [active]);

  return null;
}

/**
 * Window-level error + unhandled-rejection forwarding. Covers crashes
 * outside a React tree or before an `error.tsx` boundary can catch.
 */
function PostHogGlobalHandlers() {
  useEffect(() => {
    if (!POSTHOG_KEY) return;
    const onError = (e: ErrorEvent): void => {
      posthog.captureException(e.error ?? new Error(e.message), {
        trace_id: getLastTraceId(),
      });
    };
    const onReject = (e: PromiseRejectionEvent): void => {
      posthog.captureException(
        e.reason instanceof Error ? e.reason : new Error(String(e.reason)),
        { trace_id: getLastTraceId() },
      );
    };
    window.addEventListener("error", onError);
    window.addEventListener("unhandledrejection", onReject);
    return () => {
      window.removeEventListener("error", onError);
      window.removeEventListener("unhandledrejection", onReject);
    };
  }, []);

  return null;
}

export function PostHogProvider({ children }: { children: React.ReactNode }) {
  if (!POSTHOG_KEY) return <>{children}</>;

  return (
    <PHProvider client={posthog}>
      <Suspense fallback={null}>
        <PostHogPageView />
      </Suspense>
      <SessionRecordingGate />
      <PostHogIdentify />
      <PostHogGlobalHandlers />
      {children}
    </PHProvider>
  );
}

export { posthog };

"use client";
import { Suspense, useEffect, useRef, useState } from "react";
import { Loader2, AlertCircle, CheckCircle2 } from "lucide-react";
import { useSearchParams, useRouter } from "next/navigation";
import { useAuth, useOrganization } from "@clerk/nextjs";
import { QueryProvider } from "@/providers/query-provider";
import { useLinkInstallation } from "@/lib/queries/installations";

function CallbackInner() {
  const params = useSearchParams();
  const router = useRouter();
  const { isSignedIn, isLoaded } = useAuth();
  const { organization } = useOrganization();
  const linkMutation = useLinkInstallation();
  const hasLinked = useRef(false);
  const [status, setStatus] = useState<"loading" | "success" | "error">("loading");
  const [errorMsg, setErrorMsg] = useState("");
  const retryCount = useRef(0);

  const doLink = () => {
    const installationId = params.get("installation_id");
    if (!installationId) {
      setStatus("error");
      setErrorMsg("Missing installation ID. Please try installing the GitHub App again.");
      return;
    }

    linkMutation.mutate(
      {
        installationId: Number(installationId),
        clerkOrgId: organization?.id,
      },
      {
        onSuccess: () => {
          setStatus("success");
          setTimeout(() => router.push("/repos"), 1500);
        },
        onError: (err) => {
          // Retry once after 2s — webhook may not have arrived yet
          if (retryCount.current < 2) {
            retryCount.current++;
            setTimeout(doLink, 2000);
            return;
          }
          setStatus("error");
          setErrorMsg(
            err instanceof Error ? err.message : "Failed to connect GitHub. Please try again."
          );
        },
      },
    );
  };

  useEffect(() => {
    if (!isLoaded || hasLinked.current) return;

    if (!isSignedIn) {
      const installationId = params.get("installation_id");
      router.push(`/sign-in?redirect_url=/github/callback?installation_id=${installationId}`);
      return;
    }

    hasLinked.current = true;
    doLink();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isLoaded, isSignedIn]);

  return (
    <div className="flex h-screen items-center justify-center bg-background">
      <div className="text-center space-y-4 max-w-md">
        {status === "loading" && (
          <>
            <Loader2 className="h-8 w-8 animate-spin text-amber-500 mx-auto" />
            <p className="text-sm text-slate-text font-mono">Connecting your GitHub repos...</p>
            {retryCount.current > 0 && (
              <p className="text-xs text-zinc-500">Waiting for GitHub to sync...</p>
            )}
          </>
        )}
        {status === "success" && (
          <>
            <CheckCircle2 className="h-8 w-8 text-green-500 mx-auto" />
            <p className="text-sm text-slate-text font-mono">Connected! Redirecting to repos...</p>
          </>
        )}
        {status === "error" && (
          <>
            <AlertCircle className="h-8 w-8 text-red-500 mx-auto" />
            <p className="text-sm text-red-400 font-mono">{errorMsg}</p>
            <div className="flex gap-3 justify-center mt-4">
              <button
                onClick={() => {
                  setStatus("loading");
                  retryCount.current = 0;
                  hasLinked.current = false;
                  doLink();
                }}
                className="px-4 py-2 bg-amber-600 text-white rounded-md text-sm font-mono hover:bg-amber-500"
              >
                Try again
              </button>
              <button
                onClick={() => router.push("/repos")}
                className="px-4 py-2 bg-zinc-700 text-white rounded-md text-sm font-mono hover:bg-zinc-600"
              >
                Go to repos
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

export default function GitHubCallbackPage() {
  return (
    <QueryProvider>
      <Suspense
        fallback={
          <div className="flex h-screen items-center justify-center">
            <Loader2 className="h-5 w-5 animate-spin text-zinc-500" />
          </div>
        }
      >
        <CallbackInner />
      </Suspense>
    </QueryProvider>
  );
}

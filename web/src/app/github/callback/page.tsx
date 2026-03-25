"use client";
import { Suspense, useEffect, useRef } from "react";
import { Loader2 } from "lucide-react";
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

  useEffect(() => {
    if (!isLoaded || hasLinked.current) return;
    const installationId = params.get("installation_id");
    if (!installationId || !isSignedIn) {
      router.push("/dashboard");
      return;
    }
    hasLinked.current = true;
    linkMutation.mutate(
      {
        installationId: Number(installationId),
        clerkOrgId: organization?.id,
      },
      {
        onSuccess: () => router.push("/repos"),
        onError: () => router.push("/dashboard"),
      },
    );
  }, [isLoaded, isSignedIn, params, linkMutation, organization, router]);

  return (
    <div className="flex h-screen items-center justify-center bg-background">
      <p className="text-sm text-slate-text font-mono">Linking GitHub installation...</p>
    </div>
  );
}

export default function GitHubCallbackPage() {
  return (
    <QueryProvider>
      <Suspense fallback={<div className="flex h-screen items-center justify-center"><Loader2 className="h-5 w-5 animate-spin text-zinc-500" /></div>}>
        <CallbackInner />
      </Suspense>
    </QueryProvider>
  );
}

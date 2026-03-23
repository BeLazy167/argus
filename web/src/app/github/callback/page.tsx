"use client";
import { Suspense, useEffect } from "react";
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

  useEffect(() => {
    if (!isLoaded) return;
    const installationId = params.get("installation_id");
    if (!installationId || !isSignedIn) {
      router.push("/dashboard");
      return;
    }
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
  }, [isLoaded, isSignedIn, params]);

  return (
    <div className="flex h-screen items-center justify-center bg-background">
      <p className="text-sm text-slate-text font-mono">Linking GitHub installation...</p>
    </div>
  );
}

export default function GitHubCallbackPage() {
  return (
    <QueryProvider>
      <Suspense>
        <CallbackInner />
      </Suspense>
    </QueryProvider>
  );
}

import { Loader2 } from "lucide-react";

export default function MarketingLoading() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-black">
      <Loader2 className="h-6 w-6 animate-spin text-amber-500" />
    </div>
  );
}

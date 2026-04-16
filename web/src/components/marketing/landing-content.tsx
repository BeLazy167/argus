import { Hero } from "@/components/marketing/sections/hero";
import { Memory } from "@/components/marketing/sections/memory";
import { Simulation } from "@/components/marketing/sections/simulation";
import { Byok } from "@/components/marketing/sections/byok";
import { Open } from "@/components/marketing/sections/open";
import { InstallCta } from "@/components/marketing/sections/install-cta";

export function LandingContent() {
  return (
    <div className="bg-background text-foreground">
      <Hero />
      <Memory />
      <Simulation />
      <Byok />
      <Open />
      <InstallCta />
    </div>
  );
}

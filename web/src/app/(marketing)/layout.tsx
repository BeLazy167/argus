import { Navbar } from "@/components/marketing/navbar";
import { ScrollProgress } from "@/components/marketing/scroll-progress";
import { OrganizationJsonLd, SoftwareAppJsonLd } from "@/components/seo/json-ld";

export default function MarketingLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <>
      <ScrollProgress />
      <header>
        <Navbar />
      </header>
      <main className="pt-24">{children}</main>
      <SoftwareAppJsonLd />
      <OrganizationJsonLd />
    </>
  );
}

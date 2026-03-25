import { Navbar } from "@/components/marketing/navbar";
import { SoftwareAppJsonLd } from "@/components/seo/json-ld";

export default function MarketingLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <>
      <header>
        <Navbar />
      </header>
      <main className="pt-14">{children}</main>
      <SoftwareAppJsonLd />
    </>
  );
}

export function SoftwareAppJsonLd() {
  const schema = {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    name: "ARGUS",
    applicationCategory: "DeveloperApplication",
    operatingSystem: "Web",
    description:
      "AI-powered code review that builds institutional memory. Traces dependencies, remembers incidents, and simulates failures before they ship.",
    url: "https://argusai.vercel.app",
    offers: [
      { "@type": "Offer", price: "0", priceCurrency: "USD", name: "Free" },
      {
        "@type": "Offer",
        price: "19",
        priceCurrency: "USD",
        name: "Pro",
      },
    ],
  };

  return (
    <script
      type="application/ld+json"
      dangerouslySetInnerHTML={{ __html: JSON.stringify(schema) }}
    />
  );
}

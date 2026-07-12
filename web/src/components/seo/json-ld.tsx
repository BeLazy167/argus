function JsonLd({ schema }: { schema: Record<string, unknown> }) {
	return (
		<script
			type="application/ld+json"
			dangerouslySetInnerHTML={{ __html: JSON.stringify(schema) }}
		/>
	);
}

export function SoftwareAppJsonLd() {
	return (
		<JsonLd
			schema={{
				"@context": "https://schema.org",
				"@type": "SoftwareApplication",
				name: "ARGUS",
				applicationCategory: "DeveloperApplication",
				operatingSystem: "Web",
				description:
					"AI-powered code review that builds institutional memory. Traces dependencies, remembers incidents, and simulates failures before they ship.",
				url: "https://argus.reviews",
				offers: [
					{ "@type": "Offer", price: "0", priceCurrency: "USD", name: "Free" },
					{
						"@type": "Offer",
						price: "19",
						priceCurrency: "USD",
						name: "Pro",
					},
				],
			}}
		/>
	);
}

export function OrganizationJsonLd() {
	return (
		<JsonLd
			schema={{
				"@context": "https://schema.org",
				"@type": "Organization",
				name: "Argus",
				url: "https://argus.reviews",
				logo: "https://argus.reviews/logo-text.png",
				sameAs: ["https://github.com/BeLazy167/argus", "https://x.com/belazyaf"],
			}}
		/>
	);
}

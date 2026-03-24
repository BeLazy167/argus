import { PricingTable } from '@clerk/nextjs'

export default function BillingPage() {
  return (
    <>
      <div className="mb-8">
        <h1 className="font-display text-2xl font-bold text-foreground">Billing</h1>
        <p className="mt-1 text-sm text-slate-text">Manage your subscription and plan.</p>
      </div>
      <PricingTable for="organization" />
    </>
  )
}

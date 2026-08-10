import Link from "next/link";
import { AlertCircle } from "lucide-react";
import type { ClerkErrorInfo } from "@/lib/clerk-errors";

export const inputClass =
	"h-10 w-full rounded-md border border-iron bg-charcoal px-3 font-mono text-[13px] text-foreground placeholder:text-iron focus:border-amber focus:outline-none focus:ring-1 focus:ring-amber/40";

export function Field({
	label,
	htmlFor,
	hint,
	children,
}: {
	label: string;
	htmlFor: string;
	hint?: string;
	children: React.ReactNode;
}) {
	return (
		<label className="flex flex-col gap-1.5" htmlFor={htmlFor}>
			<span className="flex items-baseline justify-between">
				<span className="font-mono text-[11px] uppercase tracking-[0.14em] text-slate-text">
					{label}
				</span>
				{hint && <span className="font-mono text-[10px] text-slate-text/70">{hint}</span>}
			</span>
			{children}
		</label>
	);
}

export function ErrorBanner({ info }: { info: ClerkErrorInfo }) {
	return (
		<div
			role="alert"
			className="flex items-start gap-2 rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2 font-mono text-[12px] text-red-400"
		>
			<AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
			<span className="flex-1">
				{info.message}
				{info.action && (
					<>
						{" "}
						<Link
							href={info.action.href}
							className="underline underline-offset-2 hover:text-red-300"
						>
							{info.action.label}
						</Link>
						.
					</>
				)}
			</span>
		</div>
	);
}

/**
 * Single-line numeric one-time-code entry used by the sign-in email-code
 * step and the forgot-password reset step. Both render identical markup:
 * a labelled 6-digit input that strips non-digits on change. `value` is the
 * controlled code string; `onChange` receives the already-sanitized digits.
 */
export function CodeInput({
	value,
	onChange,
}: {
	value: string;
	onChange: (digits: string) => void;
}) {
	return (
		<label className="flex flex-col gap-1.5" htmlFor="code">
			<span className="font-mono text-[11px] uppercase tracking-[0.14em] text-slate-text">
				Verification code
			</span>
			<input
				id="code"
				type="text"
				required
				inputMode="numeric"
				autoComplete="one-time-code"
				maxLength={6}
				value={value}
				onChange={(e) => onChange(e.target.value.replace(/\D/g, ""))}
				placeholder="123456"
				className="h-10 w-full rounded-md border border-iron bg-charcoal px-3 font-mono text-[15px] tracking-[0.3em] text-foreground placeholder:text-iron focus:border-amber focus:outline-none focus:ring-1 focus:ring-amber/40"
			/>
		</label>
	);
}

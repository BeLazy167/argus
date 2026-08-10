import { Cpu, Info, Loader2, Sliders, UserCog, Zap } from "lucide-react";
import Link from "next/link";
import { useState } from "react";
import { track } from "@/lib/analytics";
import { type FeatureFlags, useFeatureFlags, useSaveFeatureFlags } from "@/lib/queries/features";
import {
	useDeleteOrgModelConfig,
	useOrgModelConfigs,
	useUpsertOrgModelConfig,
} from "@/lib/queries/model-configs";
import { useOrgDefaults, useSaveOrgDefaults } from "@/lib/queries/org-defaults";
import { useInstallation } from "@/providers/installation-provider";
import { ConfigCard } from "./config-card";
import { AUTO_RESOLVE_TOGGLE, AUTO_RUN_TOGGLE, CORE_STAGES, PIPELINE_FEATURES } from "./constants";
import { PERSONAS, PersonaCard } from "./persona-card";
import { PipelineFeatureCard, VerificationToggle } from "./toggle-cards";

/**
 * "Org Defaults" settings tab. Owns every org-scoped query/mutation it needs;
 * the parent passes only the list of providers that have a saved API key
 * (used to gate model selection). Conditionally rendered by the parent, so its
 * local draft state resets on tab switch exactly as before.
 */
export function OrgDefaultsTab({ savedProviders }: { savedProviders: string[] }) {
	const { active } = useInstallation();
	const { data: orgDefaults, isLoading: orgDefaultsLoading } = useOrgDefaults();
	const saveOrgDefaults = useSaveOrgDefaults();
	const { data: featureFlags } = useFeatureFlags();
	const saveFeatureFlags = useSaveFeatureFlags();

	const { data: orgConfigs } = useOrgModelConfigs();
	const upsertOrgConfig = useUpsertOrgModelConfig();
	const deleteOrgConfig = useDeleteOrgModelConfig();
	const orgConfigMap = new Map(orgConfigs?.map((c) => [c.stage, c]));

	const orgPersona = (orgDefaults?.persona as string) || "default";
	const orgCustomPrompt = (orgDefaults?.custom_persona_prompt as string) || "";
	const [orgCustomPromptDraft, setOrgCustomPromptDraft] = useState(orgCustomPrompt);

	const configuredCount = savedProviders.length;

	if (!active || orgDefaultsLoading) {
		return (
			<div className="flex items-center justify-center py-20">
				<Loader2 className="h-6 w-6 animate-spin text-slate-text" />
			</div>
		);
	}

	return (
		<div className="space-y-10">
			<div className="border border-amber/20 bg-amber/5 px-4 py-3 flex items-start gap-2.5">
				<Info className="h-3.5 w-3.5 text-amber mt-0.5 shrink-0" />
				<p className="text-[11px] font-mono text-amber/80">
					These defaults apply to all repos in{" "}
					<span className="text-amber font-medium">{active?.org_login}</span>. Repos can override
					individual settings on the &quot;Repo Overrides&quot; tab.
				</p>
			</div>

			{/* Org: Persona */}
			<section>
				<div className="flex items-center gap-3 mb-4">
					<div className="flex items-center gap-2">
						<UserCog className="h-4 w-4 text-amber" />
						<h2 className="font-mono text-lg font-semibold text-foreground">Default Persona</h2>
					</div>
				</div>
				<div className="grid gap-3 grid-cols-1 sm:grid-cols-2 lg:grid-cols-3">
					{PERSONAS.map((p) => (
						<PersonaCard
							key={p.value}
							persona={p}
							isActive={orgPersona === p.value}
							onSelect={() => {
								saveOrgDefaults.mutate({ ...orgDefaults, persona: p.value });
							}}
							disabled={saveOrgDefaults.isPending}
						/>
					))}
				</div>
				{orgPersona === "custom" && (
					<div className="mt-4 border border-iron bg-charcoal p-4">
						<label className="block text-[11px] font-mono text-slate-text mb-2">
							Custom persona prompt (org default)
						</label>
						<textarea
							value={orgCustomPromptDraft}
							onChange={(e) => setOrgCustomPromptDraft(e.target.value)}
							placeholder="e.g. You are a reviewer focused on accessibility and i18n…"
							rows={5}
							className="w-full border border-iron bg-void px-4 py-3 text-xs font-mono text-foreground placeholder:text-slate-text/40 focus:outline-none focus:border-amber/50 transition-colors resize-y"
						/>
						<button
							type="button"
							disabled={saveOrgDefaults.isPending || orgCustomPromptDraft === orgCustomPrompt}
							onClick={() => {
								saveOrgDefaults.mutate({
									...orgDefaults,
									persona: "custom",
									custom_persona_prompt: orgCustomPromptDraft,
								});
							}}
							className="mt-2 rounded border border-amber/30 bg-amber/10 px-3 py-1.5 text-[11px] font-mono font-medium text-amber hover:bg-amber/20 disabled:opacity-50 disabled:cursor-not-allowed transition-colors cursor-pointer"
						>
							{saveOrgDefaults.isPending ? "Saving..." : "Save Custom Persona"}
						</button>
					</div>
				)}
			</section>

			{/* Org: Review Pipeline */}
			<section>
				<div className="flex items-center gap-3 mb-4">
					<div className="flex items-center gap-2">
						<Cpu className="h-4 w-4 text-amber" />
						<h2 className="font-mono text-lg font-semibold text-foreground">Review Pipeline</h2>
					</div>
				</div>
				<p className="text-xs font-mono text-slate-text mb-4">
					Configure the default model for each review stage. Applies to all repos unless overridden.
				</p>

				{configuredCount === 0 ? (
					<div className="border border-iron/50 bg-iron/10 px-4 py-3 flex items-start gap-2.5">
						<Info className="h-3.5 w-3.5 text-slate-text mt-0.5 shrink-0" />
						<p className="text-[11px] font-mono text-slate-text">
							No API keys configured yet.{" "}
							<Link
								href="/providers"
								className="text-amber underline underline-offset-2 hover:text-foreground transition-colors"
							>
								Add an API key
							</Link>{" "}
							to unlock provider selection.
						</p>
					</div>
				) : (
					<div className="space-y-4">
						<div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
							{CORE_STAGES.map((stage) => (
								<ConfigCard
									key={`org-${stage}`}
									stage={stage}
									repoId={0}
									existing={orgConfigMap.get(stage)}
									savedProviders={savedProviders}
									installationId={active?.id}
									onSave={(data) => upsertOrgConfig.mutate(data)}
									onDelete={(s) => deleteOrgConfig.mutate({ stage: s })}
								/>
							))}
						</div>
					</div>
				)}
			</section>

			{/* Org: Auto-review (not pro-gated — cost control) */}
			<section>
				<div className="flex items-center gap-3 mb-4">
					<div className="flex items-center gap-2">
						<Zap className="h-4 w-4 text-amber" />
						<h2 className="font-mono text-lg font-semibold text-foreground">Auto-review</h2>
					</div>
				</div>
				<p className="text-xs font-mono text-slate-text mb-4">
					Default for all repos in {active?.org_login}. Turn off to require opt-in per PR via a
					checkbox comment with cost preview.
				</p>
				<PipelineFeatureCard
					toggle={AUTO_RUN_TOGGLE}
					enabled={
						typeof orgDefaults?.auto_run === "boolean"
							? orgDefaults.auto_run
							: AUTO_RUN_TOGGLE.defaultValue
					}
					pending={saveOrgDefaults.isPending}
					onToggle={() => {
						const current =
							typeof orgDefaults?.auto_run === "boolean"
								? orgDefaults.auto_run
								: AUTO_RUN_TOGGLE.defaultValue;
						track("settings.toggle_changed", { setting_key: "auto_run", new_value: !current });
						saveOrgDefaults.mutate({ ...orgDefaults, auto_run: !current });
					}}
				/>
				<PipelineFeatureCard
					toggle={AUTO_RESOLVE_TOGGLE}
					enabled={
						typeof orgDefaults?.auto_resolve_enabled === "boolean"
							? orgDefaults.auto_resolve_enabled
							: AUTO_RESOLVE_TOGGLE.defaultValue
					}
					pending={saveOrgDefaults.isPending}
					onToggle={() => {
						const current =
							typeof orgDefaults?.auto_resolve_enabled === "boolean"
								? orgDefaults.auto_resolve_enabled
								: AUTO_RESOLVE_TOGGLE.defaultValue;
						track("settings.toggle_changed", {
							setting_key: "auto_resolve_enabled",
							new_value: !current,
						});
						saveOrgDefaults.mutate({ ...orgDefaults, auto_resolve_enabled: !current });
					}}
				/>
			</section>

			{/* Org: Pipeline Features */}
			<section>
				<div className="flex items-center gap-3 mb-4">
					<div className="flex items-center gap-2">
						<Sliders className="h-4 w-4 text-amber" />
						<h2 className="font-mono text-lg font-semibold text-foreground">Pipeline Features</h2>
					</div>
				</div>
				<div className="grid gap-3 md:grid-cols-2">
					{PIPELINE_FEATURES.map((toggle) => {
						const isSimulation = toggle.key === "simulation";
						const orgDrVal = orgDefaults?.["deep_review"];
						const orgDrEnabled = typeof orgDrVal === "boolean" ? orgDrVal : false;

						if (isSimulation) {
							const smVal = orgDefaults?.["scenario_memory"];
							const csVal = orgDefaults?.["code_simulation"];
							const enabled =
								(typeof smVal === "boolean" ? smVal : false) &&
								(typeof csVal === "boolean" ? csVal : false);
							return (
								<PipelineFeatureCard
									key={toggle.key}
									toggle={toggle}
									enabled={enabled}
									disabled={!orgDrEnabled}
									pending={saveOrgDefaults.isPending}
									onToggle={() => {
										saveOrgDefaults.mutate({
											...orgDefaults,
											scenario_memory: !enabled,
											code_simulation: !enabled,
										});
									}}
								/>
							);
						}

						const val = orgDefaults?.[toggle.key];
						const enabled = typeof val === "boolean" ? val : toggle.defaultValue;
						return (
							<PipelineFeatureCard
								key={toggle.key}
								toggle={toggle}
								enabled={enabled}
								pending={saveOrgDefaults.isPending}
								onToggle={() => {
									const updates: Record<string, unknown> = {
										...orgDefaults,
										[toggle.key]: !enabled,
									};
									if (toggle.key === "deep_review" && enabled) {
										updates.scenario_memory = false;
										updates.code_simulation = false;
									}
									track("settings.toggle_changed", {
										setting_key: toggle.key,
										new_value: !enabled,
									});
									saveOrgDefaults.mutate(updates);
								}}
							/>
						);
					})}
				</div>
			</section>

			{/* Org: Verification Features (issue acceptance + cross-repo PR) */}
			<section>
				<div className="flex items-center gap-3 mb-4">
					<div className="flex items-center gap-2">
						<Sliders className="h-4 w-4 text-amber" />
						<h2 className="font-mono text-lg font-semibold text-foreground">
							Verification Features
						</h2>
					</div>
					<span className="text-[11px] font-mono text-slate-text">
						Per-installation toggles for optional review passes
					</span>
				</div>
				<div className="grid gap-3 md:grid-cols-2">
					<VerificationToggle
						label="Issue acceptance check"
						hint="Verify PRs against linked issue acceptance criteria"
						description="Auto-detects linked issues via GitHub's closingIssuesReferences (PR body `Closes #N` OR the Development UI panel). Extracts criteria from issue body and uses an LLM judge to classify each criterion as addressed/partial/unaddressed/ambiguous."
						cost="+1 LLM call per linked issue (~1-2k tokens)"
						enabled={featureFlags?.issue_acceptance ?? true}
						pending={saveFeatureFlags.isPending}
						onToggle={() => {
							const next: FeatureFlags = {
								issue_acceptance: !(featureFlags?.issue_acceptance ?? true),
								cross_pr_checks: featureFlags?.cross_pr_checks ?? false,
								max_linked_prs: featureFlags?.max_linked_prs ?? 5,
							};
							track("settings.toggle_changed", {
								setting_key: "issue_acceptance",
								new_value: next.issue_acceptance,
							});
							saveFeatureFlags.mutate(next);
						}}
					/>
					<VerificationToggle
						label="Cross-repo PR checks"
						hint="Cross-PR risk judge + joint issue coverage, run async after review"
						description="Auto-detects GitHub PR URLs in your PR body (up to max_linked_prs). Runs asynchronously after review completion. Two stages: (1) combination-risk judge probing 9 categories (schema race, serialization, deploy ordering, security posture, enum exhaustiveness, and more) against each linked PR's diff + prior findings; (2) joint issue coverage when 2+ linked PRs share an issue. Sibling completion triggers a debounced refresh so late-arriving PRs update earlier ones. Inaccessible repos show 'partial coverage' — severity unaffected."
						cost="1–5 LLM calls per review depending on linked-PR + shared-issue count. Bounded: per-install 30/hour, per-PR 2 refreshes/10 min."
						enabled={featureFlags?.cross_pr_checks ?? false}
						pending={saveFeatureFlags.isPending}
						onToggle={() => {
							const next: FeatureFlags = {
								issue_acceptance: featureFlags?.issue_acceptance ?? true,
								cross_pr_checks: !(featureFlags?.cross_pr_checks ?? false),
								max_linked_prs: featureFlags?.max_linked_prs ?? 5,
							};
							track("settings.toggle_changed", {
								setting_key: "cross_pr_checks",
								new_value: next.cross_pr_checks,
							});
							saveFeatureFlags.mutate(next);
						}}
					/>
				</div>
				<div className="mt-3 flex items-center gap-3 text-[11px] font-mono text-slate-text">
					<label htmlFor="max-linked-prs">Max linked PRs per review:</label>
					<input
						id="max-linked-prs"
						type="number"
						min={1}
						max={20}
						value={featureFlags?.max_linked_prs ?? 5}
						onChange={(e) => {
							const n = parseInt(e.target.value, 10);
							if (isNaN(n) || n < 1 || n > 20) return;
							saveFeatureFlags.mutate({
								issue_acceptance: featureFlags?.issue_acceptance ?? true,
								cross_pr_checks: featureFlags?.cross_pr_checks ?? false,
								max_linked_prs: n,
							});
						}}
						className="w-16 bg-charcoal border border-iron px-2 py-1 text-center text-foreground focus:border-amber focus:outline-none"
					/>
					<span className="text-slate-text/70">bounded 1–20</span>
				</div>
			</section>
		</div>
	);
}

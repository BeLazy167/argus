package admission

import "log/slog"

// AutoRun decides the pull-request webhook path, where nobody asked for the
// review and the repo's own setting is the authority.
//
// Exported, and evaluated by the caller rather than inside Decide, for the
// same reason as Budget: it needs the repo and org settings, which load later
// than the launch site. What matters is that the RULE lives here — it used to
// exist twice in the pipeline package, in two wrappers over one core that
// disagreed about which inputs they consulted.
//
// selfHosted changes the DEFAULT, not the answer. A self-host has no bill to
// gate, so with nothing stored it reviews on push and works out of the box.
// It does not override an explicit setting: a self-hoster who turns auto-review
// off must get auto-review off, and previously did not — the flag forced
// reviews on unconditionally, which was survivable while the default was on
// and a trap once it became off.
//
// explicit reports whether anything was stored at all. Without it "stored
// false" and "unset" are the same value, and the override cannot be removed.
func AutoRun(selfHosted, enabled, explicit bool) Verdict {
	verdict := Signal("auto-review is off for this repo")
	source := "hosted_default"
	if explicit {
		source = "repo_setting"
		if enabled {
			verdict = Allow()
		}
	} else if selfHosted {
		source = "self_hosted_default"
		verdict = Allow()
	}
	slog.Info("admission auto-run decided", "outcome", verdict.Outcome, "source", source,
		"self_hosted", selfHosted, "enabled", enabled, "explicit", explicit)
	return verdict
}

package obs

import (
	"context"
	"strconv"

	"github.com/posthog/posthog-go"
)

// Groups builds a posthog.Groups payload from the ctx-stored installation id
// and repo slug. Returns nil when neither is present — posthog.Capture treats
// a nil Groups as "no group" (no $groups property emitted).
func Groups(ctx context.Context, repo string) posthog.Groups {
	g := posthog.Groups{}
	if id := InstallationID(ctx); id != 0 {
		g["installation"] = strconv.FormatInt(id, 10)
	}
	if repo != "" {
		g["repo"] = repo
	}
	if len(g) == 0 {
		return nil
	}
	return g
}

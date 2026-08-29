package pipeline

// BuiltinPersona is one shipped review style, in the shape the database stores.
//
// It exists so the eight compiled-in personas can be SEEDED as rows and become
// visible and editable, without their prompt text moving out of Go. The switch
// stays the source of truth; a row is a projection of it.
type BuiltinPersona struct {
	Slug           Persona
	Name           string
	PromptOverlay  string
	SpecialistHint string
}

// builtinOrder fixes the display order and, with builtinNames, is the only
// thing this file adds. Everything else is read back out of the switches.
//
// Ordered deliberately rather than ranged over a map: seeding walks this list,
// and a map's random order would rewrite the same rows in a different sequence
// on every boot, making the seed's log output unreadable and its behaviour
// impossible to diff.
//
// custom is absent on purpose — see BuiltinPersonas.
var builtinOrder = []Persona{
	PersonaDefault,
	PersonaSecurityAuditor,
	PersonaPerformanceEngineer,
	PersonaMentor,
	PersonaArchitect,
	PersonaStrict,
	PersonaAdversarial,
	PersonaFreshEyes,
}

// builtinNames are the human labels. The slug is snake_case because it is an
// identifier; showing it in the dashboard is what made a custom persona render
// as the literal word "custom".
var builtinNames = map[Persona]string{
	PersonaDefault:             "Default",
	PersonaSecurityAuditor:     "Security Auditor",
	PersonaPerformanceEngineer: "Performance Engineer",
	PersonaMentor:              "Mentor",
	PersonaArchitect:           "Architect",
	PersonaStrict:              "Strict",
	PersonaAdversarial:         "Adversarial",
	PersonaFreshEyes:           "Fresh Eyes",
}

// BuiltinPersonas returns the shipped personas as rows.
//
// The text is READ FROM the same functions the review path calls, never copied.
// That is the point: a seed holding its own copy of the prompt would drift the
// first time somebody edited the switch alone, and the two would disagree with
// no test able to notice — the row would win for display while the switch won
// for the actual review.
//
// custom is excluded. Its overlay is per-repo text held in settings, so there is
// no single value to seed; writing one would publish one repo's private prompt
// to every repo in the installation.
func BuiltinPersonas() []BuiltinPersona {
	out := make([]BuiltinPersona, 0, len(builtinOrder))
	for _, slug := range builtinOrder {
		out = append(out, BuiltinPersona{
			Slug:           slug,
			Name:           builtinNames[slug],
			PromptOverlay:  PersonaPromptOverlay(slug),
			SpecialistHint: PersonaSpecialistHint(slug),
		})
	}
	return out
}

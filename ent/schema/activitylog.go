package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// ActivityLog holds the audit trail.
type ActivityLog struct {
	ent.Schema
}

func (ActivityLog) Fields() []ent.Field {
	return []ent.Field{
		field.String("action"),
		field.String("actor").Optional().Nillable(),
		field.String("resource").Optional().Nillable(),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Time("created_at").Default(time.Now),
	}
}

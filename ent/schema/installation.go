package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Installation holds the schema for GitHub App installations.
type Installation struct {
	ent.Schema
}

func (Installation) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("installation_id").Unique(),
		field.String("org_login"),
		field.Time("created_at").Default(time.Now),
		field.Time("suspended_at").Optional().Nillable(),
	}
}

func (Installation) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("repos", Repo.Type),
	}
}

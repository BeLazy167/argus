package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Repo holds the schema for tracked repositories.
type Repo struct {
	ent.Schema
}

func (Repo) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("github_id").Unique(),
		field.String("full_name"),
		field.String("default_branch").Default("main"),
		field.Bool("enabled").Default(true),
		field.JSON("settings_json", map[string]any{}).Default(map[string]any{}),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (Repo) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("installation", Installation.Type).Ref("repos").Unique().Required(),
		edge.To("reviews", Review.Type),
		edge.To("model_configs", ModelConfig.Type),
	}
}

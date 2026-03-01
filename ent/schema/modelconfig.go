package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ModelConfig holds per-repo, per-stage LLM configuration.
type ModelConfig struct {
	ent.Schema
}

func (ModelConfig) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("stage").Values("triage", "review", "synthesis", "embedding", "scoring"),
		field.String("provider"),
		field.String("model"),
		field.String("base_url").Optional().Nillable(),
		field.Int("max_tokens").Default(4096),
		field.Float32("temperature").Default(0.2),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (ModelConfig) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("repo", Repo.Type).Ref("model_configs").Unique(),
	}
}

func (ModelConfig) Indexes() []ent.Index {
	return []ent.Index{
		index.Edges("repo").Fields("stage").Unique(),
	}
}

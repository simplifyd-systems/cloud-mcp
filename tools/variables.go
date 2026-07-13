package tools

import (
	"context"

	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// findVariableSlug lists variables and returns the slug of the one whose name
// matches exactly. Used to upsert: the API's create endpoint rejects duplicates.
func findVariableSlug(ctx context.Context, list func(context.Context) ([]cloud.Variable, error), name string) (string, bool) {
	vars, err := list(ctx)
	if err != nil {
		return "", false
	}
	for _, v := range vars {
		if v.Name == name {
			return v.Slug, true
		}
	}
	return "", false
}

// resolveVariableSlug maps a user-supplied variable name or slug to the slug
// the API expects. Falls back to the argument itself when the list can't be
// fetched or nothing matches (the API then returns a proper validation error).
func resolveVariableSlug(ctx context.Context, list func(context.Context) ([]cloud.Variable, error), nameOrSlug string) string {
	vars, err := list(ctx)
	if err != nil {
		return nameOrSlug
	}
	for _, v := range vars {
		if v.Name == nameOrSlug || v.Slug == nameOrSlug {
			return v.Slug
		}
	}
	return nameOrSlug
}

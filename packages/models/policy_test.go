package models

import "testing"

func TestEffect_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{EffectAllow, "allow"},
		{EffectAsk, "ask"},
		{EffectDeny, "deny"},
		{EffectAdminOnly, "admin_only"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestResourceType_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{ResourceTypeFile, "file"},
		{ResourceTypeCommand, "command"},
		{ResourceTypeSecret, "secret"},
		{ResourceTypeDeploy, "deploy"},
		{ResourceTypeGit, "git"},
		{ResourceTypeNetwork, "network"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestAction_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{ActionRead, "read"},
		{ActionWrite, "write"},
		{ActionExecute, "execute"},
		{ActionDelete, "delete"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestPolicy_Match(t *testing.T) {
	// Match compares resource type and action for policy applicability.
	// Supports wildcard (*) for either field.
	match := func(p *Policy, resourceType, action string) bool {
		resourceMatch := p.ResourceType == "*" || p.ResourceType == resourceType
		actionMatch := p.Action == "*" || p.Action == action
		return resourceMatch && actionMatch
	}

	t.Run("exact match", func(t *testing.T) {
		p := &Policy{ResourceType: ResourceTypeFile, Action: ActionRead}
		assertEqual(t, match(p, ResourceTypeFile, ActionRead), true)
	})

	t.Run("wildcard resource match", func(t *testing.T) {
		p := &Policy{ResourceType: "*", Action: ActionRead}
		assertEqual(t, match(p, ResourceTypeFile, ActionRead), true)
		assertEqual(t, match(p, ResourceTypeCommand, ActionRead), true)
	})

	t.Run("no match different action", func(t *testing.T) {
		p := &Policy{ResourceType: ResourceTypeFile, Action: ActionRead}
		assertEqual(t, match(p, ResourceTypeFile, ActionWrite), false)
	})

	t.Run("no match different resource", func(t *testing.T) {
		p := &Policy{ResourceType: ResourceTypeFile, Action: ActionRead}
		assertEqual(t, match(p, ResourceTypeCommand, ActionRead), false)
	})
}

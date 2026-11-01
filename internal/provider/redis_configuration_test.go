package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestParseRedisSettings(t *testing.T) {
	for _, test := range []struct {
		input string
		valid bool
	}{
		{input: `{"tlsOnly":true,"saveToDisk":false,"maxMemoryPolicy":"allkeys-lru","modules":["json"],"notifyKeyspaceEvents":"K"}`, valid: true},
		{input: `{}`, valid: true},
		{input: `null`},
		{input: `[]`},
		{input: `{"credentials":{"password":"secret"}}`},
		{input: `{"tlsOnly":"true"}`},
		{input: `{"modules":null}`},
	} {
		_, err := parseRedisSettings(test.input)
		if (err == nil) != test.valid {
			t.Fatalf("unexpected result for %q: %v", test.input, err)
		}
	}
}

func TestManagedRedisSettingsDetectDrift(t *testing.T) {
	client := &fakeRedisCLI{responses: map[string]json.RawMessage{
		"service-configuration-get": json.RawMessage(`{"success":true,"configuration":{"tlsOnly":true,"maxMemoryPolicy":"noeviction","credentials":{"password":"secret"}}}`),
	}}
	input := `{ "tlsOnly": true }`
	actual, err := readManagedRedisSettings(context.Background(), client, "svc-123", input)
	if err != nil || actual != input {
		t.Fatalf("equivalent JSON should preserve state: %q, %v", actual, err)
	}
	actual, err = readManagedRedisSettings(context.Background(), client, "svc-123", `{"tlsOnly":false}`)
	if err != nil || actual != `{"tlsOnly":true}` || strings.Contains(actual, "secret") {
		t.Fatalf("unexpected managed drift: %q, %v", actual, err)
	}
}

func TestRedisConfigurationRemovalRequiresReplacement(t *testing.T) {
	for _, test := range []struct {
		name    string
		state   types.String
		plan    types.String
		replace bool
	}{
		{name: "remove all", state: types.StringValue(`{"tlsOnly":true}`), plan: types.StringNull(), replace: true},
		{name: "remove key", state: types.StringValue(`{"tlsOnly":true,"saveToDisk":true}`), plan: types.StringValue(`{"tlsOnly":true}`), replace: true},
		{name: "change value", state: types.StringValue(`{"tlsOnly":true}`), plan: types.StringValue(`{"tlsOnly":false}`)},
		{name: "add key", state: types.StringValue(`{"tlsOnly":true}`), plan: types.StringValue(`{"tlsOnly":true,"saveToDisk":true}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var response stringplanmodifier.RequiresReplaceIfFuncResponse
			redisConfigurationRequiresReplace(context.Background(), planmodifier.StringRequest{StateValue: test.state, PlanValue: test.plan}, &response)
			if response.RequiresReplace != test.replace {
				t.Fatalf("expected replacement=%v, got %v", test.replace, response.RequiresReplace)
			}
		})
	}
}

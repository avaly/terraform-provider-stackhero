package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
)

func redisConfigurationRequiresReplace(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() || req.PlanValue.IsUnknown() {
		return
	}
	if req.PlanValue.IsNull() {
		resp.RequiresReplace = true
		return
	}
	previous, previousErr := parseRedisSettings(req.StateValue.ValueString())
	next, nextErr := parseRedisSettings(req.PlanValue.ValueString())
	if previousErr != nil || nextErr != nil {
		return
	}
	for key := range previous {
		if _, ok := next[key]; !ok {
			resp.RequiresReplace = true
			return
		}
	}
}

func parseRedisSettings(input string) (map[string]json.RawMessage, error) {
	var settings map[string]json.RawMessage
	if err := json.Unmarshal([]byte(input), &settings); err != nil || settings == nil {
		return nil, fmt.Errorf("configuration_json must be a JSON object")
	}
	for key, value := range settings {
		var valid bool
		switch key {
		case "tlsOnly", "saveToDisk":
			var item *bool
			valid = json.Unmarshal(value, &item) == nil && item != nil
		case "maxMemoryPolicy", "notifyKeyspaceEvents":
			var item *string
			valid = json.Unmarshal(value, &item) == nil && item != nil
		case "modules":
			var item []string
			valid = json.Unmarshal(value, &item) == nil && item != nil
		default:
			return nil, fmt.Errorf("configuration_json contains an unsupported setting")
		}
		if !valid {
			return nil, fmt.Errorf("configuration_json contains a setting with an invalid type")
		}
	}
	return settings, nil
}

func getRedisConfiguration(ctx context.Context, client stackCLI, id string) (map[string]json.RawMessage, error) {
	output, err := client.JSON(ctx, "service-configuration-get", "--service="+id)
	if err != nil {
		return nil, err
	}
	var response struct {
		Configuration map[string]json.RawMessage `json:"configuration"`
	}
	if err := json.Unmarshal(output, &response); err != nil || response.Configuration == nil {
		return nil, fmt.Errorf("stackhero service-configuration-get returned an invalid configuration")
	}
	return response.Configuration, nil
}

func applyRedisSettings(ctx context.Context, client stackCLI, id, input string) error {
	settings, err := parseRedisSettings(input)
	if err != nil {
		return err
	}
	configuration, err := getRedisConfiguration(ctx, client, id)
	if err != nil {
		return err
	}
	for key, value := range settings {
		if _, ok := configuration[key]; !ok {
			return fmt.Errorf("a managed Redis setting is not present in the remote configuration")
		}
		configuration[key] = value
	}
	merged, err := json.Marshal(configuration)
	if err != nil {
		return fmt.Errorf("unable to encode Redis configuration")
	}
	if _, err := client.JSON(ctx, "service-configuration-set", "--service="+id, "--configuration="+string(merged)); err != nil {
		return err
	}
	if err := waitForRedis(ctx, client, id, "running"); err != nil {
		return err
	}
	actual, err := readManagedRedisSettings(ctx, client, id, input)
	if err != nil {
		return err
	}
	if actual != input {
		return fmt.Errorf("the requested Redis configuration was not applied")
	}
	return nil
}

func readManagedRedisSettings(ctx context.Context, client stackCLI, id, input string) (string, error) {
	wanted, err := parseRedisSettings(input)
	if err != nil {
		return "", err
	}
	configuration, err := getRedisConfiguration(ctx, client, id)
	if err != nil {
		return "", err
	}
	actual := make(map[string]json.RawMessage, len(wanted))
	changed := false
	for key, expected := range wanted {
		value, ok := configuration[key]
		if !ok {
			return "", fmt.Errorf("a managed Redis setting is missing from the remote configuration")
		}
		var expectedValue, actualValue any
		if json.Unmarshal(expected, &expectedValue) != nil || json.Unmarshal(value, &actualValue) != nil {
			return "", fmt.Errorf("a managed Redis setting has invalid JSON")
		}
		actual[key] = value
		if !reflect.DeepEqual(expectedValue, actualValue) {
			changed = true
		}
	}
	if !changed {
		return input, nil
	}
	output, err := json.Marshal(actual)
	if err != nil {
		return "", fmt.Errorf("unable to encode managed Redis settings")
	}
	return string(output), nil
}

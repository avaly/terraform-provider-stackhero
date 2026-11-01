package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

type terraformFakeState struct {
	Stack         bool           `json:"stack"`
	Redis         bool           `json:"redis"`
	Configuration map[string]any `json:"configuration"`
}

func TestMain(testMain *testing.M) {
	if os.Getenv("STACKHERO_PROVIDER_FAKE_CLI") == "1" {
		os.Exit(runTerraformFakeCLI())
	}
	os.Exit(testMain.Run())
}

func runTerraformFakeCLI() int {
	statePath := os.Getenv("STACKHERO_PROVIDER_FAKE_STATE")
	var state terraformFakeState
	if data, err := os.ReadFile(statePath); err == nil {
		if json.Unmarshal(data, &state) != nil {
			return 1
		}
	}
	if len(os.Args) < 3 {
		return 1
	}
	command := os.Args[2]
	var response any = map[string]any{"success": true}
	script := ""
	switch command {
	case "--help":
		response = map[string]any{"success": true, "version": "v1.2.0"}
	case "stack-create":
		state.Stack = true
		script = "stk-123"
	case "stacks-list":
		stacks := []any{}
		if state.Stack {
			stacks = append(stacks, map[string]any{"id": "stk-123", "name": "example"})
		}
		response = map[string]any{"success": true, "data": stacks}
	case "stack-delete":
		if state.Redis {
			return 1
		}
		state.Stack = false
	case "service-add":
		state.Redis = true
		state.Configuration = map[string]any{"domain": "redis.example", "credentials": map[string]any{"password": "fake-password"}, "tlsOnly": false}
		script = "svc-123"
	case "services-list":
		services := []any{}
		if state.Redis {
			services = append(services, map[string]any{"id": "svc-123", "stack": map[string]any{"id": "stk-123"}})
		}
		response = map[string]any{"success": true, "data": services}
	case "service-wait-for":
		status := "deleted"
		if state.Redis {
			status = "running"
		}
		response = map[string]any{"success": true, "state": status}
	case "service-configuration-get":
		response = map[string]any{"success": true, "configuration": state.Configuration}
	case "service-configuration-set":
		for _, arg := range os.Args[3:] {
			if strings.HasPrefix(arg, "--configuration=") && json.Unmarshal([]byte(strings.TrimPrefix(arg, "--configuration=")), &state.Configuration) != nil {
				return 1
			}
		}
	case "service-delete":
		state.Redis = false
	default:
		return 1
	}
	data, err := json.Marshal(state)
	if err != nil || os.WriteFile(statePath, data, 0600) != nil {
		return 1
	}
	if script != "" {
		fmt.Println(script)
		return 0
	}
	if json.NewEncoder(os.Stdout).Encode(response) != nil {
		return 1
	}
	return 0
}

func TestTerraformLifecycleWithFakeCLI(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("set TF_ACC=1 to run Terraform integration tests")
	}
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STACKHERO_PROVIDER_FAKE_CLI", "1")
	t.Setenv("STACKHERO_PROVIDER_FAKE_STATE", t.TempDir()+"/state.json")
	t.Setenv("STACKHERO_TOKEN", "fake-token")
	config := func(tlsOnly bool) string {
		return fmt.Sprintf(`
provider "stackhero" { cli_path = %q }
resource "stackhero_stack" "test" { name = "example" }
resource "stackhero_redis" "test" {
  stack_id = stackhero_stack.test.id
  instance = "1G"
  region = "europe"
  configuration_json = jsonencode({ tlsOnly = %t })
}
`, path, tlsOnly)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"stackhero": providerserver.NewProtocol6WithError(New("test")()),
		},
		Steps: []resource.TestStep{
			{
				Config: config(true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("stackhero_stack.test", "id", "stk-123"),
					resource.TestCheckResourceAttr("stackhero_redis.test", "id", "svc-123"),
					resource.TestCheckResourceAttr("stackhero_redis.test", "domain", "redis.example"),
					resource.TestCheckResourceAttr("stackhero_redis.test", "password", "fake-password"),
				),
			},
			{ResourceName: "stackhero_stack.test", ImportState: true, ImportStateVerify: true},
			{Config: config(false)},
			{Config: strings.Replace(config(false), "  configuration_json = jsonencode({ tlsOnly = false })\n", "", 1)},
		},
	})
}

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/avaly/terraform-provider-stackhero/internal/stackhero"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestLiveRedisLifecycle(t *testing.T) {
	if os.Getenv("TF_ACC") != "1" || os.Getenv("STACKHERO_LIVE_TEST") != "1" {
		t.Skip("set TF_ACC=1 and STACKHERO_LIVE_TEST=1 to run billable Stackhero tests")
	}
	if os.Getenv("STACKHERO_TOKEN") == "" {
		t.Fatal("STACKHERO_TOKEN must be set for live acceptance tests")
	}
	instance := os.Getenv("STACKHERO_TEST_INSTANCE")
	if instance == "" {
		instance = "1G"
	}
	region := os.Getenv("STACKHERO_TEST_REGION")
	if region == "" {
		region = "europe"
	}
	name := fmt.Sprintf("terraform-provider-test-%d", time.Now().UnixNano())
	cliPath, err := exec.LookPath("stackhero")
	if err != nil {
		t.Fatal("stackhero CLI must be installed for live acceptance tests")
	}
	client := stackhero.NewClient(cliPath, os.Getenv("STACKHERO_TOKEN"))
	t.Cleanup(func() { cleanupLiveRedis(t, client, name) })
	config := func(policy string) string {
		return fmt.Sprintf(`
provider "stackhero" {}
resource "stackhero_stack" "test" { name = %q }
resource "stackhero_redis" "test" {
  stack_id = stackhero_stack.test.id
  instance = %q
  region = %q
  configuration_json = jsonencode({ maxMemoryPolicy = %q })
}
`, name, instance, region, policy)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"stackhero": providerserver.NewProtocol6WithError(New("test")()),
		},
		Steps: []resource.TestStep{
			{
				Config: config("noeviction"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("stackhero_stack.test", "id"),
					resource.TestCheckResourceAttrSet("stackhero_redis.test", "id"),
					resource.TestCheckResourceAttrSet("stackhero_redis.test", "domain"),
					resource.TestCheckResourceAttrSet("stackhero_redis.test", "password"),
				),
			},
			{ResourceName: "stackhero_stack.test", ImportState: true, ImportStateVerify: true},
			{Config: config("allkeys-lru")},
		},
	})
}

func cleanupLiveRedis(t *testing.T, client stackCLI, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	output, err := client.JSON(ctx, "stacks-list")
	if err != nil {
		t.Errorf("live cleanup: cannot list stack %q: %v", name, err)
		return
	}
	var stacks struct {
		Data *[]struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if json.Unmarshal(output, &stacks) != nil || stacks.Data == nil {
		t.Errorf("live cleanup: cannot parse stack list for %q", name)
		return
	}
	stackID := ""
	for _, stack := range *stacks.Data {
		if stack.Name == name {
			if stackID != "" || stack.ID == "" {
				t.Errorf("live cleanup: multiple stacks named %q; inspect them manually", name)
				return
			}
			stackID = stack.ID
		}
	}
	if stackID == "" {
		return
	}
	output, err = client.JSON(ctx, "services-list")
	if err != nil {
		t.Errorf("live cleanup: cannot list services in stack %s: %v", stackID, err)
		return
	}
	var services struct {
		Data *[]struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Stack struct {
				ID string `json:"id"`
			} `json:"stack"`
		} `json:"data"`
	}
	if json.Unmarshal(output, &services) != nil || services.Data == nil {
		t.Errorf("live cleanup: cannot parse services in stack %s", stackID)
		return
	}
	serviceID := ""
	for _, service := range *services.Data {
		if service.Stack.ID == stackID {
			if serviceID != "" || service.Name != "Redis" || service.ID == "" {
				t.Errorf("live cleanup: unexpected services in stack %s; inspect it manually", stackID)
				return
			}
			serviceID = service.ID
		}
	}
	if serviceID != "" {
		_, err := client.JSON(ctx, "service-delete", "--service="+serviceID, "--confirm")
		if err != nil && !errors.Is(err, stackhero.ErrNotFound) {
			t.Errorf("live cleanup: cannot delete service %s: %v", serviceID, err)
			return
		}
		if err == nil {
			if err := waitForRedis(ctx, client, serviceID, "deleted"); err != nil {
				t.Errorf("live cleanup: cannot wait for service %s: %v", serviceID, err)
				return
			}
		}
		if err := waitForRedisAbsence(ctx, client, serviceID); err != nil {
			t.Errorf("live cleanup: service %s still exists: %v", serviceID, err)
			return
		}
	}
	if _, err := client.JSON(ctx, "stack-delete", "--stack="+stackID, "--confirm"); err != nil && !errors.Is(err, stackhero.ErrNotFound) {
		t.Errorf("live cleanup: cannot delete stack %s: %v", stackID, err)
	}
}
